package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// The pinned SSH host key is the panel's only defence against someone answering
// for a landing node's IP and collecting the root credentials we would hand
// over. It is also the thing that wedges the panel when the key legitimately
// changes — a reinstalled or provider-migrated VPS regenerates
// /etc/ssh/ssh_host_*, and from then on every config push fails with "host key
// mismatch (possible MITM)". These tests cover the two ways out, and the one
// case that must NOT clear the pin.

func newHostKeyAPI(t *testing.T) (*API, *store.Store) {
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

func withID(req *http.Request, id int64) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// 192.0.2.0/24 is TEST-NET-1: guaranteed unroutable, so the re-connect attempt
// fails instead of reaching a real machine.
func seedPinnedServer(t *testing.T, st *store.Store, host string) int64 {
	t.Helper()
	id, err := st.CreateServer(store.Server{
		Name: "landing", Host: host, Port: 22, SSHUser: "root",
		SSHPassword: "hunter2", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetServerHostKey(id, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOldKeyFromTheMachineBeforeItWasReinstalled"); err != nil {
		t.Fatal(err)
	}
	return id
}

func hostKeyOf(t *testing.T, st *store.Store, id int64) string {
	t.Helper()
	sv, err := st.GetServer(id)
	if err != nil || sv == nil {
		t.Fatalf("get server %d: %v", id, err)
	}
	return sv.HostKey
}

// The clear endpoint must drop the pin even when the follow-up connection
// fails. Keeping it on failure would leave the admin exactly where they
// started — refusing to connect — with no remaining way out of the UI.
func TestClearHostKeyDropsPinEvenWhenReconnectFails(t *testing.T) {
	a, st := newHostKeyAPI(t)
	id := seedPinnedServer(t, st, "192.0.2.1")

	rec := httptest.NewRecorder()
	req := withID(httptest.NewRequest("POST", "/api/admin/servers/1/clear-host-key", nil), id)
	a.handleAdminClearServerHostKey(rec, req)

	if got := hostKeyOf(t, st, id); got != "" {
		t.Fatalf("pinned host key survived the clear: %q", got)
	}
	// The admin must be told the reconnect failed rather than being left to
	// assume the machine is trusted again.
	if rec.Code == http.StatusOK {
		t.Fatalf("expected a failure status for the unreachable reconnect, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已清除") {
		t.Fatalf("response should say the pin was cleared, got: %s", rec.Body.String())
	}
}

// Repointing a server row at a different machine makes the stored pin belong to
// something we are no longer dialling. Left in place it produces a permanent
// "possible MITM" error for what is only a different host, so the update must
// drop it.
func TestUpdateServerClearsPinWhenHostChanges(t *testing.T) {
	a, st := newHostKeyAPI(t)
	id := seedPinnedServer(t, st, "192.0.2.1")

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"landing","host":"192.0.2.2","port":22,"ssh_user":"root"}`)
	req := withID(httptest.NewRequest("PUT", "/api/admin/servers/1", body), id)
	a.handleAdminUpdateServer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", rec.Code, rec.Body.String())
	}
	if got := hostKeyOf(t, st, id); got != "" {
		t.Fatalf("pin should be dropped when the host changes, still had %q", got)
	}
}

// Rotating the SSH password says nothing about the machine's identity. Clearing
// the pin here would mean anyone who can talk an admin into a password change
// also gets the panel to forget who it is talking to — so the pin must survive.
func TestUpdateServerKeepsPinWhenOnlyCredentialsChange(t *testing.T) {
	a, st := newHostKeyAPI(t)
	id := seedPinnedServer(t, st, "192.0.2.1")
	before := hostKeyOf(t, st, id)

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"landing-renamed","host":"192.0.2.1","port":2222,"ssh_user":"root","ssh_password":"newsecret"}`)
	req := withID(httptest.NewRequest("PUT", "/api/admin/servers/1", body), id)
	a.handleAdminUpdateServer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", rec.Code, rec.Body.String())
	}
	if got := hostKeyOf(t, st, id); got != before {
		t.Fatalf("pin must survive a credential/port/name change: %q -> %q", before, got)
	}
}

func TestUpdateServerEmptySecretFieldsClearStoredValues(t *testing.T) {
	a, st := newHostKeyAPI(t)
	id, err := st.CreateServer(store.Server{
		Name: "landing", Host: "192.0.2.1", Port: 22, SSHUser: "deploy",
		SSHPassword: "fixture", SSHKeyPass: "fixture", UseSudo: true,
		SudoPassword: "fixture", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"landing","host":"192.0.2.1","port":22,"ssh_user":"deploy","use_sudo":true,"ssh_key_pass":"","sudo_password":""}`)
	req := withID(httptest.NewRequest("PUT", "/api/admin/servers/1", body), id)
	a.handleAdminUpdateServer(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", rec.Code, rec.Body.String())
	}

	got, err := st.GetServer(id)
	if err != nil || got == nil {
		t.Fatalf("get server: %v", err)
	}
	if got.SSHKeyPass != "" || got.SudoPassword != "" {
		t.Fatalf("empty fields did not clear secrets: key pass=%q sudo pass=%q", got.SSHKeyPass, got.SudoPassword)
	}
}

func TestUpdateServerMaskedSecretFieldsKeepStoredValues(t *testing.T) {
	a, st := newHostKeyAPI(t)
	id, err := st.CreateServer(store.Server{
		Name: "landing", Host: "192.0.2.1", Port: 22, SSHUser: "deploy",
		SSHPassword: "fixture", SSHKeyPass: "fixture", UseSudo: true,
		SudoPassword: "fixture", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"landing","host":"192.0.2.1","port":22,"ssh_user":"deploy","use_sudo":true,"ssh_key_pass":"***","sudo_password":"***"}`)
	req := withID(httptest.NewRequest("PUT", "/api/admin/servers/1", body), id)
	a.handleAdminUpdateServer(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", rec.Code, rec.Body.String())
	}

	got, err := st.GetServer(id)
	if err != nil || got == nil {
		t.Fatalf("get server: %v", err)
	}
	if got.SSHKeyPass != "fixture" || got.SudoPassword != "fixture" {
		t.Fatalf("masked fields overwrote secrets: key pass=%q sudo pass=%q", got.SSHKeyPass, got.SudoPassword)
	}
}
