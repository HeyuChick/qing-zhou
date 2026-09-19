package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/backup"
)

func (a *API) handleAdminGetBackupConfig(w http.ResponseWriter, r *http.Request) {
	cfg, configured, err := a.remoteBackup.LoadConfig()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取远端备份配置失败")
		return
	}
	ok(w, J{"config": cfg, "configured": configured, "recovery": a.remoteBackup.RecoveryInfo()})
}

func (a *API) handleAdminPutBackupConfig(w http.ResponseWriter, r *http.Request) {
	var cfg backup.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	saved, err := a.remoteBackup.SaveConfig(cfg)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, saved)
}

func (a *API) handleAdminTestBackupConfig(w http.ResponseWriter, r *http.Request) {
	var cfg backup.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.remoteBackup.TestConnection(r.Context(), cfg); err != nil {
		ok(w, J{"ok": false, "message": err.Error()})
		return
	}
	ok(w, J{"ok": true, "message": "连接成功"})
}

func (a *API) handleAdminGetBackupSchedule(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.remoteBackup.LoadSchedule()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取备份计划失败")
		return
	}
	ok(w, cfg)
}

func (a *API) handleAdminPutBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var cfg backup.Schedule
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	saved, err := a.remoteBackup.SaveSchedule(r.Context(), cfg)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, saved)
}

func (a *API) handleAdminCreateRemoteBackup(w http.ResponseWriter, r *http.Request) {
	record, err := a.remoteBackup.StartBackup(r.Context(), "manual")
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, backup.ErrInProgress) {
			status = http.StatusConflict
		}
		fail(w, status, err.Error())
		return
	}
	ok(w, record)
}

func (a *API) handleAdminListRemoteBackups(w http.ResponseWriter, r *http.Request) {
	records, err := a.remoteBackup.List()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取备份记录失败")
		return
	}
	ok(w, J{"items": records})
}

func (a *API) handleAdminRemoteBackupDownload(w http.ResponseWriter, r *http.Request) {
	link, err := a.remoteBackup.DownloadURL(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, J{"url": link})
}

func (a *API) handleAdminDeleteRemoteBackup(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteBackup.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, nil)
}
