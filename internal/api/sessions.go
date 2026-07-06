package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// GET /api/user/sessions — the user's currently online devices: only sessions
// whose login token hasn't expired, one per device (deduped by ip+user_agent).
// Dead (token-expired) rows are purged so the count reflects who's online now.
func (a *API) handleUserSessions(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(ctxUserID).(int64)
	jti, _ := r.Context().Value(ctxJti).(string)
	minCreated := time.Now().Unix() - int64(tokenTTL/time.Second)
	_, _ = a.st.PurgeExpiredSessions(minCreated)
	list, err := a.st.ListActiveSessions(uid, minCreated, jti)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取登录设备失败")
		return
	}
	for _, s := range list {
		s.Current = s.Jti == jti
	}
	ok(w, list)
}

// POST /api/user/sessions/{id}/revoke — log out a specific device.
func (a *API) handleUserRevokeSession(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(ctxUserID).(int64)
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的会话 id")
		return
	}
	if err := a.st.DeleteUserSession(uid, id); err != nil {
		fail(w, http.StatusInternalServerError, "注销失败")
		return
	}
	ok(w, nil)
}
