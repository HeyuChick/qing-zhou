package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/store"
)

func TestSubscriptionAIRouteUsesOnlyAccessibleAIMembership(t *testing.T) {
	a, st := newUserEditAPI(t)
	ordinary, err := st.CreateGroup(store.NodeGroup{Name: "可访问"})
	if err != nil {
		t.Fatal(err)
	}
	inaccessibleAI, err := st.CreateGroup(store.NodeGroup{Name: "未授权 AI", IsAI: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("free_group_id", strconv.FormatInt(ordinary, 10)); err != nil {
		t.Fatal(err)
	}
	link := "trojan://pw@1.2.3.4:443?security=tls#node"
	if _, err := st.CreateNode(store.Node{
		Type: "external", Name: "node", Protocol: "trojan", ShareLink: link,
		Enabled: true, GroupIDs: []int64{ordinary, inaccessibleAI},
	}); err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser(store.NewUser{
		Username: "ai-user", PasswordHash: "x", SubToken: "AI_TOKEN",
		TrafficLimit: 1 << 30, ExpiryAt: time.Now().Add(24 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	get := func() string {
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, httptest.NewRequest("GET", "/sub/AI_TOKEN?format=clash", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("subscription status = %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if body := get(); strings.Contains(body, "qingzhou-ai") {
		t.Fatal("an inaccessible AI group marked an otherwise accessible node")
	}
	if err := st.UpdateGroup(store.NodeGroup{ID: ordinary, Name: "可访问", IsAI: true}); err != nil {
		t.Fatal(err)
	}
	a.invalidateLinks(uid)
	if body := get(); !strings.Contains(body, "qingzhou-ai") || !strings.Contains(body, "★ AI 节点") {
		t.Fatal("accessible AI marker did not reach the rendered subscription")
	}
}
