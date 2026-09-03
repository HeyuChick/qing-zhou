package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

func TestSubscriptionProfileLinksAreOptIn(t *testing.T) {
	base := "https://panel.example/sub/TOKEN"
	cn := subscriptionProfileLinks(base, subconv.ProfileCNDirect)
	if cn["url"] != base+"?profile=cn-direct" {
		t.Errorf("cn-direct url = %v", cn["url"])
	}
	formats, _ := cn["formats"].(J)
	if formats["clash"] != base+"?profile=cn-direct&format=clash" {
		t.Errorf("clash profile URL = %v", formats["clash"])
	}
	if formats["base64"] != base+"?format=base64" {
		t.Errorf("base64 falsely carries a routing profile: %v", formats["base64"])
	}
}

func TestPublicSubscriptionProfileDoesNotChangeLegacy(t *testing.T) {
	a, st := newResetSubAPI(t)
	_, err := st.CreateUser(store.NewUser{Username: "profile-user", PasswordHash: "x", SubToken: "PROFILE_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}

	get := func(query string) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/sub/PROFILE_TOKEN"+query, nil)
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", query, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	legacy := get("?format=clash")
	unknown := get("?format=clash&profile=typo")
	cn := get("?format=clash&profile=cn-direct")
	all := get("?format=clash&profile=proxy-all")
	if legacy != unknown {
		t.Error("unknown profile changed the legacy subscription")
	}
	if strings.Contains(legacy, "GEOSITE,CN,") || !strings.Contains(cn, "GEOSITE,CN,DIRECT") {
		t.Error("legacy or cn-direct Clash routing is wrong")
	}
	if !strings.Contains(all, "GEOSITE,CN,✈️ 节点选择") {
		t.Error("proxy-all Clash routing is missing")
	}
}

func TestSurgeManagedConfigRetainsExplicitProfile(t *testing.T) {
	a, st := newResetSubAPI(t)
	_, err := st.CreateUser(store.NewUser{Username: "surge-profile", PasswordHash: "x", SubToken: "SURGE_PROFILE"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/sub/SURGE_PROFILE?format=surge&profile=proxy-all", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/sub/SURGE_PROFILE?profile=proxy-all interval=") {
		t.Errorf("managed config lost profile: %s", w.Body.String())
	}
}
