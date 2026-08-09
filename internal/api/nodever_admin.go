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
	// Reinstall job state, so the list the UI already polls is also what tells it
	// how the reinstall went. See nodeUpgradeJob.
	Upgrading     bool   `json:"upgrading"`
	UpgradeOutput string `json:"upgrade_output,omitempty"`
	UpgradeError  string `json:"upgrade_error,omitempty"`
	UpgradedAt    int64  `json:"upgraded_at,omitempty"`
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

	jobs := a.upgradeSnapshot()
	rows := []nodeVersionView{viewFor(store.LocalNodeID, "面板本机", "", true, true, observed, jobs)}
	for _, sv := range servers {
		v := viewFor(sv.ID, sv.Name, sv.Host, false, sv.Enabled, observed, jobs)
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

func viewFor(id int64, name, host string, local, enabled bool, observed map[int64]*store.NodeSingbox, jobs map[int64]nodeUpgradeJob) nodeVersionView {
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
	if j, ok := jobs[id]; ok {
		v.Upgrading, v.UpgradeOutput, v.UpgradeError = j.Running, j.Output, j.Error
		v.UpgradedAt = j.FinishedAt
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

// nodeUpgradeJob is the state of one node's reinstall, kept in memory so the
// node list can report it. Memory rather than a table: a job cannot outlive the
// process that runs it, and a panel restart mid-install leaves the node's
// actual version to be settled by the next probe anyway.
type nodeUpgradeJob struct {
	Running    bool
	FinishedAt int64
	Output     string
	Error      string
}

// upgradeSnapshot copies the job table for reading.
func (a *API) upgradeSnapshot() map[int64]nodeUpgradeJob {
	a.upgradeMu.Lock()
	defer a.upgradeMu.Unlock()
	out := make(map[int64]nodeUpgradeJob, len(a.upgradeJobs))
	for id, j := range a.upgradeJobs {
		out[id] = *j
	}
	return out
}

// claimUpgrade reserves the job slot for one node, reporting false when a
// reinstall is already running there. Two concurrent runs of the install script
// on one machine would race over the same binary and unit file.
func (a *API) claimUpgrade(id int64) bool {
	a.upgradeMu.Lock()
	defer a.upgradeMu.Unlock()
	if a.upgradeJobs == nil {
		a.upgradeJobs = map[int64]*nodeUpgradeJob{}
	}
	if j := a.upgradeJobs[id]; j != nil && j.Running {
		return false
	}
	a.upgradeJobs[id] = &nodeUpgradeJob{Running: true}
	return true
}

func (a *API) finishUpgrade(id int64, out string, err error) {
	a.upgradeMu.Lock()
	defer a.upgradeMu.Unlock()
	j := a.upgradeJobs[id]
	if j == nil {
		return
	}
	j.Running, j.FinishedAt, j.Output = false, time.Now().Unix(), trimOutput(out)
	if err != nil {
		// The script's output is the diagnosis; without it the operator gets
		// "exit status 1" and nothing to act on.
		j.Error = trimOutput(err.Error() + "\n" + out)
	}
}

// POST /api/admin/nodes/{id}/singbox/upgrade — reinstall sing-box on one node.
//
// Starts the job and returns immediately; the client polls the node list, which
// carries the result. It cannot be synchronous: the script downloads a sing-box
// of ~60MB onto the node, which routinely takes longer than the server's
// WriteTimeout (main.go), so the response was torn off mid-flight and the panel
// reported a failure for an install that had in fact succeeded. Same reason
// sbRebuildLog exists — see the note on it in router.go.
//
// What the node did is not lost by going async: the script's output is kept on
// the job and shown when the poll picks it up.
func (a *API) handleAdminNodeSingboxUpgrade(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	// Everything that can be judged without touching the node is judged now, so
	// a misconfigured server still answers with a real error instead of a job
	// that fails a minute later somewhere the operator has to go looking.
	if id == store.LocalNodeID {
		if !runtimeIsLinux() {
			fail(w, http.StatusBadRequest, errLinuxOnly.Error())
			return
		}
	} else if err := a.checkRemoteUpgradable(id); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.claimUpgrade(id) {
		fail(w, http.StatusConflict, "该节点正在安装中，请等待本次完成")
		return
	}

	go func() {
		// context.Background, not r.Context: the request is already answered, and
		// inheriting its context would cancel the install the moment it is.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var out string
		var err error
		if id == store.LocalNodeID {
			out, err = upgradeLocalSingBox(ctx)
		} else {
			out, err = a.upgradeRemoteSingBox(ctx, id)
		}
		a.finishUpgrade(id, out, err)
		// Re-probe so the panel shows the new number rather than the old one.
		// After the job is marked done, so a poll that sees "finished" sees the
		// refreshed version too.
		if err == nil && a.sbctl != nil {
			a.sbctl.RefreshVersions()
		}
	}()
	ok(w, J{"started": true})
}

// checkRemoteUpgradable reports why a node cannot be reinstalled, or nil.
func (a *API) checkRemoteUpgradable(id int64) error {
	sv, err := a.st.GetServer(id)
	if err != nil || sv == nil {
		return errNotFound
	}
	if sv.SSHKey == "" && sv.SSHPassword == "" {
		return errNoCreds
	}
	return nil
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
