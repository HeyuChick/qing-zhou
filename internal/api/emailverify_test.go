package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/auth"
	"qingzhou/internal/store"
)

const leakLink = "ss://LEAKED-UPSTREAM-CRED@upstream.example:8388#free"

// unverifiedFixture is an unverified user who would otherwise receive an
// external share_link via the free group — the leak #22 closed.
func unverifiedFixture(t *testing.T) (*API, *store.Store, int64) {
	t.Helper()
	a, st := newUserEditAPI(t)
	if err := st.SetSettingBool("email_verify_required", true); err != nil {
		t.Fatal(err)
	}
	gid, err := st.CreateGroup(store.NodeGroup{Name: "免费"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("free_group_id", strconv.FormatInt(gid, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateNode(store.Node{
		Type: "external", Name: "上游", Protocol: "ss",
		ShareLink: leakLink, Enabled: true, GroupIDs: []int64{gid},
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("secret1")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser(store.NewUser{
		Username: "unverified", Email: "u@example.com", PasswordHash: hash,
		SubToken: "tok-unverified", TrafficLimit: 10 << 30,
		ExpiryAt: time.Now().Unix() + 86400,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, st, uid
}

func loginAs(a *API, user, pass string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	body := `{"username":"` + user + `","password":"` + pass + `"}`
	a.handleLogin(w, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body)))
	return w
}

func getSub(a *API, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, httptest.NewRequest("GET", "/sub/"+token+"?format=info", nil))
	return w
}

// The resend-verify button only works after login. Mail scanners also consume
// the one-shot verify link, so the recovery path is "log in → 个人中心 → 重发".
// Blocking login made that a dead end.
func TestUnverified_CanStillLogin(t *testing.T) {
	a, _, _ := unverifiedFixture(t)
	w := loginAs(a, "unverified", "secret1")
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d %s — unverified users must be able to sign in to resend the verify mail",
			w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Token string `json:"token"`
			User  struct {
				EmailVerified bool `json:"email_verified"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Token == "" {
		t.Fatal("login succeeded but issued no token")
	}
	if resp.Data.User.EmailVerified {
		t.Fatal("fixture is supposed to be unverified")
	}
}

// The actual hole: an unverified account's /sub/{token} used to return the
// raw upstream share_link. Empty-but-valid is how expired/over-quota already
// look, so clients do not treat it as an error.
func TestUnverified_SubWithholdsExternalLinks(t *testing.T) {
	a, st, uid := unverifiedFixture(t)

	w := getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "LEAKED-UPSTREAM-CRED") || strings.Contains(body, "upstream.example") {
		t.Fatal("unverified subscription leaked the external share_link")
	}
	if !strings.Contains(body, "可用节点</span><b>0</b>") {
		t.Fatalf("info page should report 0 nodes for an unverified account:\n%s", body)
	}

	if err := st.SetEmailVerified(uid); err != nil {
		t.Fatal(err)
	}
	w = getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub after verify = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>1</b>") {
		t.Fatalf("verified account should see the free-group node:\n%s", w.Body.String())
	}
}

// v0.2.53 emptied every unverified subscription. Paying customers who never
// clicked the verify mail still had an active plan in the panel, but /sub
// came back with 0 nodes. A live paid plan means they are already in service.
func TestUnverified_SubPassesWhenUserHasPaidPlan(t *testing.T) {
	a, st, uid := unverifiedFixture(t)
	pkgID, err := st.CreatePackage(store.Package{
		Type: "plan", Name: "月付", PricePoints: 100,
		TrafficBytes: 100 << 30, DurationDays: 30, Stock: -1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := st.GetPackage(pkgID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AssignPackage(uid, pkg, 0, func(*store.User, bool) error { return nil }); err != nil {
		t.Fatal(err)
	}

	w := getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>1</b>") {
		t.Fatalf("unverified but paying account must still receive nodes:\n%s", w.Body.String())
	}
}

// Same outage, other shape: they registered when verify was off (or were
// provisioned before the gate), so they have a client identity but email_verified
// is still 0 — CreateUser always writes 0 and nothing flips it later.
func TestUnverified_SubPassesWhenAlreadyProvisioned(t *testing.T) {
	a, st, uid := unverifiedFixture(t)
	if err := st.SetUserClient(uid, 0, "qz_unverified", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}

	w := getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>1</b>") {
		t.Fatalf("already-provisioned unverified account must still receive nodes:\n%s", w.Body.String())
	}
}

// The signup grant is minted for every provisioned account and must not be
// mistaken for a paid plan — otherwise a brand-new unverified signup that
// somehow got a welcome bucket would punch through the leak gate.
func TestUnverified_WelcomeGrantDoesNotLiftTheGate(t *testing.T) {
	a, st, uid := unverifiedFixture(t)
	if err := st.EnsureWelcomeBucket(uid, "unverified", 10<<30, time.Now().Unix()+86400); err != nil {
		t.Fatal(err)
	}

	w := getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>0</b>") {
		t.Fatalf("welcome grant alone must not release external credentials:\n%s", w.Body.String())
	}
}

// Invite-code signup is allowed to skip email verify. Those accounts often
// have only the free group / signup grant — no paid plan, and (under the old
// gate) no client either — so the v0.2.53/54 heuristics still emptied them.
func TestUnverified_RegCodeUserKeepsNodes(t *testing.T) {
	a, st, uid := unverifiedFixture(t)
	codes, err := st.GenerateRegCodes(1, 1, "test", nil)
	if err != nil || len(codes) != 1 {
		t.Fatalf("GenerateRegCodes: %v %v", codes, err)
	}
	cid, ok := st.ConsumeRegCode(codes[0])
	if !ok {
		t.Fatal("ConsumeRegCode")
	}
	if err := st.RecordRegCodeUse(cid, uid, "unverified", ""); err != nil {
		t.Fatal(err)
	}

	w := getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>1</b>") {
		t.Fatalf("invite-code account must keep its nodes without verifying email:\n%s", w.Body.String())
	}
}

// Admin-created accounts are pre-verified. Even if that flag were missing,
// they are provisioned immediately — covered by the client-id path. This
// locks the documented contract: the verify switch does not apply to them.
func TestUnverified_AdminCreatedIsPreVerified(t *testing.T) {
	a, st, uid := unverifiedFixture(t)
	if err := st.SetEmailVerified(uid); err != nil {
		t.Fatal(err)
	}
	w := getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>1</b>") {
		t.Fatalf("admin-created (pre-verified) account must receive nodes:\n%s", w.Body.String())
	}
}

// Invite-code registration must not demand an email just because the open-
// signup verify switch is on.
func TestRegister_CodeDoesNotRequireEmailWhenVerifyOn(t *testing.T) {
	a, st := newUserEditAPI(t)
	if err := st.SetSetting("register_mode", "code"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettingBool("email_verify_required", true); err != nil {
		t.Fatal(err)
	}
	codes, err := st.GenerateRegCodes(1, 1, "", nil)
	if err != nil || len(codes) != 1 {
		t.Fatalf("GenerateRegCodes: %v %v", codes, err)
	}

	w := httptest.NewRecorder()
	body := `{"username":"codeuser","password":"secret1","code":"` + codes[0] + `"}`
	a.handleRegister(w, httptest.NewRequest("POST", "/api/auth/register", strings.NewReader(body)))
	if w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), "需要邮箱") {
		t.Fatalf("invite-code signup demanded an email: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "need_verify") {
		t.Fatalf("invite-code signup deferred to email verify: %s", w.Body.String())
	}
}

// Turning the setting off must not keep withholding nodes from unverified
// accounts — otherwise flipping the toggle would silently lock everyone out.
func TestUnverified_SubPassesWhenVerifyNotRequired(t *testing.T) {
	a, st, _ := unverifiedFixture(t)
	if err := st.SetSettingBool("email_verify_required", false); err != nil {
		t.Fatal(err)
	}
	w := getSub(a, "tok-unverified")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>1</b>") {
		t.Fatalf("verify_required=off should serve the free-group node:\n%s", w.Body.String())
	}
}
