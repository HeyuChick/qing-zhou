package api

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

func TestClashSourceRefreshPreservesDisabledNodeEndToEnd(t *testing.T) {
	a, st := newResetSubAPI(t)
	group, err := st.CreateGroup(store.NodeGroup{Name: "free"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSetting("free_group_id", strconv.FormatInt(group, 10)); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSetting("email_verify_required", "false"); err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser(store.NewUser{Username: "source-pref", PasswordHash: "fixture", SubToken: "SOURCEPREF"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := st.CreateSource(store.NodeSource{Name: "source", URL: "https://source.example.test/sub", Enabled: true, GroupIDs: []int64{group}})
	if err != nil {
		t.Fatal(err)
	}
	oldJSON := `{"add":"node.example.test","aid":"0","allowInsecure":"1","host":"cdn.test","id":"11111111-2222-4333-8444-555555555555","net":"ws","path":"/ws","port":"443","ps":"same-name","sni":"tls.test","tls":"tls","type":"none","v":"2"}`
	old := "vmess://" + base64.StdEncoding.EncodeToString([]byte(oldJSON))
	oldKey := subconv.NodeKey(old)
	if err = st.ReplaceSourceNodes(source, []store.Node{{Name: "same-name", Protocol: "vmess", ShareLink: old}}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err = st.SetNodeDisabled(uid, oldKey, true); err != nil {
		t.Fatal(err)
	}
	yaml := `proxies:
- name: same-name
  type: vmess
  server: node.example.test
  port: 443
  uuid: 11111111-2222-4333-8444-555555555555
  alterId: 0
  tls: true
  servername: tls.test
  skip-cert-verify: true
  network: ws
  ws-opts:
    path: /ws
    headers: {Host: cdn.test}
`
	a.sourceClient = &http.Client{Transport: sourceRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(yaml)), Header: make(http.Header)}, nil
	})}
	for pass := 0; pass < 2; pass++ {
		src, _ := st.GetSource(source)
		if n, msg := a.fetchSource(context.Background(), src, nil); n != 1 || msg != "" {
			t.Fatalf("refresh=%d %q", n, msg)
		}
	}
	user, _ := st.UserByID(uid)
	entries := a.computeNodeEntries(user)
	if len(entries) != 1 {
		t.Fatalf("entries=%d", len(entries))
	}
	disabled, _ := st.DisabledNodeKeys(uid)
	if len(disabled) != 1 || !disabled[oldKey] || !entries[0].disabled(disabled) {
		t.Fatal("refresh re-enabled node or rewrote preference")
	}
	req := httptest.NewRequest("GET", "/api/user/nodes", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserID, uid))
	response := httptest.NewRecorder()
	a.handleUserNodes(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"disabled":true`) {
		t.Fatal("UI lost disabled state", response.Body.String())
	}
	getSub := func() string {
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, httptest.NewRequest("GET", "/sub/SOURCEPREF?format=base64", nil))
		if w.Code != 200 {
			t.Fatalf("sub status=%d", w.Code)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if strings.Contains(getSub(), "vmess://") {
		t.Fatal("subscription revived disabled source node")
	}
	key := subconv.NodeKey(entries[0].Link)
	if err = a.applyNodePrefsCompat(user, nil, []string{key}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(getSub(), "vmess://") {
		t.Fatal("explicit enable did not release the node")
	}
}
