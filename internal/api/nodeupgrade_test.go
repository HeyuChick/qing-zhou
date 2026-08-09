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
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

func newNodeUpgradeAPI(t *testing.T) (*API, *store.Store) {
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

func upgradeReq(id int64) *http.Request {
	path := "/api/admin/nodes/" + strconv.FormatInt(id, 10) + "/singbox/upgrade"
	req := httptest.NewRequest("POST", path, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestNodeSingboxUpgradeReturnsBeforeInstallFinishes is the regression test for
// the reason this handler is asynchronous.
//
// The install pushes a ~60MB sing-box onto the node, which routinely runs past
// the server's 30s WriteTimeout (main.go). A synchronous handler therefore had
// its response torn off the wire while the install was still going, and the
// panel reported 安装失败 for a node that had in fact installed correctly. So
// what matters is not merely that the endpoint works, but that it ANSWERS
// promptly and leaves the work running behind it.
func TestNodeSingboxUpgradeReturnsBeforeInstallFinishes(t *testing.T) {
	a, st := newNodeUpgradeAPI(t)
	// A host that cannot be dialled: the point is when the handler returns, not
	// whether the install succeeds. SSH will sit in its own timeout well past
	// the assertion below.
	id, err := st.CreateServer(store.Server{
		Name: "landing", Host: "192.0.2.1", Port: 22, SSHUser: "root",
		SSHPassword: "hunter2", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	start := time.Now()
	a.handleAdminNodeSingboxUpgrade(w, upgradeReq(id))
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	// Generous: a correct handler returns in microseconds. Anything near the
	// 30s WriteTimeout means the work is being awaited inline again.
	if elapsed > 2*time.Second {
		t.Fatalf("handler blocked for %v; the install must run in the background", elapsed)
	}
	var body struct {
		Data struct {
			Started bool `json:"started"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if !body.Data.Started {
		t.Fatalf("expected started=true, got %s", w.Body.String())
	}

	// The job is visible while it runs, so the node list the UI polls can report
	// "安装中" instead of looking idle.
	if j, ok := a.upgradeSnapshot()[id]; !ok || !j.Running {
		t.Fatalf("expected a running job for server %d, got %+v", id, j)
	}

	// A second click while the first is in flight must be refused: two runs of
	// the install script on one machine would race over the same binary and
	// systemd unit.
	w2 := httptest.NewRecorder()
	a.handleAdminNodeSingboxUpgrade(w2, upgradeReq(id))
	if w2.Code != http.StatusConflict {
		t.Fatalf("concurrent upgrade: status = %d, want 409; body %s", w2.Code, w2.Body.String())
	}
}

// A node the panel has no way into must be refused up front rather than
// becoming a background job that fails a minute later somewhere the operator
// has to go looking for it.
func TestNodeSingboxUpgradeRejectsUnreachableConfig(t *testing.T) {
	a, st := newNodeUpgradeAPI(t)
	noCreds, err := st.CreateServer(store.Server{
		Name: "no-creds", Host: "192.0.2.2", Port: 22, SSHUser: "root", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		id   int64
	}{
		{"no ssh credentials", noCreds},
		{"unknown server", noCreds + 999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			a.handleAdminNodeSingboxUpgrade(w, upgradeReq(tc.id))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", w.Code, w.Body.String())
			}
			if _, ok := a.upgradeSnapshot()[tc.id]; ok {
				t.Fatal("a rejected request must not leave a job behind")
			}
		})
	}
}

// finishUpgrade is what the node list reads to decide between 安装中, a result,
// and a failure — including carrying the script's output, which is the whole
// diagnosis when an install goes wrong.
func TestUpgradeJobLifecycle(t *testing.T) {
	a, _ := newNodeUpgradeAPI(t)
	const id int64 = 7

	if !a.claimUpgrade(id) {
		t.Fatal("first claim must succeed")
	}
	if a.claimUpgrade(id) {
		t.Fatal("second claim while running must fail")
	}

	a.finishUpgrade(id, "downloading...\ndone", nil)
	j := a.upgradeSnapshot()[id]
	if j.Running {
		t.Fatal("job should be finished")
	}
	if j.Error != "" {
		t.Fatalf("unexpected error %q", j.Error)
	}
	if j.Output != "downloading...\ndone" {
		t.Fatalf("output = %q", j.Output)
	}
	if j.FinishedAt == 0 {
		t.Fatal("FinishedAt should be stamped")
	}

	// Once finished, the node can be reinstalled again.
	if !a.claimUpgrade(id) {
		t.Fatal("claim after completion must succeed")
	}
	a.finishUpgrade(id, "curl: (7) failed to connect", context.DeadlineExceeded)
	j = a.upgradeSnapshot()[id]
	if j.Error == "" {
		t.Fatal("expected an error to be recorded")
	}
	// The failure message must carry the script's own output, not just the Go
	// error: "exit status 1" alone gives the operator nothing to act on.
	if !strings.Contains(j.Error, "curl: (7)") {
		t.Fatalf("error %q should include the script output", j.Error)
	}
}
