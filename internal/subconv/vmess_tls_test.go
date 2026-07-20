package subconv

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// vmessJSON decodes the base64 JSON payload of a vmess:// link.
func vmessJSON(t *testing.T, link string) map[string]any {
	t.Helper()
	if !strings.HasPrefix(link, "vmess://") {
		t.Fatalf("not a vmess link: %q", link)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("vmess payload is not base64: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("vmess payload is not JSON: %v", err)
	}
	return m
}

// sbOutboundTLS returns the tls object of the first outbound of the given type
// in a rendered sing-box config, or nil if that outbound has none. Assertions go
// through this rather than substring-matching the whole document, which silently
// picks up unrelated keys from the template.
func sbOutboundTLS(t *testing.T, cfg, outboundType string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(cfg), &doc); err != nil {
		t.Fatalf("rendered sing-box config is not valid JSON: %v", err)
	}
	for _, ob := range mapSlice(doc["outbounds"]) {
		if ob["type"] != outboundType {
			continue
		}
		tls, _ := ob["tls"].(map[string]any)
		return tls
	}
	t.Fatalf("no %q outbound in rendered config", outboundType)
	return nil
}

// The regression this whole file exists for: TLS used to be inferred from a
// non-empty SNI. server_name is optional — self-signed certs and bare-IP inbounds
// routinely leave it empty — so a TLS inbound rendered as tls:"" and the node
// was dialled in plaintext. Every renderer keys off this one field, so the
// damage was not limited to the base64/v2rayN path.
func TestVmessTLSWithoutSNIIsStillTLS(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vmess", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u",
		TLS: true, // inbound has a cert…
		SNI: "",   // …but no server_name
	})
	if got := vmessJSON(t, link)["tls"]; got != "tls" {
		t.Fatalf(`vmess "tls" = %q, want "tls" — TLS inbound rendered as plaintext`, got)
	}
}

func TestVmessWithoutTLSStaysPlaintext(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vmess", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u",
		TLS: false,
	})
	if got := vmessJSON(t, link)["tls"]; got != "" {
		t.Errorf(`vmess "tls" = %q, want "" for a non-TLS inbound`, got)
	}
}

// A stray SNI on a non-TLS inbound must not resurrect the old inference.
func TestVmessSNIAloneDoesNotImplyTLS(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vmess", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u",
		TLS: false, SNI: "a.com",
	})
	if got := vmessJSON(t, link)["tls"]; got != "" {
		t.Errorf(`vmess "tls" = %q, want "" — SNI must not imply TLS`, got)
	}
}

// The no-SNI TLS node has to survive into all four subscription formats, since
// each renderer independently tests VMess["tls"] == "tls".
func TestVmessTLSWithoutSNIReachesEveryFormat(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vmess", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u",
		TLS: true, SNI: "", Insecure: true,
	})
	proxies := ParseLinks([]string{link})
	if len(proxies) != 1 {
		t.Fatalf("link did not survive parsing: %q", link)
	}

	clash, err := Clash(proxies, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tls: true", "skip-cert-verify: true"} {
		if !strings.Contains(clash, want) {
			t.Errorf("clash output missing %q:\n%s", want, clash)
		}
	}

	sb, err := Singbox(proxies, "")
	if err != nil {
		t.Fatal(err)
	}
	// Anchored to the vmess outbound's own tls object. A bare
	// strings.Contains(sb, `"enabled": true`) is a tautology: the default
	// template already renders that twice (dns.fakeip and cache_file), so the
	// assertion passes even with the vmess TLS block deleted entirely.
	tls := sbOutboundTLS(t, sb, "vmess")
	if tls == nil {
		t.Fatal("vmess outbound has no tls block — TLS inbound rendered as plaintext")
	}
	if tls["enabled"] != true {
		t.Errorf("vmess tls.enabled = %v, want true", tls["enabled"])
	}
	if tls["insecure"] != true {
		t.Errorf("vmess tls.insecure = %v, want true", tls["insecure"])
	}

	surge := Surge(proxies, "")
	for _, want := range []string{"tls=true", "skip-cert-verify=true"} {
		if !strings.Contains(surge, want) {
			t.Errorf("surge output missing %q:\n%s", want, surge)
		}
	}
}

// vmess was the only protocol dropping these, so a self-signed or
// fingerprint-pinned vmess node could not be expressed at all.
func TestVmessCarriesFingerprintALPNAndInsecure(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vmess", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u",
		TLS: true, SNI: "a.com", Fingerprint: "firefox",
		ALPN: "h2,http/1.1", Insecure: true,
	})
	m := vmessJSON(t, link)
	for k, want := range map[string]any{
		"sni":           "a.com",
		"fp":            "firefox",
		"alpn":          "h2,http/1.1",
		"allowInsecure": "1",
	} {
		if m[k] != want {
			t.Errorf("vmess %q = %v, want %v", k, m[k], want)
		}
	}

	sb, err := Singbox(ParseLinks([]string{link}), "")
	if err != nil {
		t.Fatal(err)
	}
	tls := sbOutboundTLS(t, sb, "vmess")
	if tls == nil {
		t.Fatal("vmess outbound has no tls block")
	}
	if tls["server_name"] != "a.com" {
		t.Errorf("tls.server_name = %v, want a.com", tls["server_name"])
	}
	if tls["insecure"] != true {
		t.Errorf("tls.insecure = %v, want true", tls["insecure"])
	}
	if utls, _ := tls["utls"].(map[string]any); utls == nil || utls["fingerprint"] != "firefox" {
		t.Errorf("tls.utls = %v, want fingerprint firefox", tls["utls"])
	}
	alpn, _ := stringList(tls["alpn"])
	if len(alpn) != 2 || alpn[0] != "h2" || alpn[1] != "http/1.1" {
		t.Errorf("tls.alpn = %v, want [h2 http/1.1]", alpn)
	}

	clash, err := Clash(ParseLinks([]string{link}), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"client-fingerprint: firefox", "skip-cert-verify: true"} {
		if !strings.Contains(clash, want) {
			t.Errorf("clash vmess TLS missing %q:\n%s", want, clash)
		}
	}
}

// A non-TLS vmess node must not pick up TLS keys from the shared sbTLS helper.
func TestVmessPlaintextHasNoTLSBlock(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vmess", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u", TLS: false,
	})
	sb, err := Singbox(ParseLinks([]string{link}), "")
	if err != nil {
		t.Fatal(err)
	}
	// Scoped to the vmess outbound. A document-wide search for `"tls"` would
	// start failing for an unrelated reason the day the template grows one.
	if tls := sbOutboundTLS(t, sb, "vmess"); tls != nil {
		t.Errorf("plaintext vmess emitted a tls block: %v", tls)
	}
	clash, err := Clash(ParseLinks([]string{link}), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clash, "tls: true") {
		t.Errorf("plaintext vmess emitted tls: true:\n%s", clash)
	}
}

// Foreign panels routinely emit allowInsecure as a JSON boolean rather than the
// string "1" 轻舟 writes. str() renders that as "true", so an == "1" comparison
// dropped the exemption and left the imported node failing cert verification.
func TestImportedVmessInsecureAcceptsBothValueForms(t *testing.T) {
	for _, form := range []string{`true`, `"true"`, `"1"`, `1`} {
		payload := `{"v":"2","ps":"n","add":"1.2.3.4","port":"443","id":"u",` +
			`"aid":"0","net":"tcp","type":"none","tls":"tls","allowInsecure":` + form + `}`
		link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload))
		proxies := ParseLinks([]string{link})
		if len(proxies) != 1 {
			t.Fatalf("allowInsecure=%s: link did not parse", form)
		}
		if !proxies[0].tlsInsecure() {
			t.Errorf("allowInsecure=%s: tlsInsecure() = false, want true", form)
		}
		clash, err := Clash(proxies, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(clash, "skip-cert-verify: true") {
			t.Errorf("allowInsecure=%s: clash dropped skip-cert-verify", form)
		}
	}
}

// A Clash subscription carrying a self-signed vmess node must keep the exemption
// across import. The parse side used to drop it, so no render-side fix could
// bring it back on re-export.
func TestClashVmessImportKeepsSkipCertVerify(t *testing.T) {
	yaml := `proxies:
  - {name: v, type: vmess, server: 1.2.3.4, port: 443, uuid: u, alterId: 0, cipher: auto, tls: true, servername: a.com, skip-cert-verify: true, client-fingerprint: firefox, alpn: [h2]}
`
	proxies := ParseList(yaml)
	if len(proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(proxies))
	}
	if !proxies[0].tlsInsecure() {
		t.Error("skip-cert-verify lost on import")
	}
	if got := proxies[0].tlsParam("fp"); got != "firefox" {
		t.Errorf("client-fingerprint lost on import: %q", got)
	}
	if got := proxies[0].tlsParam("alpn"); got != "h2" {
		t.Errorf("alpn lost on import: %q", got)
	}
}

func TestFormatAliasesForV2Ray(t *testing.T) {
	for _, in := range []string{"v2ray", "V2RayN", "v2rayng", "base64", "url", "links", "wat"} {
		if got := NormalizeFormat(in); got != FormatBase64 {
			t.Errorf("NormalizeFormat(%q) = %q, want %q", in, got, FormatBase64)
		}
	}
	// The named formats must not be swallowed by the new aliases.
	for in, want := range map[string]string{
		"clash": FormatClash, "mihomo": FormatClash,
		"singbox": FormatSingbox, "sing-box": FormatSingbox,
		"surge": FormatSurge,
	} {
		if got := NormalizeFormat(in); got != want {
			t.Errorf("NormalizeFormat(%q) = %q, want %q", in, got, want)
		}
	}
}
