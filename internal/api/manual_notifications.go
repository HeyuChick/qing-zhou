package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/telegram"
)

// GET /api/admin/manual-notifications/users?q= returns active, non-admin users
// eligible for a manual broadcast and whether Telegram can reach each one.
func (a *API) handleAdminManualNotificationUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.st.ListUsers(strings.TrimSpace(r.URL.Query().Get("q")), 1000)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用户失败")
		return
	}
	binds, err := a.st.ListTelegramBinds()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取 Telegram 绑定失败")
		return
	}
	bound := map[int64]bool{}
	for _, bind := range binds {
		if bind != nil && bind.ChatID != 0 {
			bound[bind.UserID] = true
		}
	}
	out := []J{}
	for _, user := range users {
		if user.Role == "admin" || user.Status != "active" {
			continue
		}
		out = append(out, J{
			"id": user.ID, "username": user.Username,
			"email": user.Email.String, "telegram_bound": bound[user.ID],
		})
	}
	ok(w, out)
}

// POST /api/admin/manual-notifications
func (a *API) handleAdminCreateManualNotification(w http.ResponseWriter, r *http.Request) {
	if !a.telegramConfigured() {
		fail(w, http.StatusServiceUnavailable, "尚未配置 Telegram Bot")
		return
	}
	var req struct {
		Title      string  `json:"title"`
		Content    string  `json:"content"`
		TargetType string  `json:"target_type"`
		UserIDs    []int64 `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Content = strings.TrimSpace(req.Content)
	if req.Title == "" {
		fail(w, http.StatusBadRequest, "标题不能为空")
		return
	}
	// Telegram messages are limited to 4096 characters. Leave space for the
	// emoji, HTML title wrapper, separators, and escaped entities.
	if len([]rune(req.Title)) > 100 || len([]rune(req.Content)) > 3000 {
		fail(w, http.StatusBadRequest, "标题最多 100 字，内容最多 3000 字")
		return
	}
	uid, _ := r.Context().Value(ctxUserID).(int64)
	notification, err := a.st.CreateManualNotification(req.Title, req.Content, req.TargetType, req.UserIDs, uid)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	go a.deliverManualNotification(notification.ID)
	ok(w, notification)
}

func (a *API) deliverManualNotification(notificationID int64) {
	notification, err := a.st.ManualNotificationByID(notificationID)
	if err != nil || notification == nil {
		log.Printf("manual telegram notification %d: load: %v", notificationID, err)
		return
	}
	message := renderManualTelegramNotification(notification.Title, notification.Content)
	for {
		recipient, err := a.st.ClaimManualNotificationRecipient(notificationID)
		if err != nil {
			log.Printf("manual telegram notification %d: claim: %v", notificationID, err)
			return
		}
		if recipient == nil {
			return
		}

		status, reason := "sent", ""
		// Re-check the live binding immediately before delivery. The stored chat is
		// an audit snapshot, not authority after a user unbinds or changes account.
		bind, err := a.st.TelegramBindByUser(recipient.UserID)
		if err != nil {
			status, reason = "failed", truncateManualNotificationError(err.Error())
		} else if bind == nil {
			status, reason = "skipped", "发送前已解绑 Telegram"
		} else if bind.ChatID != recipient.ChatID {
			status, reason = "skipped", "Telegram 绑定已变更"
		} else if err := a.tgSend(recipient.ChatID, message); err != nil {
			status, reason = "failed", truncateManualNotificationError(err.Error())
		}
		if err := a.st.SetManualNotificationRecipientResult(notificationID, recipient.UserID, status, reason); err != nil {
			log.Printf("manual telegram notification %d: persist user %d: %v", notificationID, recipient.UserID, err)
		}
	}
}

// resumeManualNotifications runs at process startup. Pending rows are safe to
// resume because they were never claimed; interrupted sending rows are marked
// unknown instead of retried to avoid a possible duplicate Telegram message.
func (a *API) resumeManualNotifications() {
	if err := a.st.FailInterruptedManualNotifications(); err != nil {
		log.Printf("manual telegram notifications: recover interrupted: %v", err)
		return
	}
	ids, err := a.st.ListPendingManualNotificationIDs()
	if err != nil {
		log.Printf("manual telegram notifications: list pending: %v", err)
		return
	}
	for _, id := range ids {
		a.deliverManualNotification(id)
	}
}

func renderManualTelegramNotification(title, content string) string {
	var b strings.Builder
	b.WriteString("🔔 <b>")
	b.WriteString(telegram.Escape(strings.TrimSpace(title)))
	b.WriteString("</b>")
	if content = strings.TrimSpace(content); content != "" {
		b.WriteString("\n\n")
		b.WriteString(telegram.Escape(content))
	}
	return b.String()
}

func truncateManualNotificationError(s string) string {
	const max = 300
	r := []rune(strings.TrimSpace(s))
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}

// GET /api/admin/manual-notifications
func (a *API) handleAdminListManualNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListManualNotifications(100)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取发送历史失败")
		return
	}
	ok(w, list)
}

// GET /api/admin/manual-notifications/{id}
func (a *API) handleAdminManualNotificationDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的通知 ID")
		return
	}
	notification, err := a.st.ManualNotificationByID(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取发送历史失败")
		return
	}
	if notification == nil {
		fail(w, http.StatusNotFound, "通知不存在")
		return
	}
	recipients, err := a.st.ListManualNotificationRecipients(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取发送明细失败")
		return
	}
	ok(w, J{"notification": notification, "recipients": recipients})
}
