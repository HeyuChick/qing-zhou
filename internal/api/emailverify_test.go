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
