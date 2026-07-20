package subconv

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"qingzhou/internal/singbox"
)

// renderedClashProxy renders one link and returns its Clash proxy map.
func renderedClashProxy(t *testing.T, link string) map[string]any {
	t.Helper()
	out, err := Clash(ParseLinks([]string{link}), "")
	if err != nil {
		t.Fatalf("Clash: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered Clash config is not valid YAML: %v", err)
	}
	list, _ := doc["proxies"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 proxy, got %d — node was dropped: %s", len(list), link)
	}
	m, _ := list[0].(map[string]any)
	return m
}

// renderedSbOutbound returns the first outbound of the given type from a rendered config.
func renderedSbOutbound(t *testing.T, link, typ string) map[string]any {
	t.Helper()
	out, err := Singbox(ParseLinks([]string{link}), "")
	if err != nil {
		t.Fatalf("Singbox: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered sing-box config is not valid JSON: %v", err)
	}
	for _, ob := range mapSlice(doc["outbounds"]) {
		if ob["type"] == typ {
			return ob
		}
	}
	t.Fatalf("no %q outbound — node was dropped: %s", typ, link)
	return nil
}

// Both protocols used to be generated as share links but had no parse branch,
// so ParseLinks dropped them: they appeared only in the base64 subscription
// (which v2rayN cannot use for either protocol) and vanished from clash,
// sing-box and Surge. They were unusable everywhere.
func TestAnytlsAndHysteriaReachAllRenderers(t *testing.T) {
	anytls := singbox.BuildShareLink(singbox.LinkParams{
		Type: "anytls", Tag: "a", Host: "1.2.3.4", Port: 443,
		Password: "pw", TLS: true, SNI: "a.com",
	})
	hy1 := singbox.BuildShareLink(singbox.LinkParams{
		Type: "hysteria", Tag: "h", Host: "5.6.7.8", Port: 443,
		Password: "authsecret", TLS: true, SNI: "b.com", UpMbps: 50, DownMbps: 200,
	})

	for _, c := range []struct{ name, link string }{{"anytls", anytls}, {"hysteria", hy1}} {
		if got := ParseLinks([]string{c.link}); len(got) != 1 {
			t.Fatalf("%s: link not parsed back: %q", c.name, c.link)
		}
	}

	if got := renderedClashProxy(t, anytls)["type"]; got != "anytls" {
		t.Errorf("clash anytls type = %v", got)
	}
	if got := renderedClashProxy(t, hy1)["type"]; got != "hysteria" {
		t.Errorf("clash hysteria type = %v", got)
	}
	if got := renderedSbOutbound(t, anytls, "anytls")["password"]; got != "pw" {
		t.Errorf("sing-box anytls password = %v", got)
	}
	if got := renderedSbOutbound(t, hy1, "hysteria")["auth_str"]; got != "authsecret" {
		t.Errorf("sing-box hysteria auth_str = %v", got)
	}

	// Surge supports anytls but has no hysteria v1 policy, so v1 must stay skipped.
	if surge := Surge(ParseLinks([]string{anytls}), ""); !strings.Contains(surge, "anytls, 1.2.3.4, 443") {
		t.Errorf("surge missing anytls line:\n%s", surge)
	}
	if surge := Surge(ParseLinks([]string{hy1}), ""); strings.Contains(surge, "hysteria") {
		t.Errorf("surge emitted hysteria v1, which it cannot express:\n%s", surge)
	}
}

// mihomo's HysteriaOption declares up/down WITHOUT omitempty, so a node missing
// them makes the decoder report "has unset fields: down, up" and reject the
// ENTIRE config — every node gone, not just this one. They must always be
// present, even when the link carries no bandwidth.
func TestClashHysteriaAlwaysCarriesBandwidth(t *testing.T) {
	noBandwidth := singbox.BuildShareLink(singbox.LinkParams{
		Type: "hysteria", Tag: "h", Host: "5.6.7.8", Port: 443,
		Password: "auth", TLS: true, SNI: "b.com", // UpMbps/DownMbps deliberately 0
	})
	m := renderedClashProxy(t, noBandwidth)
	for _, k := range []string{"up", "down"} {
		v, _ := m[k].(string)
		if v == "" {
			t.Errorf("clash hysteria %q is absent — this fails mihomo's whole config", k)
		}
	}

	withBandwidth := renderedClashProxy(t, singbox.BuildShareLink(singbox.LinkParams{
		Type: "hysteria", Tag: "h", Host: "5.6.7.8", Port: 443,
		Password: "auth", TLS: true, SNI: "b.com", UpMbps: 50, DownMbps: 200,
	}))
	if withBandwidth["up"] != "50 Mbps" {
		t.Errorf("up = %v, want \"50 Mbps\"", withBandwidth["up"])
	}
	if withBandwidth["down"] != "200 Mbps" {
		t.Errorf("down = %v, want \"200 Mbps\"", withBandwidth["down"])
	}
}

// The obfs naming is inverted between the two schemas and swapping them yields
// a node that looks configured and silently fails to connect:
//
//	URI:    obfs = MODE ("xplus"),  obfsParam = PASSWORD
//	mihomo: obfs = PASSWORD,        obfs-protocol = MODE
func TestHysteriaObfsNamingNotSwapped(t *testing.T) {
	link := "hysteria://1.2.3.4:443?auth=a&peer=x.com&upmbps=50&downmbps=200" +
		"&obfs=xplus&obfsParam=secretpw#h"
	m := renderedClashProxy(t, link)
	if got := m["obfs"]; got != "secretpw" {
		t.Errorf("clash obfs = %v, want the PASSWORD secretpw", got)
	}
	if got := m["obfs-protocol"]; got != "xplus" {
		t.Errorf("clash obfs-protocol = %v, want the MODE xplus", got)
	}
	// sing-box has no field for the mode; its obfs is the XPlus password.
	if got := renderedSbOutbound(t, link, "hysteria")["obfs"]; got != "secretpw" {
		t.Errorf("sing-box obfs = %v, want the PASSWORD secretpw", got)
	}
}

// sing-box's hysteria outbound constructor fails with ErrTLSRequired when tls is
// missing or disabled, and that happens at startup — taking the whole config.
func TestSingboxHysteriaAndAnytlsAlwaysHaveTLS(t *testing.T) {
	for _, c := range []struct{ typ, link string }{
		{"hysteria", "hysteria://1.2.3.4:443?auth=a&peer=x.com&upmbps=50&downmbps=200#h"},
		{"anytls", "anytls://pw@1.2.3.4:443?sni=x.com#a"},
	} {
		tls, _ := renderedSbOutbound(t, c.link, c.typ)["tls"].(map[string]any)
		if tls == nil {
			t.Errorf("%s: no tls block", c.typ)
			continue
		}
		if tls["enabled"] != true {
			t.Errorf("%s: tls.enabled = %v, want true", c.typ, tls["enabled"])
		}
	}
}

// hysteria v1 spells SNI "peer"; normalising it at parse time is what lets every
// renderer and sbTLS find it without special-casing this protocol.
func TestHysteriaPeerNormalisedToSNI(t *testing.T) {
	link := "hysteria://1.2.3.4:443?auth=a&peer=x.com&upmbps=50&downmbps=200#h"
	if got := renderedClashProxy(t, link)["sni"]; got != "x.com" {
		t.Errorf("clash sni = %v, want x.com", got)
	}
	tls, _ := renderedSbOutbound(t, link, "hysteria")["tls"].(map[string]any)
	if tls["server_name"] != "x.com" {
		t.Errorf("sing-box server_name = %v, want x.com", tls["server_name"])
	}
}

// mihomo's AnyTLSOption.Password has no omitempty either, so a passwordless
// anytls entry would fail the whole config. Drop the node instead.
func TestAnytlsWithoutPasswordDropped(t *testing.T) {
	if got := ParseLinks([]string{"anytls://@1.2.3.4:443?sni=x.com#a"}); len(got) != 0 {
		t.Errorf("passwordless anytls survived: %+v", got[0])
	}
	if got := ParseLinks([]string{"anytls://pw@1.2.3.4:443?sni=x.com#a"}); len(got) != 1 {
		t.Error("anytls with a password was dropped")
	}
}

// Clash YAML import must round-trip both protocols, including the bandwidth
// forms mihomo accepts ("100", "100 Mbps", and the separate up-speed field).
func TestClashImportOfAnytlsAndHysteria(t *testing.T) {
	yaml := `proxies:
  - {name: a, type: anytls, server: 1.2.3.4, port: 443, password: pw, sni: a.com, skip-cert-verify: true}
  - {name: h1, type: hysteria, server: 5.6.7.8, port: 443, auth-str: s, up: "50 Mbps", down: "200 Mbps", sni: b.com}
  - {name: h2, type: hysteria, server: 5.6.7.9, port: 443, auth-str: s, up: 30, down: 60, sni: b.com}
`
	byName := map[string]*Proxy{}
	for _, p := range ParseList(yaml) {
		byName[p.Name] = p
	}
	for _, n := range []string{"a", "h1", "h2"} {
		if byName[n] == nil {
			t.Fatalf("node %q dropped on import", n)
		}
	}
	if byName["a"].Protocol != "anytls" || byName["a"].Password != "pw" {
		t.Errorf("anytls imported wrong: %+v", byName["a"])
	}
	if !byName["a"].tlsInsecure() {
		t.Error("anytls skip-cert-verify lost on import")
	}
	if got := byName["h1"].param("upmbps"); got != "50" {
		t.Errorf(`h1 upmbps = %q, want 50 (from "50 Mbps")`, got)
	}
	if got := byName["h2"].param("downmbps"); got != "60" {
		t.Errorf("h2 downmbps = %q, want 60 (from bare 60)", got)
	}
	if got := byName["h1"].param("auth"); got != "s" {
		t.Errorf("h1 auth = %q, want s", got)
	}
}
