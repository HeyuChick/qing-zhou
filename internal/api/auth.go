package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"qingzhou/internal/auth"
	"qingzhou/internal/idgen"
	"qingzhou/internal/store"
)

type ctxKey string

const (
	ctxUserID ctxKey = "uid"
	ctxRole   ctxKey = "role"
	ctxJti    ctxKey = "jti"

	cookieName = "qz_token"
	tokenTTL   = 7 * 24 * time.Hour
)

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// issueLogin records a login session and returns a JWT bound to it (so it can
// be listed and revoked). Also sets the auth cookie.
func (a *API) issueLogin(w http.ResponseWriter, r *http.Request, u *store.User) (string, error) {
	jti, err := idgen.RandToken(12)
	if err != nil {
		return "", err
	}
	if err := a.st.CreateSession(u.ID, jti, clientIP(r), r.UserAgent()); err != nil {
		return "", err
	}
	tok, err := auth.Issue(a.secret, u.ID, u.Role, jti, tokenTTL)
	if err != nil {
		return "", err
	}
	setAuthCookie(w, tok)
	return tok, nil
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	ok(w, J{"status": "ok", "time": time.Now().Unix()})
}

// registerMode is "open" (anyone) | "code" (needs reg code) | "closed".
// Falls back to the legacy registration_open boolean when unset.
func (a *API) registerMode() string {
	switch m, _ := a.st.GetSetting("register_mode"); m {
	case "open", "code", "closed":
		return m
	}
	if open, _ := a.st.GetSettingBool("registration_open"); open {
		return "open"
	}
	return "closed"
}

// handleConfig exposes public site config the frontend needs before login.
func (a *API) handleConfig(w http.ResponseWriter, r *http.Request) {
	verify, _ := a.st.GetSettingBool("email_verify_required")
	rate, _ := a.st.GetSettingInt64("points_per_cny", 10)
	mode := a.registerMode()
	ok(w, J{
		"register_mode":         mode,
		"registration_open":     mode != "closed",
		"email_verify_required": verify,
		"points_per_cny":        rate,
		"site_name":             "轻舟",
	})
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	u, err := a.st.UserByUsername(strings.TrimSpace(req.Username))
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if u == nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		fail(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if u.Status == "banned" {
		fail(w, http.StatusForbidden, "账号已被封禁")
		return
	}

	tok, err := a.issueLogin(w, r, u)
	if err != nil {
		fail(w, http.StatusInternalServerError, "签发令牌失败")
		return
	}
	ok(w, J{"token": tok, "user": userView(u)})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(ctxUserID).(int64)
	u, err := a.st.UserByID(uid)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if u == nil {
		fail(w, http.StatusNotFound, "用户不存在")
		return
	}
	ok(w, userView(u))
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if jti, _ := r.Context().Value(ctxJti).(string); jti != "" {
		_ = a.st.DeleteSessionByJti(jti)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	ok(w, nil)
}

func (a *API) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokStr := bearerOrCookie(r)
		if tokStr == "" {
			fail(w, http.StatusUnauthorized, "未登录")
			return
		}
		claims, err := auth.Parse(a.secret, tokStr)
		if err != nil {
			fail(w, http.StatusUnauthorized, "登录已过期，请重新登录")
			return
		}
		// Session must still exist (supports remote logout / revocation).
		if claims.ID == "" || !a.st.SessionValid(claims.ID) {
			fail(w, http.StatusUnauthorized, "登录已失效，请重新登录")
			return
		}
		a.st.TouchSession(claims.ID)
		ctx := context.WithValue(r.Context(), ctxUserID, claims.UserID)
		ctx = context.WithValue(ctx, ctxRole, claims.Role)
		ctx = context.WithValue(ctx, ctxJti, claims.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *API) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(ctxRole).(string)
		if role != "admin" {
			fail(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerOrCookie(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}

func userView(u *store.User) J {
	email := ""
	if u.Email.Valid {
		email = u.Email.String
	}
	return J{
		"id":             u.ID,
		"username":       u.Username,
		"email":          email,
		"email_verified": u.EmailVerified,
		"role":           u.Role,
		"status":         u.Status,
		"points":         u.Points,
	}
}
