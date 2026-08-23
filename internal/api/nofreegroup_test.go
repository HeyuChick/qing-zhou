package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/store"
)

// No free-group setting and no plan-bound groups → subscription stays empty.
// The old zero-config path handed out every self-built node instead.
func TestSub_NoFreeGroupGivesNoNodes(t *testing.T) {
	a, st := newUserEditAPI(t)
	_, err := st.CreateUser(store.NewUser{
		Username: "bare", PasswordHash: "x", SubToken: "tok-bare",
		TrafficLimit: 10 << 30, ExpiryAt: time.Now().Unix() + 86400,
	})
	if err != nil {
		t.Fatal(err)
	}

	w := getSub(a, "tok-bare")
	if w.Code != http.StatusOK {
		t.Fatalf("sub = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "可用节点</span><b>0</b>") {
		t.Fatalf("no free group must not dump every node onto a plan-less user:\n%s", w.Body.String())
	}
}
