package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/idgen"
	"qingzhou/internal/sbctl"
	"qingzhou/internal/store"
)

var errUnsupportedProbeArch = errors.New("不支持的探针架构")

// probeBinaryPath resolves the exact binary this panel already serves from the
// public download endpoint. One-click SSH installs and copied shell commands
// therefore install byte-for-byte the same release.
func probeBinaryPath(arch string) (string, error) {
	if arch != "linux-amd64" && arch != "linux-arm64" {
		return "", errUnsupportedProbeArch
	}
	dir := os.Getenv("QZ_PROBE_DIR")
	if dir == "" {
		dir = "cmd/probe/dist"
	}
	return filepath.Join(dir, "probe-"+arch), nil
}

func normalizeProbeArch(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "amd64", "x86_64":
		return "linux-amd64", nil
	case "arm64", "aarch64":
		return "linux-arm64", nil
	default:
		return "", fmt.Errorf("%w：%s", errUnsupportedProbeArch, strings.TrimSpace(raw))
	}
}

func (a *API) probeUpgradeSnapshot() map[int64]nodeUpgradeJob {
	a.probeUpgradeMu.Lock()
	defer a.probeUpgradeMu.Unlock()
	out := make(map[int64]nodeUpgradeJob, len(a.probeUpgradeJobs))
	for id, j := range a.probeUpgradeJobs {
		out[id] = *j
	}
	return out
}

func (a *API) claimProbeUpgrade(id int64) bool {
	a.probeUpgradeMu.Lock()
	defer a.probeUpgradeMu.Unlock()
	if a.probeUpgradeJobs == nil {
		a.probeUpgradeJobs = map[int64]*nodeUpgradeJob{}
	}
	if j := a.probeUpgradeJobs[id]; j != nil && j.Running {
		return false
	}
	a.probeUpgradeJobs[id] = &nodeUpgradeJob{Running: true}
	return true
}

func (a *API) finishProbeUpgrade(id int64, out string, err error) {
	a.probeUpgradeMu.Lock()
	defer a.probeUpgradeMu.Unlock()
	j := a.probeUpgradeJobs[id]
	if j == nil {
		return
	}
	j.Running, j.FinishedAt, j.Output = false, time.Now().Unix(), trimOutput(out)
	if err != nil {
		j.Error = trimOutput(err.Error() + "\n" + out)
	}
}

func (a *API) cancelProbeUpgrade(id int64) {
	a.probeUpgradeMu.Lock()
	defer a.probeUpgradeMu.Unlock()
	delete(a.probeUpgradeJobs, id)
}

// handleAdminProbeUpgrade enables monitoring if necessary, then installs the
// panel's bundled probe over the server row's verified SSH connection. The job
// continues after the initiating HTTP request returns; the monitor list carries
// progress and the final error/output.
func (a *API) handleAdminProbeUpgrade(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id == store.LocalNodeID {
		fail(w, http.StatusBadRequest, "面板本机使用内置采集，无需安装探针")
		return
	}
	sv, err := a.st.GetServer(id)
	if err != nil || sv == nil {
		fail(w, http.StatusNotFound, errNotFound.Error())
		return
	}
	if !serverHasCredentials(sv) {
		fail(w, http.StatusBadRequest, errNoCreds.Error())
		return
	}
	base := a.publicBase(r)
	if base == "" || strings.ContainsAny(base, "\r\n") {
		fail(w, http.StatusBadRequest, "面板访问地址无效，请先在系统设置中配置访问地址")
		return
	}
	// Clicking install is also an explicit request to monitor this server. Mint
	// the token here so a brand-new server really is one click, not "enable,
	// save, return, then install".
	if sv.ProbeToken == "" {
		sv.ProbeToken, err = idgen.RandToken(24)
		if err != nil {
			fail(w, http.StatusInternalServerError, "生成探针 token 失败")
			return
		}
	}
	if !a.claimProbeUpgrade(id) {
		fail(w, http.StatusConflict, "该机器的探针正在安装中，请等待本次完成")
		return
	}
	if err := a.st.EnableServerProbe(id, sv.ProbeToken); err != nil {
		a.cancelProbeUpgrade(id)
		fail(w, http.StatusInternalServerError, "启用探针失败")
		return
	}
	// Re-read after the narrow update so the background job uses the latest SSH
	// credentials and host-key pin if another admin edit landed concurrently.
	sv, err = a.st.GetServer(id)
	if err != nil || sv == nil {
		a.cancelProbeUpgrade(id)
		fail(w, http.StatusInternalServerError, "重新读取服务器失败")
		return
	}

	// Copy values before the request and its context disappear.
	token := sv.ProbeToken
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		out, err := a.upgradeRemoteProbe(ctx, sv, base, token)
		a.finishProbeUpgrade(id, out, err)
	}()
	ok(w, J{"started": true})
}

func (a *API) upgradeRemoteProbe(ctx context.Context, sv *store.Server, base, token string) (string, error) {
	rm := a.newRemoteManager(90 * time.Second)
	cfg := sbctl.SSHConfigFor(sv)
	rawArch, err := rm.RunCommand(ctx, cfg, "uname -m")
	if err != nil {
		return rawArch, fmt.Errorf("读取服务器架构失败: %w", err)
	}
	arch, err := normalizeProbeArch(rawArch)
	if err != nil {
		return rawArch, err
	}
	path, err := probeBinaryPath(arch)
	if err != nil {
		return "", err
	}
	bin, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取面板内置探针 %s 失败: %w", arch, err)
	}
	if len(bin) == 0 || len(bin) > 64<<20 {
		return "", fmt.Errorf("面板内置探针大小异常：%d bytes", len(bin))
	}
	return rm.InstallProbe(ctx, cfg, bin, base, token)
}
