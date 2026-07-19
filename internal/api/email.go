package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"qingzhou/internal/auth"
	"qingzhou/internal/idgen"
	"qingzhou/internal/mailer"
)

// currentMailer builds a mailer from the latest settings (env overrides win),
// so SMTP config changes take effect without restarting. Returns nil if no host.
func (a *API) currentMailer() *mailer.Mailer {
	get := func(env, key string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		v, _ := a.st.GetSetting(key)
		return v
	}
	host := get("QZ_SMTP_HOST", "smtp_host")
	if host == "" {
		return nil
	}
	first := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	return &mailer.Mailer{
		Host:     host,
		Port:     first(get("QZ_SMTP_PORT", "smtp_port"), "587"),
		User:     get("QZ_SMTP_USER", "smtp_user"),
		Pass:     get("QZ_SMTP_PASS", "smtp_pass"),
		From:     first(get("QZ_SMTP_FROM", "smtp_from"), get("QZ_SMTP_USER", "smtp_user")),
		FromName: first(get("QZ_SMTP_FROM_NAME", "smtp_from_name"), "轻舟"),
		Security: get("QZ_SMTP_SECURITY", "smtp_security"),
	}
}

// deliver sends an email, or (when SMTP is unconfigured) logs the link so the
// flow still works in development. Never blocks the request on a send failure.
func (a *API) deliver(to, subject, html, devLink string) {
	if m := a.currentMailer(); m != nil {
		if err := m.Send([]string{to}, subject, html); err != nil {
			log.Printf("email send to %s failed: %v", to, err)
		}
		return
	}
	log.Printf("[email:dev] SMTP not configured; would send %q to %s; link: %s", subject, to, devLink)
}

// GET /api/auth/verify?token=...  (clicked from an email, renders HTML)
func (a *API) handleVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	userID, ok, err := a.st.UseEmailToken(token, "verify")
	if err != nil {
		writeHTMLPage(w, http.StatusInternalServerError, "服务器错误", "请稍后重试。")
		return
	}
	if !ok {
		// Token couldn't be consumed (already used / pre-fetched by a mail
		// scanner / clicked twice). If its owner is already verified, this is a
		// success from the user's point of view — say so instead of "invalid".
		if found, verified := a.st.TokenUserVerified(token); found && verified {
			writeHTMLPage(w, http.StatusOK, "邮箱已验证", "你的邮箱已经验证过了，账号已激活，现在可以返回登录。")
			return
		}
		writeHTMLPage(w, http.StatusBadRequest, "链接无效", "验证链接无效或已过期，请登录后在「个人中心」重新发送验证邮件。")
		return
	}
	if err := a.st.SetEmailVerified(userID); err != nil {
		writeHTMLPage(w, http.StatusInternalServerError, "服务器错误", "请稍后重试。")
		return
	}
	u, _ := a.st.UserByID(userID)
	if u != nil && u.Role != "admin" && !u.ClientID.Valid {
		if err := a.provisionClient(u); err != nil {
			log.Printf("verify: provision failed for user %d: %v", userID, err)
			writeHTMLPage(w, http.StatusBadGateway, "激活未完成", "邮箱已验证，但开通节点失败，请稍后重试或联系管理员。")
			return
		}
	}
	writeHTMLPage(w, http.StatusOK, "验证成功", "邮箱验证成功，账号已激活，现在可以返回登录了。")
}

// POST /api/auth/forgot {email}
func (a *API) handleForgot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if u, _ := a.st.UserByEmail(email); u != nil && u.Email.Valid {
		token, _ := idgen.RandToken(24)
		if err := a.st.CreateEmailToken(u.ID, token, "reset", time.Hour); err == nil {
			link := a.publicBase(r) + "/reset?token=" + token
			a.deliver(email, "重置密码 - 轻舟", resetEmailHTML(link), link)
		}
	}
	// Always succeed to avoid leaking which emails are registered.
	ok(w, J{"message": "若该邮箱已注册，我们已发送密码重置邮件"})
}

// POST /api/auth/reset {token, new_password}
func (a *API) handleReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.NewPassword) < 6 {
		fail(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}
	userID, ok2, err := a.st.UseEmailToken(req.Token, "reset")
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if !ok2 {
		fail(w, http.StatusBadRequest, "重置链接无效或已过期")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if err := a.st.UpdatePassword(userID, hash); err != nil {
		fail(w, http.StatusInternalServerError, "重置失败")
		return
	}
	// Revoke every existing session. Forgot-password is the account-recovery path
	// used precisely when an account is compromised; since JWTs stay valid for days
	// and auth only checks the session row exists (not password freshness), a stolen
	// session would otherwise survive the reset. Mirrors change-password / admin-reset.
	_ = a.st.DeleteUserSessions(userID)
	ok(w, J{"message": "密码已重置，请用新密码登录"})
}

// validEmail does a light-weight sanity check (real validation = the link works).
//
// CR and LF are rejected outright, not as formatting hygiene: the address is
// interpolated raw into the "To:" header, so an address like
// "a@b\r\nBcc: attacker@example.com" is header injection. Today net/smtp's own
// Rcpt validation rejects those bytes before the message is transmitted, so this
// is defence in depth — but that safety net depends on stdlib internals and on
// deliver() happening to call Rcpt before Data. It also stops such an address
// from being stored and then breaking every future mail to that user.
func validEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	if at <= 0 || at >= len(e)-3 {
		return false
	}
	if strings.ContainsAny(e, "\r\n\x00") {
		return false
	}
	dot := strings.LastIndexByte(e, '.')
	return dot > at+1 && dot < len(e)-1 && !strings.ContainsAny(e, " \t")
}

// POST /api/user/email {email} — bind or change email, then send a verify link.
func (a *API) handleBindEmail(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if !validEmail(email) {
		fail(w, http.StatusBadRequest, "邮箱格式不正确")
		return
	}
	if u.Email.Valid && u.Email.String == email && u.EmailVerified {
		ok(w, J{"message": "邮箱无变化"})
		return
	}
	if other, _ := a.st.UserByEmail(email); other != nil && other.ID != u.ID {
		fail(w, http.StatusConflict, "该邮箱已被其他账号绑定")
		return
	}
	if a.resendRL != nil && !a.resendRL.allow(fmt.Sprintf("e%d", u.ID)) {
		fail(w, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
		return
	}
	if err := a.st.SetUserEmail(u.ID, email); err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	token, err := idgen.RandToken(24)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if err := a.st.CreateEmailToken(u.ID, token, "verify", 24*time.Hour); err != nil {
		fail(w, http.StatusInternalServerError, "创建验证令牌失败")
		return
	}
	link := a.publicBase(r) + "/api/auth/verify?token=" + token
	a.deliver(email, "验证你的邮箱 - 轻舟", verifyEmailHTML(link), link)
	ok(w, J{"message": "验证邮件已发送到该邮箱，请点击其中链接完成绑定"})
}

// POST /api/user/resend-verify — re-send the email verification link.
func (a *API) handleResendVerify(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	if u.EmailVerified {
		ok(w, J{"message": "邮箱已验证，无需重发"})
		return
	}
	email := ""
	if u.Email.Valid {
		email = u.Email.String
	}
	if email == "" {
		fail(w, http.StatusBadRequest, "账号未绑定邮箱，无法发送验证邮件")
		return
	}
	if a.resendRL != nil && !a.resendRL.allow(fmt.Sprintf("u%d", u.ID)) {
		fail(w, http.StatusTooManyRequests, "发送过于频繁，请稍后再试")
		return
	}
	token, err := idgen.RandToken(24)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if err := a.st.CreateEmailToken(u.ID, token, "verify", 24*time.Hour); err != nil {
		fail(w, http.StatusInternalServerError, "创建验证令牌失败")
		return
	}
	link := a.publicBase(r) + "/api/auth/verify?token=" + token
	a.deliver(email, "验证你的邮箱 - 轻舟", verifyEmailHTML(link), link)
	ok(w, J{"message": "验证邮件已发送，请查收（含垃圾箱）"})
}

// POST /api/admin/settings/test-smtp {to}
func (a *API) handleTestSMTP(w http.ResponseWriter, r *http.Request) {
	m := a.currentMailer()
	if m == nil {
		fail(w, http.StatusBadRequest, "SMTP 未配置（请先填写服务器地址）")
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		fail(w, http.StatusBadRequest, "请填写收件邮箱")
		return
	}
	if err := m.Send([]string{req.To}, "测试邮件 - 轻舟",
		"<p>这是一封来自<strong>轻舟面板</strong>的测试邮件。收到它说明你的 SMTP 配置正确。</p>"); err != nil {
		fail(w, http.StatusBadGateway, "发送失败: "+err.Error())
		return
	}
	ok(w, J{"message": "测试邮件已发送，请查收"})
}

// ---- helpers ----

func writeHTMLPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s - 轻舟</title>
<style>body{font-family:system-ui,-apple-system,"Microsoft YaHei",sans-serif;background:#f5f6f8;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{background:#fff;padding:32px 28px;border-radius:14px;box-shadow:0 6px 24px rgba(0,0,0,.08);max-width:360px;text-align:center}
h1{font-size:20px;margin:0 0 12px;color:#1f2937}p{color:#4b5563;line-height:1.6;margin:0}</style></head>
<body><div class="card"><h1>%s</h1><p>%s</p></div></body></html>`, title, title, body)
}

func verifyEmailHTML(link string) string {
	return fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:480px;margin:0 auto">
<h2>欢迎注册轻舟</h2>
<p>请点击下面的按钮验证你的邮箱并激活账号（24 小时内有效）：</p>
<p><a href="%s" style="display:inline-block;background:#2563eb;color:#fff;padding:10px 22px;border-radius:8px;text-decoration:none">验证邮箱</a></p>
<p style="color:#6b7280;font-size:13px">如果按钮无法点击，请复制此链接到浏览器：<br>%s</p></div>`, link, link)
}

func resetEmailHTML(link string) string {
	return fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:480px;margin:0 auto">
<h2>重置你的密码</h2>
<p>我们收到了重置密码的请求。点击下面的链接设置新密码（1 小时内有效）：</p>
<p><a href="%s" style="display:inline-block;background:#2563eb;color:#fff;padding:10px 22px;border-radius:8px;text-decoration:none">重置密码</a></p>
<p style="color:#6b7280;font-size:13px">若不是你本人操作，请忽略此邮件。链接：<br>%s</p></div>`, link, link)
}
