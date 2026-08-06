package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

func newResetSubAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return New(st, []byte("secret"), nil), st
}

func post(a *API, uid int64, path string, h http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserID, uid))
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// adminPost invokes an /api/admin/users/{id}/... handler with the path param and
// the operator's identity in place, without standing up the whole router (which
// would need a real admin session).
func adminPost(a *API, operatorID, targetID int64, h http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/admin/users/"+strconv.FormatInt(targetID, 10)+"/reset-node-creds", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(targetID, 10))
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(context.WithValue(ctx, ctxUserID, operatorID))
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func resetSub(t *testing.T, a *API, uid int64) {
	t.Helper()
	if w := post(a, uid, "/api/user/reset-sub", a.handleResetSub); w.Code != http.StatusOK {
		t.Fatalf("reset-sub = %d %s", w.Code, w.Body.String())
	}
}

// resetCreds runs the credential rotation with the kill switch turned on — the
// tests below cover the switch itself separately.
func resetCreds(t *testing.T, a *API, st *store.Store, uid int64) *httptest.ResponseRecorder {
	t.Helper()
	if err := st.SetSetting("node_creds_reset_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	return post(a, uid, "/api/user/reset-node-creds", a.handleResetNodeCreds)
}

// Swapping the address must take effect immediately: the old /sub/<token> stops
// resolving, and the response must not be cacheable — a CDN or browser holding a
// 200 for a deleted token is indistinguishable from no swap at all.
func TestResetSub_OldURLStopsResolving(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "OLDTOKEN"})
	if err != nil {
		t.Fatal(err)
	}

	get := func(token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, httptest.NewRequest("GET", "/sub/"+token, nil))
		return w
	}

	before := get("OLDTOKEN")
	if before.Code != http.StatusOK {
		t.Fatalf("baseline: old token GET = %d, want 200", before.Code)
	}
	if cc := before.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: a cached copy survives revocation", cc)
	}

	resetSub(t, a, uid)

	if code := get("OLDTOKEN").Code; code != http.StatusNotFound {
		t.Errorf("old subscription link still resolves: GET /sub/OLDTOKEN = %d, want 404", code)
	}
	u, _ := st.UserByID(uid)
	if code := get(u.SubToken.String).Code; code != http.StatusOK {
		t.Errorf("new subscription link = %d, want 200", code)
	}
}

// Swapping the address is panel-only by design: it must leave the node
// credentials strictly alone, so no config changes and no node reloads. The
// price is that it does not revoke — which is why the copy says so and why the
// credential rotation exists as its own action.
func TestResetSub_LeavesNodeCredentialsAlone(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "OLDTOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}

	resetSub(t, a, uid)

	after, _ := st.ListBuckets(uid)
	if after[0].ClientUUID != "uuid" || after[0].ClientSecret != "secret" {
		t.Errorf("address swap rotated node credentials (%s/%s) — that would restart every node",
			after[0].ClientUUID, after[0].ClientSecret)
	}
	u, _ := st.UserByID(uid)
	if u.CredsResetAt != 0 {
		t.Error("address swap consumed the credential-rotation cooldown")
	}
}

// The half that actually revokes (issue #6). A leaked subscription has already
// handed out its node links, and those authenticate with the bucket credentials
// — not the token. If nothing rotates them, every link the old address served
// keeps working.
func TestResetNodeCreds_RotatesCredentials(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "OLDTOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserClient(uid, 0, "qz_u1", "leaked-uuid", "leaked-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "leaked-uuid", "leaked-secret"); err != nil {
		t.Fatal(err)
	}

	before, err := st.ListBuckets(uid)
	if err != nil || len(before) == 0 {
		t.Fatalf("ListBuckets = %v, %v; want at least one bucket", before, err)
	}

	if w := resetCreds(t, a, st, uid); w.Code != http.StatusOK {
		t.Fatalf("reset-node-creds = %d %s", w.Code, w.Body.String())
	}

	after, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("bucket count changed: %d -> %d", len(before), len(after))
	}
	for i, b := range after {
		old := before[i]
		if b.ClientUUID == old.ClientUUID {
			t.Errorf("bucket %q: client_uuid unchanged (%s) — links from the old subscription still authenticate",
				b.Name, b.ClientUUID)
		}
		if b.ClientSecret == old.ClientSecret {
			t.Errorf("bucket %q: client_secret unchanged (%s) — links from the old subscription still authenticate",
				b.Name, b.ClientSecret)
		}
		// The stats identity must survive, or the user's metered usage is orphaned.
		if b.ClientName != old.ClientName {
			t.Errorf("bucket %q: client_name changed %q -> %q, which orphans traffic accounting",
				b.Name, old.ClientName, b.ClientName)
		}
		// The mixed-proxy credential never leaves via the subscription, so it must
		// keep working: its effective value is pinned, not rotated.
		if b.ProxySecret() != old.ProxySecret() {
			t.Errorf("bucket %q: mixed-proxy password changed %q -> %q; it was never exposed by the link",
				b.Name, old.ProxySecret(), b.ProxySecret())
		}
		if b.ProxyName() != old.ProxyName() {
			t.Errorf("bucket %q: mixed-proxy username changed %q -> %q", b.Name, old.ProxyName(), b.ProxyName())
		}
	}

	// The users row seeds buckets provisioned later; a stale value there would
	// hand the leaked credential straight back.
	u, _ := st.UserByID(uid)
	if u.ClientUUID.String == "leaked-uuid" || u.ClientSecret.String == "leaked-secret" {
		t.Error("users row still holds the pre-reset credential")
	}
}

// The feature ships off. A disabled button is not an access control — the
// endpoint is reachable by any logged-in user, so the switch has to be enforced
// server-side, and the message has to be the one the tooltip promises.
func TestResetNodeCreds_DisabledByDefault(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}

	w := post(a, uid, "/api/user/reset-node-creds", a.handleResetNodeCreds)
	if w.Code != http.StatusForbidden {
		t.Errorf("reset-node-creds with the switch off = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "联系管理员") {
		t.Errorf("body = %s, want the 「该功能暂时禁用，有需要请联系管理员」 message", w.Body.String())
	}
	after, _ := st.ListBuckets(uid)
	if after[0].ClientUUID != "uuid" {
		t.Error("credentials rotated even though the feature is disabled")
	}
}

// Applying a rotation restarts sing-box on every server carrying the user's
// nodes, dropping everyone else's connections — so one per 30 days, enforced on
// the server, not by a greyed-out button.
func TestResetNodeCreds_CooldownIs30Days(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}

	if w := resetCreds(t, a, st, uid); w.Code != http.StatusOK {
		t.Fatalf("first rotation = %d %s", w.Code, w.Body.String())
	}
	afterFirst, _ := st.ListBuckets(uid)

	w := resetCreds(t, a, st, uid)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("second rotation = %d, want 429", w.Code)
	}
	afterSecond, _ := st.ListBuckets(uid)
	if afterSecond[0].ClientUUID != afterFirst[0].ClientUUID {
		t.Error("the rejected rotation still changed credentials")
	}

	// Just past the window, it's allowed again.
	u, _ := st.UserByID(uid)
	if _, err := st.DB().Exec(`UPDATE users SET creds_reset_at=? WHERE id=?`,
		u.CredsResetAt-int64(credsResetCooldown/time.Second)-1, uid); err != nil {
		t.Fatal(err)
	}
	if w := resetCreds(t, a, st, uid); w.Code != http.StatusOK {
		t.Errorf("rotation after the cooldown expired = %d %s, want 200", w.Code, w.Body.String())
	}
}

// The first-boot admin is inserted by Seed with no sub_token, so its 订阅 page
// served url:"" and format links that were just bare "?format=clash". The token
// is backfilled on read; an account that already has one must keep it, since
// silently reminting would revoke every configured client.
func TestSubscription_BackfillsMissingToken(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "admin", PasswordHash: "x"}) // no SubToken
	if err != nil {
		t.Fatal(err)
	}

	get := func() (string, map[string]any) {
		req := httptest.NewRequest("GET", "/api/user/subscription", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxUserID, uid))
		w := httptest.NewRecorder()
		a.handleSubscription(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("subscription = %d %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data struct {
				URL     string         `json:"url"`
				Formats map[string]any `json:"formats"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data.URL, resp.Data.Formats
	}

	url, formats := get()
	if url == "" {
		t.Fatal("subscription URL is empty for a token-less account")
	}
	for name, v := range formats {
		if s, _ := v.(string); s == "" || strings.HasPrefix(s, "?") {
			t.Errorf("format %q = %q, want an absolute URL", name, s)
		}
	}

	// Stable across reads — a fresh token every poll would be a silent reset.
	if again, _ := get(); again != url {
		t.Errorf("token changed between reads: %q -> %q", url, again)
	}
}

// A user who customized their proxy credential keeps exactly that one — the pin
// that protects the fallback case must not overwrite a value the user chose.
func TestResetNodeCreds_KeepsCustomProxyCredential(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "OLDTOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}
	bs, _ := st.ListBuckets(uid)
	if err := st.SetBucketProxyCred(bs[0].ID, uid, "myuser", "mypass", 0); err != nil {
		t.Fatal(err)
	}

	if w := resetCreds(t, a, st, uid); w.Code != http.StatusOK {
		t.Fatalf("reset-node-creds = %d %s", w.Code, w.Body.String())
	}

	after, _ := st.ListBuckets(uid)
	if after[0].ProxyUsername != "myuser" || after[0].ProxyPassword != "mypass" {
		t.Errorf("custom proxy credential clobbered: %q/%q", after[0].ProxyUsername, after[0].ProxyPassword)
	}
	if after[0].ClientUUID == "uuid" {
		t.Error("node credentials did not rotate — the proxy assertion above proves nothing")
	}
}

// The user-facing endpoint tells people to 「联系管理员」 when the switch is off, and
// the 订阅 page repeats it. That is only true if the operator actually has a way
// to do it — so the admin path must work with the switch off.
func TestAdminResetNodeCreds_WorksWithSwitchOff(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "leaked-uuid", "leaked-secret"); err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(store.NewUser{Username: "root", PasswordHash: "x", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}

	// The switch is off — the self-service path is refused, which is the exact
	// situation this endpoint exists for.
	if w := post(a, uid, "/api/user/reset-node-creds", a.handleResetNodeCreds); w.Code != http.StatusForbidden {
		t.Fatalf("precondition: self-service = %d, want 403", w.Code)
	}

	if w := adminPost(a, admin, uid, a.handleAdminResetNodeCreds); w.Code != http.StatusOK {
		t.Fatalf("admin reset-node-creds = %d %s", w.Code, w.Body.String())
	}
	after, _ := st.ListBuckets(uid)
	if after[0].ClientUUID == "leaked-uuid" || after[0].ClientSecret == "leaked-secret" {
		t.Error("admin rotation left the leaked credentials in place")
	}
}

// The operator path is the escape hatch from the cooldown, not another thing
// subject to it: an admin reaching for this is handling a leak on an account
// that has, by definition, already used its own rotation.
func TestAdminResetNodeCreds_IgnoresCooldown(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}
	if w := resetCreds(t, a, st, uid); w.Code != http.StatusOK {
		t.Fatalf("first rotation = %d %s", w.Code, w.Body.String())
	}
	first, _ := st.ListBuckets(uid)

	if w := adminPost(a, 1, uid, a.handleAdminResetNodeCreds); w.Code != http.StatusOK {
		t.Fatalf("admin rotation during the user's cooldown = %d %s", w.Code, w.Body.String())
	}
	after, _ := st.ListBuckets(uid)
	if after[0].ClientUUID == first[0].ClientUUID {
		t.Error("admin rotation was silently swallowed by the cooldown")
	}
}

func TestAdminResetNodeCreds_UnknownUser(t *testing.T) {
	a, _ := newResetSubAPI(t)
	if w := adminPost(a, 1, 4242, a.handleAdminResetNodeCreds); w.Code != http.StatusNotFound {
		t.Errorf("admin reset for a nonexistent user = %d, want 404", w.Code)
	}
}

// The cooldown is only a cooldown if it holds under concurrent requests. Checked
// here because it is enforced as the WHERE clause of the stamping UPDATE — a
// read-then-write would let several requests through together, each restarting
// sing-box on every server the user's nodes live on.
func TestResetNodeCreds_CooldownHoldsUnderConcurrency(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(uid, "qz_u1", "uuid", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("node_creds_reset_enabled", "true"); err != nil {
		t.Fatal(err)
	}

	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = post(a, uid, "/api/user/reset-node-creds", a.handleResetNodeCreds).Code
		}(i)
	}
	close(start)
	wg.Wait()

	granted := 0
	for i, c := range codes {
		switch c {
		case http.StatusOK:
			granted++
		case http.StatusTooManyRequests:
		default:
			t.Errorf("request %d = %d, want 200 or 429", i, c)
		}
	}
	if granted != 1 {
		t.Errorf("%d of %d concurrent rotations were granted, want exactly 1", granted, n)
	}
}

// The kill switch has to reach the button, or it is a setting with no observable
// effect and the button stays dead no matter what the admin does.
func TestSubscription_ReportsCredsResetSwitch(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "u1", PasswordHash: "x", SubToken: "T"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := func() bool {
		req := httptest.NewRequest("GET", "/api/user/subscription", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxUserID, uid))
		w := httptest.NewRecorder()
		a.handleSubscription(w, req)
		var resp struct {
			Data struct {
				CredsResetEnabled bool `json:"creds_reset_enabled"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data.CredsResetEnabled
	}

	if enabled() {
		t.Error("creds_reset_enabled is true by default; the feature ships off")
	}
	if err := st.SetSetting("node_creds_reset_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if !enabled() {
		t.Error("flipping node_creds_reset_enabled did not reach the subscription payload")
	}
}

// One user's actions must not disturb another's link or credentials.
func TestReset_IsolatedBetweenUsers(t *testing.T) {
	a, st := newResetSubAPI(t)
	mk := func(name, tok string) int64 {
		id, err := st.CreateUser(store.NewUser{Username: name, PasswordHash: "x", SubToken: tok})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.EnsurePoolBucket(id, "qz_"+name, "uuid-"+name, "sec-"+name); err != nil {
			t.Fatal(err)
		}
		return id
	}
	alice := mk("alice", "ALICE_TOKEN")
	bob := mk("bob", "BOB_TOKEN")

	bobBefore, _ := st.ListBuckets(bob)
	resetSub(t, a, alice)
	if w := resetCreds(t, a, st, alice); w.Code != http.StatusOK {
		t.Fatalf("reset-node-creds = %d %s", w.Code, w.Body.String())
	}

	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, httptest.NewRequest("GET", "/sub/BOB_TOKEN", nil))
	if w.Code != http.StatusOK {
		t.Errorf("bob's link broke: GET /sub/BOB_TOKEN = %d", w.Code)
	}
	bobAfter, _ := st.ListBuckets(bob)
	if bobAfter[0].ClientUUID != bobBefore[0].ClientUUID || bobAfter[0].ClientSecret != bobBefore[0].ClientSecret {
		t.Error("bob's credentials rotated by alice's reset")
	}
	bobUser, _ := st.UserByID(bob)
	if bobUser.CredsResetAt != 0 {
		t.Error("alice's rotation consumed bob's cooldown")
	}
}
