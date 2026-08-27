package store

import (
	"testing"
	"time"
)

// UpdateServer writes every column, so any caller that builds a Server from a
// partial payload silently blanks the rest. This pins the round-trip the admin
// edit form depends on: load → patch a couple of fields → save must not disturb
// credentials, the probe token, or the monitor-owned metadata.
func TestUpdateServer_PreservesUntouchedColumns(t *testing.T) {
	st := newRefundStore(t)

	id, err := st.CreateServer(Server{
		Name: "tokyo", Host: "1.2.3.4", Port: 22, SSHUser: "root",
		SSHKey: "PRIVATE-KEY", SSHPassword: "hunter2", SSHKeyPass: "passphrase",
		ConfigPath: "/etc/sing-box/config.json", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fields owned by the monitor page, set separately from the edit form.
	sv, _ := st.GetServer(id)
	sv.ProbeEnabled = true
	sv.ProbeToken = "probe-token-abc"
	sv.ExpiryDate = time.Now().Add(720 * time.Hour).Unix()
	sv.Provider = "Vultr"
	sv.Location = "Tokyo"
	sv.Spec = "1C1G"
	sv.Price = 5.5
	sv.Notes = "renew yearly"
	if err := st.UpdateServer(*sv); err != nil {
		t.Fatal(err)
	}

	// What the edit handler now does: load the row, apply the form's fields.
	edit, err := st.GetServer(id)
	if err != nil || edit == nil {
		t.Fatal("server vanished")
	}
	edit.Name = "tokyo-renamed"
	edit.Port = 2222
	if err := st.UpdateServer(*edit); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetServer(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "tokyo-renamed" || got.Port != 2222 {
		t.Errorf("edit did not apply: name=%q port=%d", got.Name, got.Port)
	}
	// The whole point: a rename must not cost the operator their SSH access.
	if got.SSHKey != "PRIVATE-KEY" {
		t.Errorf("ssh_key = %q, want it preserved — remote deploys would break irrecoverably", got.SSHKey)
	}
	if got.SSHPassword != "hunter2" || got.SSHKeyPass != "passphrase" {
		t.Errorf("ssh password/passphrase lost: %q / %q", got.SSHPassword, got.SSHKeyPass)
	}
	if got.ProbeToken != "probe-token-abc" || !got.ProbeEnabled {
		t.Errorf("probe token/enabled lost (%q / %v) — the running agent would start 403ing", got.ProbeToken, got.ProbeEnabled)
	}
	if got.Provider != "Vultr" || got.Location != "Tokyo" || got.Spec != "1C1G" || got.Price != 5.5 || got.Notes != "renew yearly" {
		t.Errorf("monitor metadata lost: %+v", got)
	}
	if got.ExpiryDate == 0 {
		t.Error("expiry_date cleared")
	}
}

// The probe token is looked up by hash; the hash must track the token through an
// update or the agent's reports stop authenticating.
func TestUpdateServer_ProbeTokenHashStaysInSync(t *testing.T) {
	st := newRefundStore(t)
	id, err := st.CreateServer(Server{Name: "s", Host: "h", Port: 22, ProbeEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	sv, _ := st.GetServer(id)
	sv.ProbeEnabled = true
	sv.ProbeToken = "tok-1"
	if err := st.UpdateServer(*sv); err != nil {
		t.Fatal(err)
	}
	found, err := st.GetServerByProbeToken("tok-1")
	if err != nil || found == nil {
		t.Fatalf("probe token lookup failed after update: %v", err)
	}
	if found.ID != id {
		t.Errorf("looked up server %d, want %d", found.ID, id)
	}
}

func TestEnableServerProbeDoesNotRewriteUnrelatedFields(t *testing.T) {
	st := newRefundStore(t)
	id, err := st.CreateServer(Server{
		Name: "before", Host: "192.0.2.10", Port: 2222,
		SSHUser: "root", SSHPassword: "secret", SingBoxBin: "/opt/sing-box",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnableServerProbe(id, "probe-token"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetServer(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ProbeEnabled || got.ProbeToken != "probe-token" {
		t.Fatalf("probe access = enabled %v token %q", got.ProbeEnabled, got.ProbeToken)
	}
	if got.Name != "before" || got.Host != "192.0.2.10" || got.Port != 2222 ||
		got.SSHPassword != "secret" || got.SingBoxBin != "/opt/sing-box" {
		t.Fatalf("unrelated server fields changed: %+v", got)
	}
}

// "即将到期" must mean upcoming, not "ever expired". Without a lower bound every
// long-expired server is counted forever.
func TestCountProbeServers_ExpiringExcludesAlreadyExpired(t *testing.T) {
	st := newRefundStore(t)
	now := time.Now()

	mk := func(name string, expiry int64) {
		id, err := st.CreateServer(Server{Name: name, Host: name, Port: 22, ProbeEnabled: true})
		if err != nil {
			t.Fatal(err)
		}
		sv, _ := st.GetServer(id)
		sv.ProbeEnabled = true
		sv.ExpiryDate = expiry
		if err := st.UpdateServer(*sv); err != nil {
			t.Fatal(err)
		}
	}
	mk("expired-long-ago", now.AddDate(0, -6, 0).Unix())
	mk("expiring-tomorrow", now.Add(24*time.Hour).Unix())
	mk("expiring-next-year", now.AddDate(1, 0, 0).Unix())
	mk("no-expiry", 0)

	_, _, expiring, err := st.CountProbeServers()
	if err != nil {
		t.Fatal(err)
	}
	if expiring != 1 {
		t.Errorf("expiring = %d, want 1 (only the one due tomorrow)", expiring)
	}
}
