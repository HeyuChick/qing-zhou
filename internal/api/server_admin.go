package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/sbver"
	"qingzhou/internal/sshctl"
	"qingzhou/internal/store"
)

// ---- server management (multi-server sing-box orchestration) ----

// maskServerSecrets replaces stored SSH credentials with a "***" sentinel
// before returning a server to the client. The update handler preserves any
// field still equal to "***", so secrets never round-trip through the browser.
func maskServerSecrets(sv *store.Server) {
	if sv == nil {
		return
	}
	if sv.SSHKey != "" {
		sv.SSHKey = "***"
	}
	if sv.SSHKeyPass != "" {
		sv.SSHKeyPass = "***"
	}
	if sv.SSHPassword != "" {
		sv.SSHPassword = "***"
	}
}

func (a *API) handleAdminListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.st.ListServers()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取服务器失败")
		return
	}
	for _, sv := range servers {
		maskServerSecrets(sv)
	}
	ok(w, servers)
}

func (a *API) handleAdminCreateServer(w http.ResponseWriter, r *http.Request) {
	var sv store.Server
	if err := json.NewDecoder(r.Body).Decode(&sv); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	sv.Name = strings.TrimSpace(sv.Name)
	sv.Host = strings.TrimSpace(sv.Host)
	if sv.Name == "" || sv.Host == "" {
		fail(w, http.StatusBadRequest, "名称和主机不能为空")
		return
	}
	if sv.Port == 0 {
		sv.Port = 22
	}
	sv.Enabled = true
	sv.Status = "unknown"
	id, err := a.st.CreateServer(sv)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建服务器失败")
		return
	}
	created, _ := a.st.GetServer(id)
	maskServerSecrets(created)
	ok(w, created)
}

func (a *API) handleAdminUpdateServer(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))

	// Decode ONTO the stored row, not into a zero value. UpdateServer writes all
	// 22 columns, while the edit form only posts nine — so decoding into a fresh
	// struct silently blanked everything it doesn't carry: the SSH key/password,
	// the probe token (breaking the running agent and flipping probe_enabled
	// off), and every field owned by the 监控管理 page (expiry_date, provider,
	// location, spec, price, notes). Renaming a server or changing its port was
	// enough to destroy its credentials irrecoverably.
	//
	// json.Decode leaves fields absent from the payload untouched, so this keeps
	// stored values for anything the caller didn't send.
	sv, err := a.st.GetServer(id)
	if err != nil || sv == nil {
		fail(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if err := json.NewDecoder(r.Body).Decode(sv); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	sv.ID = id
	sv.Name = strings.TrimSpace(sv.Name)
	sv.Host = strings.TrimSpace(sv.Host)
	if sv.Name == "" || sv.Host == "" {
		fail(w, http.StatusBadRequest, "名称和主机不能为空")
		return
	}
	if sv.Port == 0 {
		sv.Port = 22
	}
	// The list response masks secrets as "***"; a client that echoes the masked
	// value back must not store the literal. sv already holds the stored value
	// (it was decoded onto the stored row), so this just undoes the echo.
	stored, _ := a.st.GetServer(id)
	if stored != nil {
		if sv.SSHKey == "***" {
			sv.SSHKey = stored.SSHKey
		}
		if sv.SSHKeyPass == "***" {
			sv.SSHKeyPass = stored.SSHKeyPass
		}
		if sv.SSHPassword == "***" {
			sv.SSHPassword = stored.SSHPassword
		}
		if sv.ProbeToken == "***" {
			sv.ProbeToken = stored.ProbeToken
		}
	}
	if err := a.st.UpdateServer(*sv); err != nil {
		fail(w, http.StatusInternalServerError, "更新服务器失败")
		return
	}
	// Point the row at a different host and the pinned key stops being about the
	// machine we are dialling: it belongs to the old one, so every later connect
	// fails as "host key mismatch (possible MITM)" for what is really just a
	// different machine. OpenSSH indexes known_hosts BY host for the same reason;
	// storing the pin on the server row is what makes the host field mutable
	// underneath it. Drop the pin so the next connect re-pins by TOFU.
	//
	// Deliberately NOT done for a credential change: a new SSH password says
	// nothing about the machine's identity, and clearing on it would hand anyone
	// who can talk an admin into a password rotation a free way to make the panel
	// forget who it is talking to.
	if stored != nil && stored.Host != sv.Host && stored.HostKey != "" {
		if err := a.st.SetServerHostKey(id, ""); err != nil {
			log.Printf("admin: clear pinned host key after host change on server %d: %v", id, err)
		} else {
			log.Printf("admin: server %d host changed %s -> %s, pinned SSH host key dropped", id, stored.Host, sv.Host)
		}
	}
	saved, _ := a.st.GetServer(id)
	maskServerSecrets(saved)
	ok(w, saved)
}

func (a *API) handleAdminDeleteServer(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteServer(atoi(chi.URLParam(r, "id"))); err != nil {
		if errors.Is(err, store.ErrInUse) {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		fail(w, http.StatusInternalServerError, "删除服务器失败")
		return
	}
	ok(w, nil)
}

// newRemoteManager builds an SSH manager that verifies host keys and pins them
// on first successful connect (trust-on-first-use), for all remote SSH flows.
func (a *API) newRemoteManager(timeout time.Duration) *sshctl.RemoteManager {
	rm := sshctl.New(sshctl.WithTimeout(timeout))
	rm.SetHostKeyPersister(func(id int64, key string) error { return a.st.SetServerHostKey(id, key) })
	return rm
}

// sshConfigFor builds the SSH dial config for a server row. Shared so every
// remote flow authenticates and pins host keys identically — a second, subtly
// different copy is how one path ends up skipping host-key verification.
func sshConfigFor(sv *store.Server) *sshctl.ServerConfig {
	return &sshctl.ServerConfig{
		ID: sv.ID, Host: sv.Host, Port: sv.Port, SSHUser: sv.SSHUser,
		SSHKey: sv.SSHKey, SSHKeyPass: sv.SSHKeyPass, SSHPassword: sv.SSHPassword,
		SingBoxBin: sv.SingBoxBin, HostKey: sv.HostKey,
	}
}

// POST /api/admin/servers/{id}/test — attempt an SSH connection and report
// success or failure. The SSH key/pass from DB is used (not from request body).
func (a *API) handleAdminTestServer(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	sv, err := a.st.GetServer(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取服务器失败")
		return
	}
	if sv == nil {
		fail(w, http.StatusNotFound, "服务器不存在")
		return
	}

	if sv.SSHKey == "" && sv.SSHPassword == "" {
		_ = a.st.UpdateServerStatus(id, "error")
		fail(w, http.StatusBadRequest, "未配置 SSH 密钥或密码，无法连接")
		return
	}

	// Exercise the exact auth + connection path used for real config
	// deployment, so a passing test guarantees deployment can connect too.
	// This is also the natural place to pin the host key (trust-on-first-use).
	rm := a.newRemoteManager(10 * time.Second)
	cfg := sshConfigFor(sv)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	version, err := rm.TestConnection(ctx, cfg)
	if err != nil {
		_ = a.st.UpdateServerStatus(id, "error")
		fail(w, http.StatusBadGateway, "SSH 连接失败: "+err.Error())
		return
	}
	_ = a.st.UpdateServerStatus(id, "online")
	// TestConnection runs `sing-box version`; record it rather than showing it
	// once and forgetting, so the node version list is fresh after a test too.
	_ = a.st.SetNodeSingbox(sv.ID, sbver.Parse(version))
	ok(w, J{"status": "online", "message": "SSH 连接成功", "version": version})
}

// POST /api/admin/servers/{id}/clear-host-key — drop the pinned SSH host key and
// re-pin whatever the machine presents now.
//
// The pin is what stops an attacker who can answer for this IP from harvesting
// the root SSH credentials we would otherwise hand over. But it also refuses to
// connect when the key legitimately changed — a reinstalled VPS regenerates
// /etc/ssh/ssh_host_*, and reusing a server row for a replacement machine has
// the same effect. Until now that left the panel wedged with no way out of the
// UI: host_key is never sent to the client and editing the server doesn't touch
// it, so recovering meant editing the database by hand.
//
// Reconnecting right away is the point: it re-pins in the same click and returns
// the new fingerprint, so the admin can check it against the machine instead of
// trusting silently. The client side gates this behind a warning — clearing a
// pin that changed for reasons you can't explain is accepting a MITM.
func (a *API) handleAdminClearServerHostKey(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	sv, err := a.st.GetServer(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取服务器失败")
		return
	}
	if sv == nil {
		fail(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if err := a.st.SetServerHostKey(id, ""); err != nil {
		fail(w, http.StatusInternalServerError, "清除失败")
		return
	}
	// Root-level trust decision: leave a trace even when it works, since the
	// panel keeps no other record of a pin being reset.
	log.Printf("admin: cleared pinned SSH host key for server %d (%s)", id, sv.Host)

	if sv.SSHKey == "" && sv.SSHPassword == "" {
		ok(w, J{"message": "已清除固定的主机密钥；该服务器未配置 SSH 密钥或密码，无法立即重新连接"})
		return
	}
	// Dial with the pin cleared so the callback trusts-on-first-use and persists
	// the new key. sv still holds the OLD key in memory — zero it, or we would
	// verify against exactly the key we just removed.
	sv.HostKey = ""
	rm := a.newRemoteManager(10 * time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := rm.TestConnection(ctx, sshConfigFor(sv)); err != nil {
		_ = a.st.UpdateServerStatus(id, "error")
		fail(w, http.StatusBadGateway, "已清除固定的主机密钥，但重新连接失败："+err.Error())
		return
	}
	_ = a.st.UpdateServerStatus(id, "online")
	var fp string
	if cur, _ := a.st.GetServer(id); cur != nil {
		fp = sshctl.Fingerprint(cur.HostKey)
	}
	ok(w, J{"message": "已重新信任这台机器", "fingerprint": fp})
}

// POST /api/admin/servers/{id}/rebuild — rebuild sing-box config for a single
// server (server_id=0 means the local panel server).
func (a *API) handleAdminRebuildServer(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id < 0 {
		fail(w, http.StatusBadRequest, "无效的服务器 ID")
		return
	}
	if a.sbctl == nil {
		fail(w, http.StatusServiceUnavailable, "sing-box 控制器未初始化")
		return
	}
	if err := a.sbctl.RebuildServer(int64(id)); err != nil {
		fail(w, http.StatusBadGateway, "重建失败: "+err.Error())
		return
	}
	ok(w, J{"message": "已重建"})
}
