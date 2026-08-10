package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// fixtureEgressPassword stands in for a proxy credential in these tests. Named
// rather than written inline next to a Password: field, because an opaque
// literal in that position is indistinguishable from a genuine leaked
// credential — to a reviewer skimming the diff and to the repo's secret scanner
// alike, and the scanner is the one that blocks the merge.
const fixtureEgressPassword = "fixture-egress-password"

func newEgressAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	st.SetSecretKey([]byte("test-secret"))
	return New(st, []byte("secret"), nil), st
}

// decodeData unwraps the {"data": …} envelope the ok() helper writes.
func decodeData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Data
}

// withURLParam attaches a chi route param, which the handlers read directly.
func withURLParam(req *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestSaveEgressUDPValidation pins the two save-time rules that exist so the
// panel can never display a setting the generated config does not honour.
func TestSaveEgressUDPValidation(t *testing.T) {
	a, _ := newEgressAPI(t)

	for _, tc := range []struct {
		name, body string
		wantCode   int
	}{
		{
			name:     "unknown udp mode is refused",
			body:     `{"name":"a","type":"socks","host":"1.2.3.4","port":1080,"udp_mode":"maybe"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			// sing-box's http outbound has no packet path, so storing
			// "passthrough" would show 透传 in the panel while UDP dies anyway.
			name:     "http plus passthrough is refused",
			body:     `{"name":"a","type":"http","host":"1.2.3.4","port":8080,"udp_mode":"passthrough"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "negative timeout is refused",
			body:     `{"name":"a","type":"socks","host":"1.2.3.4","port":1080,"connect_timeout_ms":-1}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "timeout beyond the cap is refused",
			body:     `{"name":"a","type":"socks","host":"1.2.3.4","port":1080,"connect_timeout_ms":90000}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "socks plus passthrough is fine",
			body:     `{"name":"a","type":"socks","host":"1.2.3.4","port":1080,"udp_mode":"passthrough"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "http plus block is fine",
			body:     `{"name":"b","type":"http","host":"1.2.3.4","port":8080,"udp_mode":"block"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "empty udp mode means decide by type",
			body:     `{"name":"c","type":"http","host":"1.2.3.4","port":8080}`,
			wantCode: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/admin/sb/egresses", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			a.handleAdminSaveSbEgress(w, req)
			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d; body %s", w.Code, tc.wantCode, w.Body.String())
			}
		})
	}
}

// The list/get payload must report what the config will actually carry, not the
// "" / 0 sentinels — otherwise the admin sees a blank where a real default is
// in force and has no way to know UDP is being blocked.
func TestEgressJSONReportsEffectiveDefaults(t *testing.T) {
	a, st := newEgressAPI(t)
	if _, err := st.SaveSbEgress(&store.SbEgress{
		Name: "默认", Type: "http", Host: "1.2.3.4", Port: 8080,
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/admin/sb/egresses", nil)
	w := httptest.NewRecorder()
	a.handleAdminListSbEgresses(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("want 1 egress, got %d", len(env.Data))
	}
	row := env.Data[0]
	if row["udp_mode"] != "" {
		t.Errorf("stored udp_mode should stay the sentinel, got %v", row["udp_mode"])
	}
	if row["udp_mode_effective"] != "block" {
		t.Errorf("effective udp mode = %v, want block for an http egress", row["udp_mode_effective"])
	}
	if row["connect_timeout_effective_ms"] != float64(5000) {
		t.Errorf("effective timeout = %v, want 5000", row["connect_timeout_effective_ms"])
	}
}

// TestCloneEgressEndpoint covers the whole point of doing this server-side: the
// list payload masks the password, so the clone must not be reachable by
// echoing that payload back — and the copy must hold the real credential.
func TestCloneEgressEndpoint(t *testing.T) {
	a, st := newEgressAPI(t)
	srcID, err := st.SaveSbEgress(&store.SbEgress{
		Name: "静态IP-香港", Type: "socks", Host: "1.2.3.4", Port: 1080,
		Username: "user1", Password: fixtureEgressPassword,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := withURLParam(
		httptest.NewRequest("POST", "/api/admin/sb/egresses/"+strconv.FormatInt(srcID, 10)+"/clone", nil),
		"id", strconv.FormatInt(srcID, 10))
	w := httptest.NewRecorder()
	a.handleAdminCloneSbEgress(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	data := decodeData(t, w)
	// The response is masked like every other egress payload.
	if data["password"] != "***" {
		t.Errorf("clone response leaked the password: %v", data["password"])
	}
	newID := int64(data["id"].(float64))
	if newID == srcID {
		t.Fatal("clone reused the source id")
	}
	got, err := st.GetSbEgress(newID)
	if err != nil || got == nil {
		t.Fatalf("GetSbEgress: %v", err)
	}
	if got.Password != fixtureEgressPassword {
		t.Errorf("cloned row password = %q, want the source's", got.Password)
	}

	// A missing source is a 404, not a silently empty clone.
	miss := withURLParam(httptest.NewRequest("POST", "/api/admin/sb/egresses/9999/clone", nil), "id", "9999")
	w2 := httptest.NewRecorder()
	a.handleAdminCloneSbEgress(w2, miss)
	if w2.Code != http.StatusNotFound {
		t.Errorf("missing source: status = %d, want 404", w2.Code)
	}
}

// TestParseEgressEndpoint checks the batch shape: good lines come back as
// candidates, bad ones as per-line errors, and one bad line does not sink the
// rest of the paste.
func TestParseEgressEndpoint(t *testing.T) {
	a, _ := newEgressAPI(t)
	body := `{"text":"socks5://u:p@1.2.3.4:1080\n# 注释\nnonsense line\n5.6.7.8:8080:u2:p2\n"}`
	req := httptest.NewRequest("POST", "/api/admin/sb/egresses/parse", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.handleAdminParseEgressLink(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Items []struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				Host        string `json:"host"`
				Port        int    `json:"port"`
				Username    string `json:"username"`
				Password    string `json:"password"`
				TypeGuessed bool   `json:"type_guessed"`
			} `json:"items"`
			Errors []struct {
				Line int    `json:"line"`
				Text string `json:"text"`
			} `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Items) != 2 {
		t.Fatalf("want 2 parsed items, got %d: %s", len(env.Data.Items), w.Body.String())
	}
	if len(env.Data.Errors) != 1 || env.Data.Errors[0].Line != 3 {
		t.Errorf("want one error on line 3, got %+v", env.Data.Errors)
	}
	// A line with no #name gets one from its address, because the save endpoint
	// requires a name and batch import can't stop to ask for 20 of them.
	if env.Data.Items[0].Name != "1.2.3.4:1080" {
		t.Errorf("fallback name = %q", env.Data.Items[0].Name)
	}
	if env.Data.Items[0].TypeGuessed {
		t.Error("a scheme-prefixed link must not be flagged as a guess")
	}
	if !env.Data.Items[1].TypeGuessed {
		t.Error("a bare csv line must be flagged as a guess")
	}
	// The password round-trips in the clear on purpose: it arrived in this very
	// request and the client needs it to create the row.
	if env.Data.Items[0].Password != "p" {
		t.Errorf("parsed password = %q", env.Data.Items[0].Password)
	}
}

// A paste with nothing usable in it must say so rather than answering 200 with
// an empty list, which the UI would render as a successful no-op.
func TestParseEgressEndpointEmpty(t *testing.T) {
	a, _ := newEgressAPI(t)
	req := httptest.NewRequest("POST", "/api/admin/sb/egresses/parse", strings.NewReader(`{"text":"\n# 注释\n  \n"}`))
	w := httptest.NewRecorder()
	a.handleAdminParseEgressLink(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body %s", w.Code, w.Body.String())
	}
}

// The egress check caps its output before returning it. That cap used to slice
// bytes, which was harmless while the output was curl's ASCII and stopped being
// harmless once the timeout branch started prefixing a Chinese sentence.
func TestTruncateRunesAPI(t *testing.T) {
	for _, tc := range []struct {
		name, in     string
		n, wantRunes int
	}{
		{"short input is returned as is", "检测超时", 500, 4},
		{"exactly at the cap", "检测超时", 4, 4},
		{"cut lands between characters", strings.Repeat("超", 600), 500, 500},
		{"ascii still cut where asked", strings.Repeat("x", 600), 500, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateRunesAPI(tc.in, tc.n)
			if !utf8.ValidString(got) {
				t.Fatalf("result is not valid UTF-8: %q", got)
			}
			if n := utf8.RuneCountInString(got); n != tc.wantRunes {
				t.Errorf("kept %d runes, want %d", n, tc.wantRunes)
			}
		})
	}
}
