package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

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
	cfg := &sshctl.ServerConfig{
		ID: sv.ID, Host: sv.Host, Port: sv.Port, SSHUser: sv.SSHUser,
		SSHKey: sv.SSHKey, SSHKeyPass: sv.SSHKeyPass, SSHPassword: sv.SSHPassword,
		SingBoxBin: sv.SingBoxBin, HostKey: sv.HostKey,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	version, err := rm.TestConnection(ctx, cfg)
	if err != nil {
		_ = a.st.UpdateServerStatus(id, "error")
		fail(w, http.StatusBadGateway, "SSH 连接失败: "+err.Error())
		return
	}
	_ = a.st.UpdateServerStatus(id, "online")
	ok(w, J{"status": "online", "message": "SSH 连接成功", "version": version})
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
