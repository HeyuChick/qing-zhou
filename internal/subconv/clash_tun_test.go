package subconv

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestClashTunExclude verifies the anti-loop injection is correct for both
// IP-based and domain-based nodes. The regression it guards: a bare domain
// leaking into tun.route-exclude-address produces a non-CIDR entry that makes
// mihomo fail to load the config, killing TUN ("virtual NIC on → no network").
func TestClashTunExclude(t *testing.T) {
	links := []string{
		// IP-based node: belongs in route-exclude-address.
		"vless://11111111-1111-1111-1111-111111111111@38.246.228.54:2000?security=tls&sni=a.com#ip-node",
		// Domain-based node: must NOT appear in route-exclude-address (not a CIDR),
		// must appear in dns.fake-ip-filter instead.
		"vless://22222222-2222-2222-2222-222222222222@us.example.com:443?security=reality&pbk=x&sni=b.com#domain-node",
	}
	out, err := Clash(ParseLinks(links), "")
	if err != nil {
		t.Fatalf("Clash render: %v", err)
	}

	var doc struct {
		DNS struct {
			FakeIPFilter []string `yaml:"fake-ip-filter"`
		} `yaml:"dns"`
		Tun struct {
			RouteExclude []string `yaml:"route-exclude-address"`
		} `yaml:"tun"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("re-parse rendered config: %v\n%s", err, out)
	}

	if !contains(doc.Tun.RouteExclude, "38.246.228.54/32") {
		t.Errorf("IP node missing from route-exclude-address: %v", doc.Tun.RouteExclude)
	}
	for _, e := range doc.Tun.RouteExclude {
		if strings.Contains(e, "example.com") {
			t.Errorf("domain leaked into route-exclude-address (breaks mihomo): %q", e)
		}
		if !strings.Contains(e, "/") {
			t.Errorf("non-CIDR entry in route-exclude-address: %q", e)
		}
	}
	if !contains(doc.DNS.FakeIPFilter, "us.example.com") {
		t.Errorf("domain node missing from fake-ip-filter: %v", doc.DNS.FakeIPFilter)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestDefaultClashTemplateDNS verifies the built-in anti-leak template has a
// sane DNS configuration: resolvers are encrypted, span multiple vendors, are
// all foreign (the template has no CN bypass, so a domestic resolver would be
// dialed through a foreign node), and fake-ip-filter covers common edge cases.
func TestDefaultClashTemplateDNS(t *testing.T) {
	var doc struct {
		DNS struct {
			RespectRules  bool     `yaml:"respect-rules"`
			Nameserver    []string `yaml:"nameserver"`
			FakeIPFilter  []string `yaml:"fake-ip-filter"`
			ProxyServerNS []string `yaml:"proxy-server-nameserver"`
		} `yaml:"dns"`
	}
	if err := yaml.Unmarshal([]byte(DefaultClashTemplate), &doc); err != nil {
		t.Fatalf("parse DefaultClashTemplate: %v", err)
	}

	// --- nameserver: DoH/DoT only. mihomo races every entry concurrently, so a
	// plain UDP:53 entry here leaks every queried domain in cleartext on the
	// local network instead of through the (encrypted, tunneled) proxy. ---
	for _, s := range doc.DNS.Nameserver {
		if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "tls://") {
			t.Errorf("dns.nameserver has a plaintext entry %q — leaks queried domains in cleartext", s)
		}
	}

	// --- respect-rules: DNS module's own connections must follow the same
	// GEOSITE/GEOIP/MATCH table as regular traffic, or foreign fallback
	// lookups dial out the real interface regardless of nameserver being DoH —
	// this is what DNS-leak-test sites actually detect (egress ASN, not
	// encryption). Requires proxy-server-nameserver to avoid a resolution loop. ---
	if !doc.DNS.RespectRules {
		t.Error("dns.respect-rules must be true, or fallback DNS dials off-tunnel and leaks the real network's ASN")
	}
	if len(doc.DNS.ProxyServerNS) == 0 {
		t.Error("dns.proxy-server-nameserver must be set when respect-rules is true (avoids a resolution loop)")
	}

	// --- nameserver: must not be dominated by a single vendor. Vendor
	// diversity used to live in the fallback list; with no CN bypass there is
	// no domestic/foreign split left to justify a separate fallback block, so
	// the requirement moved here. ---
	cf, google := 0, 0
	for _, s := range doc.DNS.Nameserver {
		if strings.Contains(s, "cloudflare") || strings.Contains(s, "1.1.1.1") {
			cf++
		}
		if strings.Contains(s, "google") || strings.Contains(s, "8.8.8.8") {
			google++
		}
	}
	if cf > 2 {
		t.Errorf("nameserver has %d Cloudflare entries (should be ≤2 to avoid single-vendor risk): %v", cf, doc.DNS.Nameserver)
	}
	if google == 0 {
		t.Errorf("nameserver has no Google DNS entries — missing vendor diversity: %v", doc.DNS.Nameserver)
	}

	// --- no domestic resolver: with no CN bypass in the rules, a CN DoH entry
	// would be dialed through a foreign node — slower, and it hands the full
	// query stream to a domestic provider for no benefit. ---
	for _, s := range doc.DNS.Nameserver {
		for _, cn := range []string{"doh.pub", "alidns", "119.29.29.29", "223.5.5.5"} {
			if strings.Contains(s, cn) {
				t.Errorf("dns.nameserver has domestic resolver %q — pointless without a CN bypass rule", s)
			}
		}
	}

	// --- fake-ip-filter: essential entries must be present ---
	mustHave := []string{
		"*.lan",
		"*.local",
		"+.msftconnecttest.com",
		"+.stun.*.*",
		"+.ntp.org.cn",
		"+.srv.nintendo.net",
		"+.stun.playstation.net",
		"+.xboxlive.com",
		"localhost.ptlogin2.qq.com",
	}
	for _, want := range mustHave {
		if !contains(doc.DNS.FakeIPFilter, want) {
			t.Errorf("fake-ip-filter missing %q", want)
		}
	}

}

// TestDefaultClashTemplateNoCNBypass verifies the template routes domestic
// traffic through the proxy like everything else. The "private" rules are a
// LAN carve-out, not a geographic one, and are expected to stay; a CN rule is
// what this guards against, because re-adding one silently sends every
// domestic domain (and its DNS) out the real interface again.
func TestDefaultClashTemplateNoCNBypass(t *testing.T) {
	var doc struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(DefaultClashTemplate), &doc); err != nil {
		t.Fatalf("parse DefaultClashTemplate: %v", err)
	}
	for _, r := range doc.Rules {
		upper := strings.ToUpper(r)
		if strings.HasPrefix(upper, "GEOSITE,CN,") || strings.HasPrefix(upper, "GEOIP,CN,") {
			t.Errorf("rule %q reintroduces a CN bypass — template is meant to have no geographic split", r)
		}
	}
}

// TestDefaultClashTemplateNoResolve verifies the CN geoip rule can't force a
// fresh local DNS resolution for foreign fake-ip'd domains. Regression it
// guards: a bare "GEOIP,CN,DIRECT" (no "no-resolve") makes mihomo resolve
// every domain not already caught by GEOSITE,cn just to evaluate this rule —
// and mihomo has an open bug (MetaCubeX/mihomo#2971) where respect-rules
// doesn't reliably tunnel that resolution, so it leaks out the real network
// instead of the proxy.
func TestDefaultClashTemplateNoResolve(t *testing.T) {
	var doc struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(DefaultClashTemplate), &doc); err != nil {
		t.Fatalf("parse DefaultClashTemplate: %v", err)
	}
	for _, r := range doc.Rules {
		if strings.HasPrefix(r, "GEOIP,") && !strings.HasSuffix(r, ",no-resolve") {
			t.Errorf("GEOIP rule %q forces a fresh local DNS resolution — must end in \",no-resolve\"", r)
		}
	}
}

// TestDefaultClashTemplateStrictRoute verifies tun.strict-route is on.
// Regression it guards: Windows races DNS queries across every active
// adapter ("smart multi-homed name resolution") including the physical NIC;
// dns-hijack alone doesn't stop that, only strict-route adds the firewall
// rules that do (clash-verge-rev#3133). Without it, Windows clients leak
// straight to the ISP's resolver even with a fully correct dns: block.
func TestDefaultClashTemplateStrictRoute(t *testing.T) {
	var doc struct {
		Tun struct {
			StrictRoute bool `yaml:"strict-route"`
		} `yaml:"tun"`
	}
	if err := yaml.Unmarshal([]byte(DefaultClashTemplate), &doc); err != nil {
		t.Fatalf("parse DefaultClashTemplate: %v", err)
	}
	if !doc.Tun.StrictRoute {
		t.Error("tun.strict-route must be true, or Windows clients leak DNS via the physical adapter")
	}
}
