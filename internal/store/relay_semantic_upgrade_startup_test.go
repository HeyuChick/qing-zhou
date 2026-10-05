package store

import (
	"bytes"
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

func semanticStartupFixture(t *testing.T, managed bool) (userMeteringFixture, *SbInbound, relaySpecHashes, int) {
	t.Helper()
	p := meteringProtocolCase{name: "vless", protocol: "vless"}
	f := newProtocolMeteringFixture(t, p, p)
	target, err := f.st.GetSbInbound(f.inbounds[1])
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		cp, kp, err := singbox.GenerateSelfSignedCert("localhost", 1)
		if err != nil {
			t.Fatal(err)
		}
		certID, err := f.st.SaveCert(&Cert{Name: "startup-managed", Domain: "localhost", Source: "paste", CertPEM: cp, KeyPEM: kp})
		if err != nil {
			t.Fatal(err)
		}
		tls, err := f.st.GetSbTls(target.TlsID)
		if err != nil {
			t.Fatal(err)
		}
		tls.CertID, tls.ServerJSON, tls.ClientJSON = certID, "", `{}`
		if _, err = f.st.SaveSbTls(tls); err != nil {
			t.Fatal(err)
		}
		if err = f.st.PrepareRelayMetering(); err != nil {
			t.Fatal(err)
		}
	}
	links, _, _ := semanticSnapshot(t, f)
	hashes, _, _, err := f.st.relayTargetSpecHashesWith(f.st.db, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.db.Exec(`UPDATE relay_metering_links SET spec_hash=?`, hashes.legacy); err != nil {
		t.Fatal(err)
	}
	return f, target, hashes, links[0].Generation
}

func semanticRewindToV7(t *testing.T, st *Store) {
	t.Helper()
	if _, err := st.db.Exec(`DROP TABLE node_source_key_aliases; DROP TABLE relay_user_retirements;
 ALTER TABLE relay_credential_audit DROP COLUMN user_id;
 DROP INDEX idx_traffic_polls_server_state_time; DROP INDEX idx_traffic_observations_identity_positive;
 ALTER TABLE relay_metering_users DROP COLUMN source_auth_hashes;
 DELETE FROM schema_migrations WHERE version>='000008_relay_protocol_auth_verification'`); err != nil {
		t.Fatal(err)
	}
	if err := st.runMigrations(st.migrations()[:7]); err != nil {
		t.Fatal("invalid v7 fixture:", err)
	}
}

func TestRelaySemanticStartupJWTKeyBeforeSeed(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "encrypted-profile", true: "encrypted-cert-empty-profile"}[managed], func(t *testing.T) {
			f, target, hashes, generation := semanticStartupFixture(t, managed)
			if err := f.st.SetSetting("jwt_secret", "protocol-logic-state-fixture"); err != nil {
				t.Fatal(err)
			}
			semanticRewindToV7(t, f.st)
			f.st.secretKey = nil       // main.go loads the fallback only AFTER Migrate+Seed.
			f.st.db.SetMaxOpenConns(1) // Key/secret reads must use the migration transaction.
			if err := f.st.Migrate(); err != nil {
				t.Fatal(err)
			}
			f.st.db.SetMaxOpenConns(8)
			if len(f.st.secretKey) != 0 {
				t.Fatal("migration mutated the live Store encryption key")
			}
			links, err := f.st.RelayMeteringLinks()
			if err != nil || links[0].SpecHash != hashes.current {
				t.Fatalf("migration used wrong decrypted specification: %+v %v", links, err)
			}
			f.st.SetSecretKey([]byte("protocol-logic-state-fixture"))
			if !managed {
				target.Options = " \n" + target.Options + "\n "
				if _, err := f.st.SaveSbInbound(target); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			links, err = f.st.RelayMeteringLinks()
			if err != nil || links[0].Generation != generation {
				t.Fatalf("unchanged startup endpoint rotated: generation=%d -> %+v %v", generation, links, err)
			}
		})
	}
}

func TestRelaySemanticStartupKeyPrecedenceAndMissingKey(t *testing.T) {
	for _, mode := range []string{"explicit-wins", "explicit-wrong", "missing-key", "wrong-jwt"} {
		t.Run(mode, func(t *testing.T) {
			f, target, hashes, _ := semanticStartupFixture(t, true)
			if err := f.st.SetSetting("jwt_secret", "protocol-logic-state-fixture"); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "explicit-wins":
				if err := f.st.SetSetting("jwt_secret", "not-the-cipher-key"); err != nil {
					t.Fatal(err)
				}
			case "explicit-wrong":
				f.st.SetSecretKey([]byte("explicit-wrong-key"))
			case "missing-key":
				f.st.secretKey = nil
				if err := f.st.DeleteSetting("jwt_secret"); err != nil {
					t.Fatal(err)
				}
			case "wrong-jwt":
				f.st.secretKey = nil
				if err := f.st.SetSetting("jwt_secret", "wrong-jwt-key"); err != nil {
					t.Fatal(err)
				}
			}
			liveKey := append([]byte(nil), f.st.secretKey...)
			semanticRewindToV7(t, f.st)
			if err := f.st.Migrate(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(liveKey, f.st.secretKey) {
				t.Fatal("migration changed shared key state")
			}
			links, err := f.st.RelayMeteringLinks()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "explicit-wins" {
				if links[0].SpecHash != hashes.current {
					t.Fatal("explicit derived key was overridden or double-derived")
				}
				return
			}
			if links[0].SpecHash != hashes.legacy {
				t.Fatal("missing/wrong decryption key manufactured semantic proof")
			}
			if _, _, _, err := f.st.relayTargetSpecHashesWith(f.st.db, target); err == nil {
				t.Fatal("encrypted certificate without usable live key became a specification")
			}
		})
	}
}

func TestRelaySemanticUpgradeRetainsInstalledOptOutCredentials(t *testing.T) {
	for _, migrate := range []bool{false, true} {
		t.Run(map[bool]string{false: "planner", true: "migration"}[migrate], func(t *testing.T) {
			f, _, _, generation := semanticStartupFixture(t, true)
			before := map[int64]*RelayMeteringUser{}
			for _, user := range protocolCurrentUsers(t, f) {
				before[user.ID] = user
			}
			if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
				t.Fatal(err)
			}
			if migrate {
				semanticRewindToV7(t, f.st)
				if err := f.st.Migrate(); err != nil {
					t.Fatal(err)
				}
			} else if err := f.st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			raw, err := f.config(t, 1)
			if err != nil {
				t.Fatal(err)
			}
			after := protocolCurrentUsers(t, f)
			for _, user := range after {
				old := before[user.ID]
				if old == nil || user.AcceptedAt != old.AcceptedAt || user.AcceptedAt <= 0 || !strings.Contains(string(raw), user.IdentityName) {
					t.Fatalf("normalization removed historical installed credential: %+v", user)
				}
				if user.State != "prepared" || user.ActivatedAt != 0 {
					t.Fatal("historical installation receipt endorsed new endpoint")
				}
			}
			links, err := f.st.RelayMeteringLinks()
			if err != nil || links[0].Generation != generation || links[0].State != "prepared" || links[0].AcceptedAt != 0 {
				t.Fatalf("link readiness or generation wrong: %+v %v", links, err)
			}
			if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
				t.Fatal(err)
			}
			if err := f.st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			if _, err := f.config(t, 0); err == nil {
				t.Fatal("P1 source used preserved installation receipt before fresh target confirmation")
			}
			f.apply(t, 1)
			f.apply(t, 0)
			protocolAssertState(t, f, "active")
			for _, user := range protocolCurrentUsers(t, f) {
				if user.Credential != before[user.ID].Credential {
					t.Fatal("confirmation rotated installed compatibility credential")
				}
			}
		})
	}
}

func TestRelaySemanticRetirementRequiresFreshProof(t *testing.T) {
	for _, optout := range []bool{false, true} {
		t.Run(map[bool]string{false: "P1_enabled", true: "optout_then_reenable"}[optout], func(t *testing.T) {
			protocol := meteringProtocolCase{name: "vless", protocol: "vless"}
			f := newProtocolMeteringFixture(t, protocol, protocol)
			f.apply(t, 1)
			f.apply(t, 0)
			first, err := f.st.RelayMeteringUsers()
			if err != nil {
				t.Fatal(err)
			}
			old := first[0]
			target, err := f.st.GetSbInbound(f.inbounds[1])
			if err != nil {
				t.Fatal(err)
			}
			certPEM, keyPEM, err := singbox.GenerateSelfSignedCert("localhost", 1)
			if err != nil {
				t.Fatal(err)
			}
			certID, err := f.st.SaveCert(&Cert{Name: "semantic-retirement-managed", Domain: "localhost", Source: "paste", CertPEM: certPEM, KeyPEM: keyPEM})
			if err != nil {
				t.Fatal(err)
			}
			tls, err := f.st.GetSbTls(target.TlsID)
			if err != nil {
				t.Fatal(err)
			}
			tls.CertID, tls.ServerJSON, tls.ClientJSON = certID, `{"enabled":true}`, `{}`
			if _, err = f.st.SaveSbTls(tls); err != nil {
				t.Fatal(err)
			}
			if err = f.st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			f.apply(t, 1)
			sourceBefore := f.apply(t, 0)
			links, err := f.st.RelayMeteringLinks()
			if err != nil {
				t.Fatal(err)
			}
			link := links[0]
			if link.Generation <= old.Generation {
				t.Fatal("fixture requires a replaced credential")
			}
			v := RelayCredentialView{Kind: "user_generation", LinkID: old.LinkID, UserID: old.UserID, Generation: old.Generation, ServerID: f.servers[1], InboundID: f.inbounds[1]}
			quietRelayUser(t, f, old)
			if err = f.st.canRetireRelayUser(f.st.db, v); err != nil {
				t.Fatalf("baseline should be eligible: %v", err)
			}
			beforeUsers, err := f.st.RelayMeteringUsers()
			if err != nil {
				t.Fatal(err)
			}
			accepted := map[int64]int64{}
			identities := map[int64]string{}
			for _, u := range beforeUsers {
				if u.Generation == link.Generation {
					if u.State != "active" || u.AcceptedAt <= 0 {
						t.Fatal("fixture not active")
					}
					accepted[u.ID] = u.AcceptedAt
					identities[u.ID] = u.IdentityName
				}
			}
			hashes, _, _, err := f.st.relayTargetSpecHashesWith(f.st.db, target)
			if err != nil {
				t.Fatal(err)
			}
			if hashes.legacy == hashes.previous {
				t.Fatal("fixture did not exercise uncertified legacy managed-cert hash")
			}
			if optout {
				if err = f.st.ConfigureTrafficMetering(true, false, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.st.db.Exec(`UPDATE relay_metering_links SET spec_hash=? WHERE id=?`, hashes.legacy, link.ID); err != nil {
				t.Fatal(err)
			}
			if err = f.st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			afterLinks, _ := f.st.RelayMeteringLinks()
			if afterLinks[0].State != "prepared" || afterLinks[0].AcceptedAt != 0 || afterLinks[0].ActivatedAt != 0 || afterLinks[0].Generation != link.Generation {
				t.Fatal("hash upgrade retained readiness or rotated identity", afterLinks[0])
			}
			afterUsers, _ := f.st.RelayMeteringUsers()
			for _, u := range afterUsers {
				if at, ok := accepted[u.ID]; ok {
					if u.State != "prepared" || u.ActivatedAt != 0 || u.AcceptedAt != at || u.IdentityName != identities[u.ID] {
						t.Fatalf("published credential/readiness conflated: %+v", u)
					}
				}
			}
			landing, err := f.config(t, 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range identities {
				if !strings.Contains(string(landing), name) {
					t.Fatal("hash upgrade implicitly removed a published credential", name)
				}
			}
			if scalar(t, f.st, `SELECT COUNT(*) FROM relay_user_retirements WHERE state IN ('retiring','retired')`) != 0 || scalar(t, f.st, `SELECT COUNT(*) FROM relay_credential_audit`) != 0 {
				t.Fatal("hash normalization implicitly retired credentials")
			}
			if optout {
				if err = f.st.ConfigureTrafficMetering(true, false, true); err != nil {
					t.Fatal(err)
				}
			}
			if err = f.st.canRetireRelayUser(f.st.db, v); err == nil {
				t.Fatal("prepared generation inherited stale active/quiet proof")
			}
			if _, err = f.config(t, 0); err == nil {
				t.Fatal("retained accepted_at bypassed fresh target acceptance")
			}
			if err = f.st.RecordRelayConfigApplied(f.servers[0], sourceBefore); err != nil {
				t.Fatal(err)
			}
			if err = f.st.canRetireRelayUser(f.st.db, v); err == nil {
				t.Fatal("old accepted_at/source apply authorized retirement without fresh target proof")
			}
			current := protocolCurrentUsers(t, f)
			for _, u := range current {
				if u.State != "prepared" || u.ActivatedAt != 0 {
					t.Fatal("old source config activated prepared generation", u)
				}
			}
			if err = f.st.RecordRelayConfigApplied(f.servers[1], landing); err != nil {
				t.Fatal(err)
			}
			for _, u := range protocolCurrentUsers(t, f) {
				if u.State != "accepted" {
					t.Fatal("fresh target receipt not acknowledged", u)
				}
			}
			if err = f.st.canRetireRelayUser(f.st.db, v); err == nil {
				t.Fatal("target acceptance alone authorized retirement")
			}
			f.apply(t, 0)
			for _, u := range protocolCurrentUsers(t, f) {
				if u.State != "active" {
					t.Fatal("fresh two-stage apply failed", u)
				}
			}
			if err = f.st.canRetireRelayUser(f.st.db, v); err == nil {
				t.Fatal("stale quiet polls authorized retirement after fresh source apply")
			}
		})
	}
}
