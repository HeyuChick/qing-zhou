package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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

func resetSub(t *testing.T, a *API, uid int64) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/user/reset-sub", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserID, uid))
	w := httptest.NewRecorder()
	a.handleResetSub(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reset-sub = %d %s", w.Code, w.Body.String())
	}
}

// The URL half of the revocation: the old /sub/<token> must stop resolving, and
// must not be cacheable — a CDN or browser holding a 200 for a deleted token is
// indistinguishable from no revocation at all.
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

// The half that actually matters (issue #6). A leaked subscription has already
// handed out its node links, and those authenticate with the bucket credentials
// — not the token. If reset leaves them in place, every link the old URL served
// keeps working and the reset is cosmetic.
func TestResetSub_RotatesNodeCredentials(t *testing.T) {
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

	resetSub(t, a, uid)

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
// must not overwrite a value the user chose.
func TestResetSub_KeepsCustomProxyCredential(t *testing.T) {
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

	resetSub(t, a, uid)

	after, _ := st.ListBuckets(uid)
	if after[0].ProxyUsername != "myuser" || after[0].ProxyPassword != "mypass" {
		t.Errorf("custom proxy credential clobbered: %q/%q", after[0].ProxyUsername, after[0].ProxyPassword)
	}
}
