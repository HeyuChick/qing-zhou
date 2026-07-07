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
// sane DNS configuration: fallback resolvers span multiple vendors,
// fake-ip-filter covers common edge cases, and fallback-filter ipcidr catches
// polluted responses.
func TestDefaultClashTemplateDNS(t *testing.T) {
	var doc struct {
		DNS struct {
			FakeIPFilter []string `yaml:"fake-ip-filter"`
			Fallback     []string `yaml:"fallback"`
			FallbackFilter struct {
				GeoIP     bool     `yaml:"geoip"`
				GeoIPCode string   `yaml:"geoip-code"`
				IPCIDR    []string `yaml:"ipcidr"`
			} `yaml:"fallback-filter"`
		} `yaml:"dns"`
	}
	if err := yaml.Unmarshal([]byte(DefaultClashTemplate), &doc); err != nil {
		t.Fatalf("parse DefaultClashTemplate: %v", err)
	}

	// --- fallback DNS: must not be dominated by a single vendor ---
	cf, google := 0, 0
	for _, s := range doc.DNS.Fallback {
		if strings.Contains(s, "cloudflare") || strings.Contains(s, "1.1.1.1") {
			cf++
		}
		if strings.Contains(s, "google") || strings.Contains(s, "8.8.8.8") {
			google++
		}
	}
	if cf > 2 {
		t.Errorf("fallback has %d Cloudflare entries (should be ≤2 to avoid single-vendor risk): %v", cf, doc.DNS.Fallback)
	}
	if google == 0 {
		t.Errorf("fallback has no Google DNS entries — missing vendor diversity: %v", doc.DNS.Fallback)
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

	// --- fallback-filter.ipcidr: must catch common polluted ranges ---
	mustCIDR := []string{"240.0.0.0/4", "0.0.0.0/32", "127.0.0.0/8", "198.18.0.0/15"}
	for _, cidr := range mustCIDR {
		if !contains(doc.DNS.FallbackFilter.IPCIDR, cidr) {
			t.Errorf("fallback-filter.ipcidr missing %q", cidr)
		}
	}
}
