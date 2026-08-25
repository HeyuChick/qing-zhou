package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"qingzhou/internal/store"
)

func TestSubscriptionFetchRecordsSuccessfulCanonicalResponse(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "sub-fetch", PasswordHash: "x", SubToken: "FETCH_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/sub/FETCH_TOKEN?format=meta", nil)
	r.Header.Set("User-Agent", "mihomo/1.19.0")
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("subscription status=%d body=%s", w.Code, w.Body.String())
	}

	u, _ := st.UserByID(uid)
	if u.SubLastFetchedAt == 0 || u.SubLastFormat != "clash" || u.SubLastClient != "mihomo" {
		t.Fatalf("fetch telemetry = %d/%q/%q", u.SubLastFetchedAt, u.SubLastFormat, u.SubLastClient)
	}
}

func TestSubscriptionFetchDoesNotRecordRejectedRequest(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "sub-banned", PasswordHash: "x", SubToken: "BANNED_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE users SET status='banned' WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/BANNED_TOKEN", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("banned subscription status=%d", w.Code)
	}
	u, _ := st.UserByID(uid)
	if u.SubLastFetchedAt != 0 {
		t.Fatalf("rejected request recorded as successful: %+v", u)
	}
}

func TestSubscriptionFetchRecordsBrowserInfoSeparately(t *testing.T) {
	a, st := newResetSubAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "sub-browser", PasswordHash: "x", SubToken: "BROWSER_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/sub/BROWSER_TOKEN", nil)
	r.Header.Set("Accept", browserAccept)
	r.Header.Set("User-Agent", "Mozilla/5.0 Chrome/126.0 Safari/537.36")
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("info response status/type=%d/%q", w.Code, w.Header().Get("Content-Type"))
	}

	u, _ := st.UserByID(uid)
	if u.SubLastFetchedAt == 0 || u.SubLastFormat != "info" || u.SubLastClient != "browser" {
		t.Fatalf("info telemetry = %d/%q/%q", u.SubLastFetchedAt, u.SubLastFormat, u.SubLastClient)
	}
}

func TestSubscriptionClientForUAIsBounded(t *testing.T) {
	for _, tc := range []struct {
		ua, want string
	}{
		{"Mozilla/5.0 Chrome/126.0 Safari/537.36", "browser"},
		{"Clash-Verge/v2.0", "clash"},
		{"SFI/1.11.0 (io.nekohasekai.sfa)", "sing-box"},
		{"Surge/2800 CFNetwork/1494", "surge"},
		{"some-private-client/123 with identifiers", "unknown"},
	} {
		if got := subscriptionClientForUA(tc.ua); got != tc.want {
			t.Errorf("clientForUA(%q)=%q want %q", tc.ua, got, tc.want)
		}
	}
}
