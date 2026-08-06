package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"qingzhou/internal/auth"
)

// POST /api/user/password {old_password, new_password} — change own password.
func (a *API) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(ctxUserID).(int64)
	jti, _ := r.Context().Value(ctxJti).(string)
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.NewPassword) < 6 {
		fail(w, http.StatusBadRequest, "新密码至少 6 位")
		return
	}
	u, err := a.st.UserByID(uid)
	if err != nil || u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		fail(w, http.StatusBadRequest, "原密码错误")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if err := a.st.UpdatePassword(uid, hash); err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// Log out other devices for safety; keep the current session.
	_ = a.st.DeleteUserSessionsExcept(uid, jti)
	ok(w, J{"message": "密码已修改，其他设备已退出登录"})
}

// StartMaintenance periodically prunes stale sessions / tokens and sweeps the
// rate limiter.
func (a *API) StartMaintenance(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		a.maintain()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.maintain()
			}
		}
	}()
}

func (a *API) maintain() {
	cutoff := time.Now().Add(-tokenTTL).Unix()
	if n, err := a.st.CleanupSessions(cutoff); err == nil && n > 0 {
		log.Printf("maintenance: pruned %d stale session(s)", n)
	}
	a.st.CleanupEmailTokens()
	// Sweep every limiter — otherwise resendRL/probeRL entries accumulate for the
	// process lifetime (unbounded memory; the probe endpoint is IP-keyed).
	for _, rl := range []*rateLimiter{a.authRL, a.resendRL, a.probeRL, a.subRL} {
		if rl != nil {
			rl.sweep()
		}
	}
}
