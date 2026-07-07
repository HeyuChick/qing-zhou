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
