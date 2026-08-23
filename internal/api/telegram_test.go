package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/store"
	"qingzhou/internal/telegram"
)

type tgMsg struct {
	chat int64
	html string
}

func newTelegramAPI(t *testing.T) (*API, *store.Store, *[]tgMsg) {
	t.Helper()
	a, st := newUserEditAPI(t)
	inbox := &[]tgMsg{}
	a.tgSendFn = func(chatID int64, html string) {
		*inbox = append(*inbox, tgMsg{chat: chatID, html: html})
	}
	if err := st.SetSetting("telegram_bot_token", "TEST:token"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("telegram_bot_username", "qingzhou_bot"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("public_base", "https://panel.example"); err != nil {
		t.Fatal(err)
	}
	return a, st, inbox
}

func asUser(uid int64, r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUserID, uid))
}

func insertPlan(t *testing.T, st *store.Store, userID int64, name string, limit, used, expiry int64) {
	t.Helper()
	now := time.Now().Unix()
	_, err := st.DB().Exec(`INSERT INTO user_plans
		(user_id, kind, package_id, name, client_name, client_uuid, client_secret,
		 traffic_limit, used_up, used_down, expiry_at, status, created_at, updated_at)
		VALUES (?, 'plan', 0, ?, ?, 'u', 's', ?, ?, 0, ?, 'active', ?, ?)`,
		userID, name, "qz_"+name+"_"+itoa(userID)+"_"+itoa(now), limit, used, expiry, now, now)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConfig_ReportsTelegramEnabled(t *testing.T) {
	a, st := newUserEditAPI(t)
	read := func() bool {
		w := httptest.NewRecorder()
		a.handleConfig(w, httptest.NewRequest("GET", "/api/config", nil))
		var resp struct {
			Data struct {
				TelegramEnabled bool `json:"telegram_enabled"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data.TelegramEnabled
	}
	if read() {
		t.Fatal("telegram_enabled is true with no token")
	}
	if err := st.SetSetting("telegram_bot_token", "TEST:token"); err != nil {
		t.Fatal(err)
	}
	if !read() {
		t.Fatal("telegram_enabled still false after token was set")
	}
}

func TestTelegramBindToken_RequiresBot(t *testing.T) {
	a, st := newUserEditAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleTelegramBindToken(w, asUser(uid, httptest.NewRequest("POST", "/api/user/telegram/bind-token", nil)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", w.Code, w.Body.String())
	}
}

func TestTelegramBindToken_ReturnsDeepLink(t *testing.T) {
	a, st, _ := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleTelegramBindToken(w, asUser(uid, httptest.NewRequest("POST", "/api/user/telegram/bind-token", nil)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			URL string `json:"url"`
			Bot string `json:"bot"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Bot != "qingzhou_bot" || !strings.HasPrefix(resp.Data.URL, "https://t.me/qingzhou_bot?start=") {
		t.Fatalf("link = %+v", resp.Data)
	}
}

func TestTelegramStartBindsAndSub(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "alice", PasswordHash: "x", SubToken: "SUBTOK"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTelegramBindToken(uid, "bindtok", time.Hour); err != nil {
		t.Fatal(err)
	}

	a.handleTelegramUpdate(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		From: &telegram.User{ID: 9001, Username: "alice_tg", FirstName: "Al"},
		Chat: telegram.Chat{ID: 9001, Type: "private"},
		Text: "/start bindtok",
	}})
	b, err := st.TelegramBindByUser(uid)
	if err != nil || b == nil || b.TelegramID != 9001 {
		t.Fatalf("bind = %+v err=%v", b, err)
	}
	if len(*inbox) == 0 || !strings.Contains((*inbox)[0].html, "alice") {
		t.Fatalf("welcome = %#v", *inbox)
	}

	*inbox = nil
	a.handleTelegramUpdate(telegram.Update{UpdateID: 2, Message: &telegram.Message{
		From: &telegram.User{ID: 9001},
		Chat: telegram.Chat{ID: 9001, Type: "private"},
		Text: "/sub",
	}})
	if len(*inbox) != 1 {
		t.Fatalf("sub replies = %#v", *inbox)
	}
	if !strings.Contains((*inbox)[0].html, "https://panel.example/sub/SUBTOK") {
		t.Fatalf("sub message missing url: %s", (*inbox)[0].html)
	}
	if !strings.Contains((*inbox)[0].html, "?format=clash") {
		t.Fatal("sub message missing clash format")
	}
}

func TestTelegramCommandsRequireBind(t *testing.T) {
	a, _, inbox := newTelegramAPI(t)
	a.handleTelegramUpdate(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		From: &telegram.User{ID: 1},
		Chat: telegram.Chat{ID: 1, Type: "private"},
		Text: "/traffic",
	}})
	if len(*inbox) != 1 || !strings.Contains((*inbox)[0].html, "尚未绑定") {
		t.Fatalf("unbound traffic = %#v", *inbox)
	}
}

func TestTelegramBannedUserIsRefused(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "banned", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdminUpdateUser(uid, "banned", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 7, 7, "x", "x"); err != nil {
		t.Fatal(err)
	}
	a.handleTelegramUpdate(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		From: &telegram.User{ID: 7},
		Chat: telegram.Chat{ID: 7, Type: "private"},
		Text: "/sub",
	}})
	if len(*inbox) != 1 || !strings.Contains((*inbox)[0].html, "禁用") {
		t.Fatalf("banned /sub = %#v", *inbox)
	}
	if strings.Contains((*inbox)[0].html, "/sub/T") {
		t.Fatal("banned user was handed the subscription URL")
	}
}

func TestTelegramIgnoresGroupChats(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 8, 8, "", ""); err != nil {
		t.Fatal(err)
	}
	a.handleTelegramUpdate(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		From: &telegram.User{ID: 8},
		Chat: telegram.Chat{ID: -100, Type: "group"},
		Text: "/sub",
	}})
	if len(*inbox) != 0 {
		t.Fatalf("group chat was answered: %#v", *inbox)
	}
}

func TestTelegramPlanNameIsHTMLEscaped(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 9, 9, "", ""); err != nil {
		t.Fatal(err)
	}
	insertPlan(t, st, uid, `<script>alert(1)</script>`, 10<<30, 0, time.Now().Unix()+86400)
	a.handleTelegramUpdate(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		From: &telegram.User{ID: 9},
		Chat: telegram.Chat{ID: 9, Type: "private"},
		Text: "/plan",
	}})
	if len(*inbox) != 1 {
		t.Fatalf("replies = %#v", *inbox)
	}
	if strings.Contains((*inbox)[0].html, "<script>") {
		t.Fatalf("plan name not escaped: %s", (*inbox)[0].html)
	}
	if !strings.Contains((*inbox)[0].html, "&lt;script&gt;") {
		t.Fatalf("expected escaped name: %s", (*inbox)[0].html)
	}
}

func TestTelegramStartTakenChat(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	alice, err := st.CreateUser(store.NewUser{Username: "alice", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(store.NewUser{Username: "bob", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(alice, 11, 11, "a", "A"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTelegramBindToken(bob, "bobtok", time.Hour); err != nil {
		t.Fatal(err)
	}
	a.handleTelegramUpdate(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		From: &telegram.User{ID: 11},
		Chat: telegram.Chat{ID: 11, Type: "private"},
		Text: "/start bobtok",
	}})
	got, _ := st.TelegramBindByTelegramID(11)
	if got == nil || got.UserID != alice {
		t.Fatalf("owner moved: %+v", got)
	}
	if len(*inbox) == 0 || !strings.Contains((*inbox)[0].html, "已绑定其他账号") {
		t.Fatalf("taken chat reply = %#v", *inbox)
	}
}

func TestNotifyExpirySoonOnce(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 12, 12, "", ""); err != nil {
		t.Fatal(err)
	}
	insertPlan(t, st, uid, "月付", 10<<30, 1<<30, time.Now().Unix()+2*86400)
	a.sweepTelegramNotifies()
	a.sweepTelegramNotifies()
	n := 0
	for _, m := range *inbox {
		if strings.Contains(m.html, "即将到期") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expiry_soon sent %d times, want 1: %#v", n, *inbox)
	}
}

func TestNotifyTrafficLowRecovers(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	if err := st.SetSetting("notify_traffic_percent", "20"); err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 13, 13, "", ""); err != nil {
		t.Fatal(err)
	}
	insertPlan(t, st, uid, "计量", 100<<30, 90<<30, time.Now().Unix()+30*86400)
	a.sweepTelegramNotifies()
	a.sweepTelegramNotifies()
	lows := 0
	for _, m := range *inbox {
		if strings.Contains(m.html, "流量不足") {
			lows++
		}
	}
	if lows != 1 {
		t.Fatalf("traffic_low sent %d times: %#v", lows, *inbox)
	}

	// Top up: remaining climbs back above the threshold, then drops again.
	if _, err := st.DB().Exec(`UPDATE user_plans SET used_up=? WHERE user_id=?`, 10<<30, uid); err != nil {
		t.Fatal(err)
	}
	a.sweepTelegramNotifies()
	if _, err := st.DB().Exec(`UPDATE user_plans SET used_up=? WHERE user_id=?`, 95<<30, uid); err != nil {
		t.Fatal(err)
	}
	a.sweepTelegramNotifies()
	lows = 0
	for _, m := range *inbox {
		if strings.Contains(m.html, "流量不足") {
			lows++
		}
	}
	if lows != 2 {
		t.Fatalf("after recover+redrop, traffic_low sent %d times, want 2: %#v", lows, *inbox)
	}
}

func TestNotifySkipsUnlimitedAndBanned(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 14, 14, "", ""); err != nil {
		t.Fatal(err)
	}
	// Uncapped: must not fire traffic_low even at "0 remaining".
	insertPlan(t, st, uid, "不限", 0, 50<<30, time.Now().Unix()+30*86400)
	a.sweepTelegramNotifies()
	for _, m := range *inbox {
		if strings.Contains(m.html, "流量") {
			t.Fatalf("unlimited account got a traffic notice: %s", m.html)
		}
	}

	uid2, err := st.CreateUser(store.NewUser{Username: "u2", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdminUpdateUser(uid2, "banned", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid2, 15, 15, "", ""); err != nil {
		t.Fatal(err)
	}
	insertPlan(t, st, uid2, "月付", 10<<30, 1, time.Now().Unix()+3600)
	*inbox = nil
	a.sweepTelegramNotifies()
	if len(*inbox) != 0 {
		t.Fatalf("banned user was notified: %#v", *inbox)
	}
}

func TestNotifyPrefsCanSilence(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 16, 16, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTelegramNotify(uid, false, false); err != nil {
		t.Fatal(err)
	}
	insertPlan(t, st, uid, "月付", 10<<30, 9<<30, time.Now().Unix()+3600)
	a.sweepTelegramNotifies()
	if len(*inbox) != 0 {
		t.Fatalf("silenced user was notified: %#v", *inbox)
	}
}

func TestSplitTelegramCommand(t *testing.T) {
	cmd, arg := splitTelegramCommand("/start@qingzhou_bot abc")
	if cmd != "/start" || arg != "abc" {
		t.Fatalf("got %q %q", cmd, arg)
	}
	cmd, arg = splitTelegramCommand("订阅")
	if cmd != "订阅" || arg != "" {
		t.Fatalf("got %q %q", cmd, arg)
	}
}

func TestTelegramUnbindFromChat(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, 17, 17, "", ""); err != nil {
		t.Fatal(err)
	}
	a.handleTelegramUpdate(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		From: &telegram.User{ID: 17},
		Chat: telegram.Chat{ID: 17, Type: "private"},
		Text: "/unbind",
	}})
	if got, _ := st.TelegramBindByUser(uid); got != nil {
		t.Fatal("bind survived /unbind")
	}
	if len(*inbox) == 0 || !strings.Contains((*inbox)[0].html, "已解绑") {
		t.Fatalf("unbind reply = %#v", *inbox)
	}
}
