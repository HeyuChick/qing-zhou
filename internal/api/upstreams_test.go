package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/auth"
	"qingzhou/internal/store"
)

func newUpstreamAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	st.SetSecretKey([]byte("upstream-at-rest-test-key"))
	return New(st, []byte("test-secret"), nil), st
}

func adminRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	return r.WithContext(context.WithValue(r.Context(), ctxRole, "admin"))
}

func TestUpstreamListMasksSecrets(t *testing.T) {
	a, st := newUpstreamAPI(t)
	if err := st.SetSetting(upstreamCloudflareSetting, `{"account_id":"acct","analytics_token":"top-secret","daily_request_limit":123}`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleAdminGetUpstreams(w, adminRequest(http.MethodGet, "/api/admin/upstreams", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "top-secret") {
		t.Fatalf("response leaked token: %s", w.Body.String())
	}
	var envelope struct {
		Data []upstreamView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 2 || !envelope.Data[1].AnalyticsTokenSet || envelope.Data[1].Limit != 123 {
		t.Fatalf("unexpected view %#v", envelope.Data)
	}
}

func TestUpstreamSaveStoresEncryptedConfig(t *testing.T) {
	a, st := newUpstreamAPI(t)
	body := `{"account_id":"acct","analytics_token":"top-secret","daily_request_limit":987}`
	req := adminRequest(http.MethodPut, "/api/admin/upstreams/cloudflare", body)
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "cloudflare")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	a.handleAdminPutUpstream(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var raw string
	if err := st.DB().QueryRow(`SELECT value FROM settings WHERE key=?`, upstreamCloudflareSetting).Scan(&raw); err != nil || !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "top-secret") {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	if strings.Contains(w.Body.String(), "top-secret") {
		t.Fatalf("save response leaked token: %s", w.Body.String())
	}
}

func TestUpstreamRefreshNeedsConfiguration(t *testing.T) {
	a, _ := newUpstreamAPI(t)
	req := adminRequest(http.MethodPost, "/api/admin/upstreams/cloudflare/refresh", "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "cloudflare")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	a.handleAdminRefreshUpstream(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "请先完整配置") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpstreamRoutesRequireAdminSession(t *testing.T) {
	a, st := newUpstreamAPI(t)
	uid, err := st.CreateUser(store.NewUser{Username: "upstream-admin", PasswordHash: "test", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	router := a.Router()
	noAuth := httptest.NewRecorder()
	router.ServeHTTP(noAuth, httptest.NewRequest(http.MethodGet, "/api/admin/upstreams", nil))
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d body=%s", noAuth.Code, noAuth.Body.String())
	}
	token, err := auth.Issue(a.secret, uid, "admin", "upstream-test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(uid, "upstream-test", "127.0.0.1", "test"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/upstreams", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	admin := httptest.NewRecorder()
	router.ServeHTTP(admin, req)
	if admin.Code != http.StatusOK {
		t.Fatalf("admin status=%d body=%s", admin.Code, admin.Body.String())
	}
}
