package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// bindOps creates a user, binds a Telegram chat to it and optionally flags it
// as an ops-alert recipient.
func bindOps(t *testing.T, st *store.Store, name, role string, chatID int64, ops bool) int64 {
	t.Helper()
	uid, err := st.CreateUser(store.NewUser{Username: name, Role: role, PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTelegram(uid, chatID, chatID, name+"_tg", name); err != nil {
		t.Fatal(err)
	}
	if ops {
		bound, err := st.SetTelegramNotifyOps(uid, true)
		if err != nil || !bound {
			t.Fatalf("flag %s: bound=%v err=%v", name, bound, err)
		}
	}
	return uid
}

// TestOpsAlertReachesNonAdminRecipients is the requirement in one test: the
// person who should hear that a node is flapping is whoever runs the servers,
// which need not be someone holding a panel admin account. A recipient is
// chosen by an admin, not by role.
func TestOpsAlertReachesNonAdminRecipients(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)

	bindOps(t, st, "ops_person", "user", 1001, true) // not an admin — must receive
	bindOps(t, st, "boss", "admin", 1002, true)      // admin — must receive
	bindOps(t, st, "customer", "user", 1003, false)  // bound but not picked — must not
	if err := st.SetSetting("alert_ops_extra_chats", "-1005550001"); err != nil {
		t.Fatal(err)
	}

	a.deliverOpsMessage("<b>test</b>")

	got := map[int64]bool{}
	for _, m := range *inbox {
		got[m.chat] = true
	}
	for _, want := range []int64{1001, 1002, -1005550001} {
		if !got[want] {
			t.Fatalf("chat %d did not receive the alert; inbox=%v", want, *inbox)
		}
	}
	if got[1003] {
		t.Fatal("an account that was never picked received an ops alert")
	}
}

// TestOpsRecipientsDropWhenUnreachable covers the two ways a configured
// recipient silently stops existing. Both must remove them from the list rather
// than fail the send, and the settings page's "effective" count is what tells
// the admin their list has emptied out.
func TestOpsRecipientsDropWhenUnreachable(t *testing.T) {
	a, st, _ := newTelegramAPI(t)
	unbinder := bindOps(t, st, "leaver", "user", 2001, true)
	banned := bindOps(t, st, "banned_one", "user", 2002, true)

	if chats, err := a.opsChatIDs(); err != nil || len(chats) != 2 {
		t.Fatalf("expected 2 recipients, got %v (%v)", chats, err)
	}

	if err := st.UnbindTelegram(unbinder); err != nil {
		t.Fatal(err)
	}
	if err := st.AdminUpdateUser(banned, "banned", false, nil); err != nil {
		t.Fatal(err)
	}

	chats, err := a.opsChatIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 0 {
		t.Fatalf("unbound / banned accounts still on the recipient list: %v", chats)
	}
}

// TestOpsRecipientsDeduplicate: the same chat reachable both as a bound account
// and as a hand-entered extra chat is one recipient, not two messages.
func TestOpsRecipientsDeduplicate(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	bindOps(t, st, "ops_person", "user", 3001, true)
	if err := st.SetSetting("alert_ops_extra_chats", "3001, 3002"); err != nil {
		t.Fatal(err)
	}

	a.deliverOpsMessage("x")

	seen := map[int64]int{}
	for _, m := range *inbox {
		seen[m.chat]++
	}
	if seen[3001] != 1 {
		t.Fatalf("chat 3001 received %d copies, want 1", seen[3001])
	}
	if seen[3002] != 1 {
		t.Fatalf("extra chat 3002 received %d copies, want 1", seen[3002])
	}
}

// TestSetOpsRecipientRequiresABinding: flagging an account that never bound
// Telegram has to fail loudly. Silently accepting it is how an admin ends up
// with a recipient list that looks configured and reaches nobody.
func TestSetOpsRecipientRequiresABinding(t *testing.T) {
	_, st := newUserEditAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "nobind", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := st.SetTelegramNotifyOps(uid, true)
	if err != nil {
		t.Fatal(err)
	}
	if bound {
		t.Fatal("flagged an account with no Telegram binding")
	}
}

// TestRestartAlertMessageCarriesNoSecrets: recipients need not be admins, so
// the message may name the node and say what to do — and nothing else. This
// guards against someone later "helpfully" pasting the config or the error
// output into it.
func TestRestartAlertMessageCarriesNoSecrets(t *testing.T) {
	msg := renderRestartLoopAlert("hk-01 <重要>", 6, 30)
	if !strings.Contains(msg, "hk-01") || !strings.Contains(msg, "6") {
		t.Fatalf("alert does not say which node or how often: %s", msg)
	}
	// The node name is escaped rather than interpolated raw: a name containing
	// angle brackets must not break the HTML parse mode.
	if strings.Contains(msg, "<重要>") {
		t.Fatalf("node name was not HTML-escaped: %s", msg)
	}
	for _, forbidden := range []string{"password", "uuid", "private_key", "reality", "ssh"} {
		if strings.Contains(strings.ToLower(msg), forbidden) {
			t.Fatalf("alert message leaks %q: %s", forbidden, msg)
		}
	}
}

// callOps drives one ops-recipient handler with {id} bound, without standing up
// the router (which would need a real admin session).
func callOps(a *API, method, path, body string, id int64, h http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rc := chi.NewRouteContext()
	if id > 0 {
		rc.URLParams.Add("id", strconv.FormatInt(id, 10))
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// TestOpsRecipientHandlers walks the admin page's round trip: read the list,
// tick a non-admin, see the effective count move, send a test.
func TestOpsRecipientHandlers(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	uid := bindOps(t, st, "ops_person", "user", 4001, false)

	w := callOps(a, "GET", "/api/admin/ops-recipients", "", 0, a.handleListOpsRecipients)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		Data struct {
			Candidates []struct {
				UserID  int64 `json:"user_id"`
				IsAdmin bool  `json:"is_admin"`
				On      bool  `json:"on"`
			} `json:"candidates"`
			Effective int `json:"effective"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v (%s)", err, w.Body.String())
	}
	if len(listed.Data.Candidates) != 1 || listed.Data.Candidates[0].IsAdmin {
		t.Fatalf("a non-admin bound account is not offered as a candidate: %s", w.Body.String())
	}
	if listed.Data.Effective != 0 {
		t.Fatalf("nobody is ticked yet, but effective=%d", listed.Data.Effective)
	}

	// Nothing to send to yet — the button must say so rather than pretend.
	if w := callOps(a, "POST", "/api/admin/ops-recipients/test", "{}", 0, a.handleTestOpsAlert); w.Code != http.StatusBadRequest {
		t.Fatalf("test with no recipients: %d %s", w.Code, w.Body.String())
	}

	if w := callOps(a, "PUT", "/api/admin/ops-recipients/x", `{"on":true}`, uid, a.handleSetOpsRecipient); w.Code != http.StatusOK {
		t.Fatalf("toggle: %d %s", w.Code, w.Body.String())
	}
	w = callOps(a, "GET", "/api/admin/ops-recipients", "", 0, a.handleListOpsRecipients)
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Data.Effective != 1 || !listed.Data.Candidates[0].On {
		t.Fatalf("ticking a recipient did not take: %s", w.Body.String())
	}

	if w := callOps(a, "POST", "/api/admin/ops-recipients/test", "{}", 0, a.handleTestOpsAlert); w.Code != http.StatusOK {
		t.Fatalf("test send: %d %s", w.Code, w.Body.String())
	}
	if len(*inbox) != 1 || (*inbox)[0].chat != 4001 {
		t.Fatalf("test message did not reach the ticked account: %v", *inbox)
	}

	// An account with no binding cannot be ticked, and says why.
	orphan, err := st.CreateUser(store.NewUser{Username: "nobind2", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if w := callOps(a, "PUT", "/api/admin/ops-recipients/x", `{"on":true}`, orphan, a.handleSetOpsRecipient); w.Code != http.StatusBadRequest {
		t.Fatalf("ticking an unbound account: %d %s", w.Code, w.Body.String())
	}
}
