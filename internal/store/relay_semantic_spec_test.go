package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

func semanticSnapshot(t *testing.T, f userMeteringFixture) ([]*RelayMeteringLink, []*RelayMeteringUser, [][]byte) {
	t.Helper()
	links, err := f.st.RelayMeteringLinks()
	if err != nil {
		t.Fatal(err)
	}
	users, err := f.st.RelayMeteringUsers()
	if err != nil {
		t.Fatal(err)
	}
	configs := make([][]byte, len(f.servers))
	for i := len(configs) - 1; i >= 0; i-- {
		configs[i] = f.apply(t, i)
	}
	return links, users, configs
}

func TestRelaySemanticSpecNoopJSON(t *testing.T) {
	for _, scenario := range []string{"empty-options", "nested-key-order", "tls-json"} {
		t.Run(scenario, func(t *testing.T) {
			var f userMeteringFixture
			if scenario == "empty-options" {
				f = newUserMeteringFixture(t, 3)
			} else {
				p := meteringProtocolCase{name: "vless", protocol: "vless"}
				f = newProtocolMeteringFixture(t, p, p, p)
			}
			links, users, configs := semanticSnapshot(t, f)
			for i := range f.inbounds {
				ib, err := f.st.GetSbInbound(f.inbounds[i])
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "empty-options" {
					ib.Options = " { \n } "
				} else {
					ib.Options = ` { "transport": { "path":"/metering-logic", "type":"ws" }, "flow": "none" } `
				}
				if _, err = f.st.SaveSbInbound(ib); err != nil {
					t.Fatal(err)
				}
				if scenario == "tls-json" {
					tls, err := f.st.GetSbTls(ib.TlsID)
					if err != nil {
						t.Fatal(err)
					}
					var server, client map[string]interface{}
					if err = json.Unmarshal([]byte(tls.ServerJSON), &server); err != nil {
						t.Fatal(err)
					}
					if err = json.Unmarshal([]byte(tls.ClientJSON), &client); err != nil {
						t.Fatal(err)
					}
					a, _ := json.MarshalIndent(server, "", "   ")
					b, _ := json.MarshalIndent(client, "", "\t")
					tls.ServerJSON, tls.ClientJSON = string(a), string(b)
					if _, err = f.st.SaveSbTls(tls); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := f.st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			afterLinks, afterUsers, afterConfigs := semanticSnapshot(t, f)
			if len(links) != len(afterLinks) || len(users) != len(afterUsers) {
				t.Fatal("JSON formatting created extra identity rows")
			}
			for i := range links {
				if links[i].SpecHash != afterLinks[i].SpecHash || links[i].Generation != afterLinks[i].Generation || links[i].Credential != afterLinks[i].Credential {
					t.Fatal("formatting-only edit rotated the physical link")
				}
			}
			for i := range users {
				if users[i].ID != afterUsers[i].ID || users[i].Credential != afterUsers[i].Credential {
					t.Fatal("formatting-only edit rotated a user credential")
				}
			}
			for i := range configs {
				if !bytes.Equal(configs[i], afterConfigs[i]) {
					t.Fatalf("formatting-only edit changed hop %d config, causing a needless restart", i)
				}
			}
		})
	}
}

func TestRelaySemanticSpecLegacyUpgradeWithoutRotation(t *testing.T) {
	for _, migrate := range []bool{false, true} {
		t.Run(map[bool]string{false: "planner", true: "version-7-upgrade"}[migrate], func(t *testing.T) {
			p := meteringProtocolCase{name: "vless", protocol: "vless"}
			f := newProtocolMeteringFixture(t, p, p)
			links, users, configs := semanticSnapshot(t, f)
			target, _ := f.st.GetSbInbound(f.inbounds[1])
			hashes, _, _, err := f.st.relayTargetSpecHashesWith(f.st.db, target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.st.db.Exec(`UPDATE relay_metering_links SET spec_hash=?`, hashes.legacy); err != nil {
				t.Fatal(err)
			}
			if migrate {
				if _, err = f.st.db.Exec(`DROP TABLE relay_user_retirements; ALTER TABLE relay_credential_audit DROP COLUMN user_id; DROP INDEX idx_traffic_polls_server_state_time; DROP INDEX idx_traffic_observations_identity_positive; ALTER TABLE relay_metering_users DROP COLUMN source_auth_hashes; DELETE FROM schema_migrations WHERE version>='000008_relay_protocol_auth_verification'`); err != nil {
					t.Fatal(err)
				}
				if err = f.st.runMigrations(f.st.migrations()[:7]); err != nil {
					t.Fatalf("legacy hash fixture is not a valid version-7 database: %v", err)
				}
				if err = f.st.Migrate(); err != nil {
					t.Fatal(err)
				}
				got, _ := f.st.RelayMeteringLinks()
				if got[0].SpecHash != hashes.current {
					t.Fatal("migration did not normalize proven old hash before admin edits")
				}
				// The first post-upgrade edit is formatting only. There must still
				// be no generation churn before the first new planner run.
				target.Options = " \n" + target.Options + "\n "
				if _, err = f.st.SaveSbInbound(target); err != nil {
					t.Fatal(err)
				}
			}
			if err = f.st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			afterLinks, afterUsers, afterConfigs := semanticSnapshot(t, f)
			if len(afterLinks) != 1 || afterLinks[0].Generation != links[0].Generation || afterLinks[0].Credential != links[0].Credential || !strings.HasPrefix(afterLinks[0].SpecHash, relaySemanticSpecPrefix) {
				t.Fatal("equivalent old hash rotated link or was not normalized")
			}
			if len(afterUsers) != len(users) {
				t.Fatal("upgrade added user generations")
			}
			for i := range users {
				if users[i].Credential != afterUsers[i].Credential {
					t.Fatal("upgrade rekeyed user")
				}
			}
			for i := range configs {
				if !bytes.Equal(configs[i], afterConfigs[i]) {
					t.Fatal("hash-only upgrade changed the core configuration")
				}
			}
		})
	}
}

func TestRelaySemanticSpecStaleLegacyStillRotates(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	links, _, _ := semanticSnapshot(t, f)
	target, _ := f.st.GetSbInbound(f.inbounds[1])
	hashes, _, _, err := f.st.relayTargetSpecHashesWith(f.st.db, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.db.Exec(`UPDATE relay_metering_links SET spec_hash=?`, hashes.legacy); err != nil {
		t.Fatal(err)
	}
	target.ListenPort++
	if _, err = f.st.SaveSbInbound(target); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	after, _ := f.st.RelayMeteringLinks()
	if after[0].Generation != links[0].Generation+1 || after[0].Credential == links[0].Credential || after[0].State != "prepared" {
		t.Fatal("a genuinely changed endpoint was incorrectly blessed as a legacy no-op")
	}
	if _, err = f.config(t, 0); err == nil {
		t.Fatal("source switched before genuinely changed target accepted")
	}
}

func TestRelaySemanticSpecCanonicalNumbersAndArrayOrder(t *testing.T) {
	a, err := canonicalRelayJSONObject(`{"count":1e3,"x":["a","b"]}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalRelayJSONObject(` {"x":["a","b"],"count":1000.0} `)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("equivalent renderer numeric values differ")
	}
	c, _ := canonicalRelayJSONObject(`{"count":1000,"x":["b","a"]}`)
	if a == c {
		t.Fatal("semantic hash reordered significant arrays")
	}
}

func TestRelaySemanticSpecLegacyManagedCertNeedsFreshAcceptance(t *testing.T) {
	p := meteringProtocolCase{name: "vless", protocol: "vless"}
	f := newProtocolMeteringFixture(t, p, p)
	target, _ := f.st.GetSbInbound(f.inbounds[1])
	certPEM, keyPEM, err := singbox.GenerateSelfSignedCert("localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	certID, err := f.st.SaveCert(&Cert{Name: "semantic-upgrade-cert", Domain: "localhost", Source: "paste", CertPEM: certPEM, KeyPEM: keyPEM})
	if err != nil {
		t.Fatal(err)
	}
	tls, _ := f.st.GetSbTls(target.TlsID)
	tls.CertID, tls.ServerJSON, tls.ClientJSON = certID, `{"enabled":true}`, `{}`
	if _, err = f.st.SaveSbTls(tls); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	links, users, _ := semanticSnapshot(t, f)
	hashes, _, _, err := f.st.relayTargetSpecHashesWith(f.st.db, target)
	if err != nil {
		t.Fatal(err)
	}
	if hashes.legacy == hashes.previous {
		t.Fatal("test did not reconstruct unproven cert-id-only hash")
	}
	if _, err = f.st.db.Exec(`UPDATE relay_metering_links SET spec_hash=?`, hashes.legacy); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	after, _ := f.st.RelayMeteringLinks()
	if after[0].Generation != links[0].Generation || after[0].Credential != links[0].Credential || after[0].State != "prepared" {
		t.Fatal("legacy certificate hash rotated credentials or skipped target proof")
	}
	if _, err = f.config(t, 0); err == nil {
		t.Fatal("legacy cert-id hash endorsed target certificate contents")
	}
	f.apply(t, 1)
	f.apply(t, 0)
	protocolAssertState(t, f, "active")
	afterUsers, err := f.st.RelayMeteringUsers()
	if err != nil || len(afterUsers) != len(users) {
		t.Fatal("certificate re-acceptance multiplied identities")
	}
	// Once normalized, changing the certificate under this same ID is a real
	// specification change, not a formatting-only migration shortcut.
	cert, _ := f.st.GetCert(certID)
	cert.CertPEM, cert.KeyPEM, err = singbox.GenerateSelfSignedCert("localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.SaveCert(cert); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	rotated, _ := f.st.RelayMeteringLinks()
	if rotated[0].Generation != links[0].Generation+1 {
		t.Fatal("same-ID certificate change did not create a fresh generation")
	}
}

func TestRelaySemanticSpecKeepsTLSOverridePresence(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	if err := f.st.ConfigureTrafficMetering(false, false, false); err != nil {
		t.Fatal(err)
	}
	target, _ := f.st.GetSbInbound(f.inbounds[1])
	target.Type = "trojan"
	target.Options = `{"tls":{"enabled":true,"server_name":"original.example"}}`
	tlsID, err := f.st.SaveSbTls(&SbTls{Name: "inline-fallback-profile", ServerJSON: "", ClientJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	target.TlsID = tlsID
	if _, err = f.st.SaveSbInbound(target); err != nil {
		t.Fatal(err)
	}
	before, _, _, err := f.st.relayTargetSpec(target)
	if err != nil {
		t.Fatal(err)
	}
	tls, _ := f.st.GetSbTls(tlsID)
	tls.ServerJSON = "null"
	if _, err = f.st.SaveSbTls(tls); err != nil {
		t.Fatal(err)
	}
	nullHash, _, _, err := f.st.relayTargetSpec(target)
	if err != nil {
		t.Fatal(err)
	}
	if nullHash != before {
		t.Fatal("equivalent missing/null profile fallbacks differ")
	}
	tls.ServerJSON = "{}"
	if _, err = f.st.SaveSbTls(tls); err != nil {
		t.Fatal(err)
	}
	after, _, _, err := f.st.relayTargetSpec(target)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("explicit TLS override object was conflated with inline fallback")
	}
}
