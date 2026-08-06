package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"qingzhou/internal/singbox"
)

// TestEgressWiring exercises the third-party proxy egress: an inbound with
// EgressID must get a socks outbound (with credentials) + a route rule steering
// its tag into it, while a sibling inbound without an egress stays direct.
func TestEgressWiring(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	st.SetSecretKey([]byte("test-secret"))

	egID, err := st.SaveSbEgress(&SbEgress{
		Name: "静态IP", Type: "socks", Host: "9.9.9.9", Port: 1080,
		Username: "user1", Password: "pw1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Password must be encrypted at rest but returned decrypted.
	var raw string
	if err := st.db.QueryRow(`SELECT password FROM sb_egresses WHERE id=?`, egID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "pw1" {
		t.Error("egress password stored in cleartext")
	}
	eg, err := st.GetSbEgress(egID)
	if err != nil || eg == nil || eg.Password != "pw1" {
		t.Fatalf("GetSbEgress: %+v, %v", eg, err)
	}

	inbID, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "via-egress", ListenPort: 443,
		Options: `{}`, Enabled: true, EgressID: egID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "direct-exit", ListenPort: 444,
		Options: `{}`, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	users := map[string][]singbox.User{
		"via-egress":  {{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}},
		"direct-exit": {{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}},
	}
	cfgBytes, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "", users)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}

	wantTag := "egress-" + itoa(egID)
	if !hasOutbound(cfg, wantTag) {
		t.Errorf("config missing egress outbound %q:\n%s", wantTag, cfgBytes)
	}
	if !hasRouteToOutbound(cfg, wantTag, "via-egress") {
		t.Errorf("config missing route rule via-egress -> %q:\n%s", wantTag, cfgBytes)
	}
	if hasRouteToOutbound(cfg, wantTag, "direct-exit") {
		t.Errorf("direct-exit must not be routed into the egress:\n%s", cfgBytes)
	}
	// The outbound must dial with the decrypted credentials.
	obs, _ := cfg["outbounds"].([]any)
	for _, o := range obs {
		m, ok := o.(map[string]any)
		if !ok || m["tag"] != wantTag {
			continue
		}
		if m["type"] != "socks" || m["server"] != "9.9.9.9" || m["username"] != "user1" || m["password"] != "pw1" || m["version"] != "5" {
			t.Errorf("egress outbound malformed: %v", m)
		}
	}

	// Deleting an in-use egress must be refused; after the inbound is gone it works.
	if err := st.DeleteSbEgress(egID); err == nil {
		t.Error("DeleteSbEgress should refuse while an inbound references it")
	}
	if _, err := st.DeleteSbInbound(inbID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSbEgress(egID); err != nil {
		t.Errorf("DeleteSbEgress after unreference: %v", err)
	}
}

// TestEgressTLS covers the TLS-to-the-proxy hop ("HTTPS proxy"): the settings
// must survive a store round-trip, reach sing-box as a tls block carrying the
// pinned trust anchor, and — because sing-box's socks outbound has no tls
// option — never be emitted on a socks egress, where they would read as
// encrypted while the hop is actually plaintext.
func TestEgressTLS(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	st.SetSecretKey([]byte("test-secret"))

	const anchorPEM = "-----BEGIN CERTIFICATE-----\nTEST-ANCHOR\n-----END CERTIFICATE-----"
	certID, err := st.SaveCert(&Cert{
		Name: "自建代理", Domain: "proxy.example.com", Source: "selfsigned",
		CertPEM: anchorPEM, KeyPEM: "-----BEGIN PRIVATE KEY-----\nTEST-KEY\n-----END PRIVATE KEY-----",
	})
	if err != nil {
		t.Fatal(err)
	}

	tlsID, err := st.SaveSbEgress(&SbEgress{
		Name: "TLS出口", Type: "http", Host: "9.9.9.9", Port: 443,
		Username: "user1", Password: "pw1",
		TLSEnabled: true, SNI: "proxy.example.com", TLSCertID: certID,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSbEgress(tlsID)
	if err != nil || got == nil {
		t.Fatalf("GetSbEgress: %v", err)
	}
	if !got.TLSEnabled || got.SNI != "proxy.example.com" || got.TLSCertID != certID || got.TLSInsecure {
		t.Errorf("TLS fields did not round-trip: %+v", got)
	}

	// A socks egress with the flag set: it must not produce a tls block.
	socksID, err := st.SaveSbEgress(&SbEgress{
		Name: "SOCKS出口", Type: "socks", Host: "8.8.8.8", Port: 1080,
		TLSEnabled: true, SNI: "proxy.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "via-tls", ListenPort: 443,
		Options: `{}`, Enabled: true, EgressID: tlsID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "via-socks", ListenPort: 444,
		Options: `{}`, Enabled: true, EgressID: socksID,
	}); err != nil {
		t.Fatal(err)
	}

	users := map[string][]singbox.User{
		"via-tls":   {{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}},
		"via-socks": {{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}},
	}
	cfgBytes, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "", users)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}

	outbound := func(tag string) map[string]any {
		obs, _ := cfg["outbounds"].([]any)
		for _, o := range obs {
			if m, ok := o.(map[string]any); ok && m["tag"] == tag {
				return m
			}
		}
		return nil
	}

	ob := outbound("egress-" + itoa(tlsID))
	if ob == nil {
		t.Fatalf("missing TLS egress outbound:\n%s", cfgBytes)
	}
	tlsBlock, _ := ob["tls"].(map[string]any)
	if tlsBlock == nil {
		t.Fatalf("TLS egress emitted without a tls block: %v", ob)
	}
	if tlsBlock["enabled"] != true || tlsBlock["server_name"] != "proxy.example.com" {
		t.Errorf("tls block malformed: %v", tlsBlock)
	}
	if tlsBlock["certificate"] != anchorPEM {
		t.Errorf("pinned trust anchor not inlined: %v", tlsBlock["certificate"])
	}
	if _, ok := tlsBlock["insecure"]; ok {
		t.Errorf("insecure must be absent unless enabled: %v", tlsBlock)
	}

	if ob := outbound("egress-" + itoa(socksID)); ob == nil {
		t.Fatalf("missing socks egress outbound:\n%s", cfgBytes)
	} else if _, ok := ob["tls"]; ok {
		t.Errorf("socks egress must never carry a tls block (sing-box ignores it): %v", ob)
	}
}
