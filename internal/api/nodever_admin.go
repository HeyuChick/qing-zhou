package api

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/assets"
	"qingzhou/internal/sbver"
	"qingzhou/internal/store"
)

var (
	errNotFound  = errors.New("服务器不存在")
	errNoCreds   = errors.New("未配置 SSH 密钥或密码，无法连接")
	errLinuxOnly = errors.New("一键升级仅支持 Linux 部署；请在服务器上手动执行安装脚本")
)

func runtimeIsLinux() bool { return runtime.GOOS == "linux" }

// nodeVersionView is one row of the node sing-box list.
type nodeVersionView struct {
	ServerID    int64  `json:"server_id"` // 0 = the panel's own machine
	Name        string `json:"name"`
	Host        string `json:"host"`
	Local       bool   `json:"local"`
	Enabled     bool   `json:"enabled"`
	Version     string `json:"version"`
	Raw         string `json:"raw"`
	HasV2RayAPI bool   `json:"has_v2ray_api"`
	CheckedAt   int64  `json:"checked_at"`
	Error       string `json:"error"`
	// TooOld means the generated config would be rejected by this binary — which
	// on a node means the panel has silently stopped being able to deploy to it.
	TooOld bool `json:"too_old"`
	// Upgradable is false for the local node when the panel is not on Linux, and
	// for remote nodes with no SSH credentials configured.
	Upgradable bool `json:"upgradable"`
}

// GET /api/admin/nodes/singbox — which sing-box each node is running.
//
// The panel already ran `sing-box version` on every remote node to decide
// whether traffic could be metered there, and discarded the answer. An operator
// who installed with the one-line script had no way to see, from the panel,
// what a node ended up with — or that it had been left behind.
func (a *API) handleAdminNodeVersions(w http.ResponseWriter, r *http.Request) {
	observed, err := a.st.NodeSingboxAll()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取失败")
		return
	}
	servers, err := a.st.ListServers()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取服务器失败")
		return
	}

	rows := []nodeVersionView{viewFor(store.LocalNodeID, "面板本机", "", true, true, observed)}
	for _, sv := range servers {
		v := viewFor(sv.ID, sv.Name, sv.Host, false, sv.Enabled, observed)
		// Without credentials there is no way in, so the button must not pretend.
		v.Upgradable = sv.SSHKey != "" || sv.SSHPassword != ""
		rows = append(rows, v)
	}
	ok(w, J{
		"nodes": rows,
		// Stated rather than hardcoded in the UI so the two cannot drift.
		"min_supported": sbver.MinSupported,
	})
}

func viewFor(id int64, name, host string, local, enabled bool, observed map[int64]*store.NodeSingbox) nodeVersionView {
	v := nodeVersionView{ServerID: id, Name: name, Host: host, Local: local, Enabled: enabled}
	if local {
		// The local upgrade shells out to bash; the install script is Linux-only.
		v.Upgradable = runtimeIsLinux()
	}
	if n := observed[id]; n != nil {
		v.Version, v.Raw, v.HasV2RayAPI = n.Version, n.Raw, n.HasV2RayAPI
		v.CheckedAt, v.Error = n.CheckedAt, n.Error
		v.TooOld = sbver.Info{Version: n.Version}.TooOld()
	}
	return v
}

// POST /api/admin/nodes/singbox/refresh — re-probe every node now.
func (a *API) handleAdminNodeVersionRefresh(w http.ResponseWriter, r *http.Request) {
	if a.sbctl == nil {
		fail(w, http.StatusServiceUnavailable, "sing-box 控制器未启用")
		return
	}
	// Probing every node over SSH can take a while on an unreachable one, so it
	// runs in the background and the client re-reads the list.
	go a.sbctl.RefreshVersions()
	ok(w, J{"started": true})
}

// POST /api/admin/nodes/{id}/singbox/upgrade — reinstall sing-box on one node.
//
// Synchronous on purpose: the download is tens of megabytes and the operator
// needs the script's output when it fails. The handler's own timeout bounds it.
func (a *API) handleAdminNodeSingboxUpgrade(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()

	var out string
	var err error
	if id == store.LocalNodeID {
		out, err = upgradeLocalSingBox(ctx)
	} else {
		out, err = a.upgradeRemoteSingBox(ctx, id)
	}
	if err != nil {
		// The script's output is the diagnosis; without it the operator gets
		// "exit status 1" and nothing to act on.
		fail(w, http.StatusBadGateway, trimOutput(err.Error()+"\n"+out))
		return
	}
	// Re-probe so the panel shows the new number rather than the old one.
	if a.sbctl != nil {
		a.sbctl.RefreshVersions()
	}
	ok(w, J{"output": trimOutput(out)})
}

func (a *API) upgradeRemoteSingBox(ctx context.Context, id int64) (string, error) {
	sv, err := a.st.GetServer(id)
	if err != nil || sv == nil {
		return "", errNotFound
	}
	if sv.SSHKey == "" && sv.SSHPassword == "" {
		return "", errNoCreds
	}
	// Generous timeout: the script downloads a binary of tens of megabytes.
	rm := a.newRemoteManager(2 * time.Minute)
	return rm.UpgradeSingBox(ctx, sshConfigFor(sv), assets.InstallScript())
}

// upgradeLocalSingBox runs the same script on the panel's own machine.
func upgradeLocalSingBox(ctx context.Context) (string, error) {
	if !runtimeIsLinux() {
		return "", errLinuxOnly
	}
	cmd := exec.CommandContext(ctx, "bash", "-s", "--", "--force")
	cmd.Stdin = strings.NewReader(assets.InstallScript())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// trimOutput bounds what a node's script can push into an API response.
func trimOutput(s string) string {
	s = strings.TrimSpace(s)
	const max = 8 << 10
	if len(s) > max {
		return "…" + s[len(s)-max:]
	}
	return s
}
