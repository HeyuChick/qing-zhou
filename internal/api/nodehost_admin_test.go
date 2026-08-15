package api

import (
	"net"
	"testing"

	"qingzhou/internal/store"
)

func TestHostOnlyStripsSchemePortAndPath(t *testing.T) {
	cases := map[string]string{
		"https://panel.example.com":            "panel.example.com",
		"http://1.2.3.4:8081":                  "1.2.3.4",
		"https://panel.example.com:8443/sub?x": "panel.example.com",
		"http://[2001:db8::1]:8081":            "2001:db8::1",
		"1.2.3.4":                              "1.2.3.4",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

// The field takes a bare host, so a configured 访问地址 has to arrive stripped —
// pasting "https://panel.example.com" into a node address would produce links
// nothing can dial.
func TestConfiguredNodeHostCandidates(t *testing.T) {
	a, st := newNodeUpgradeAPI(t)
	if err := st.SetSetting("public_base", "https://panel.example.com:8443"); err != nil {
		t.Fatal(err)
	}
	// A disabled row must not be offered: it is not what nodeHost() would pick.
	if _, err := st.CreateServer(store.Server{Name: "off", Host: "10.0.0.9", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateServer(store.Server{Name: "hk", Host: "1.2.3.4", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	got := a.configuredNodeHostCandidates()
	if len(got) != 2 {
		t.Fatalf("want 访问地址 + 已启用服务器, got %+v", got)
	}
	if got[0].Source != "public_base" || got[0].Value != "panel.example.com" {
		t.Errorf("public_base candidate = %+v", got[0])
	}
	if got[1].Source != "server" || got[1].Value != "1.2.3.4" {
		t.Errorf("server candidate = %+v", got[1])
	}
}

// Sources overlap constantly (the echo probe and the server row are the same
// machine on a single-node install). Showing one address twice would read as
// two different answers.
func TestDedupeNodeHostCandidatesKeepsMostTrustedSource(t *testing.T) {
	got := dedupeNodeHostCandidates([]nodeHostCandidate{
		{Value: "1.2.3.4", Source: "echo"},
		{Value: "1.2.3.4", Source: "server"},
		{Value: "", Source: "public_base"},
		{Value: "2001:db8::1", Source: "iface"},
	})
	if len(got) != 2 {
		t.Fatalf("want 2 distinct non-empty candidates, got %+v", got)
	}
	if got[0].Source != "echo" {
		t.Errorf("first occurrence wins (highest confidence first): %+v", got[0])
	}
	if got[1].Value != "2001:db8::1" {
		t.Errorf("second candidate = %+v", got[1])
	}
}

// With 访问地址 unset, publicBase() would answer with the request's Host —
// which for an admin on an SSH tunnel is "localhost:8099". Suggesting that as
// the address written into every client's subscription is worse than
// suggesting nothing.
func TestConfiguredNodeHostCandidatesSkipUnreachableBase(t *testing.T) {
	a, st := newNodeUpgradeAPI(t)
	for _, base := range []string{"", "http://localhost:8099", "http://127.0.0.1:8099", "http://192.168.1.10"} {
		if err := st.SetSetting("public_base", base); err != nil {
			t.Fatal(err)
		}
		for _, c := range a.configuredNodeHostCandidates() {
			if c.Source == "public_base" {
				t.Errorf("public_base %q offered as %q", base, c.Value)
			}
		}
	}
}

// A dev box or a VPS with containers has a handful of private NIC addresses
// (docker0, WSL, hypervisor bridges), none of which any client can dial. They
// are only offered when the echo probes came back empty.
func TestLocalInterfaceCandidatesHidePrivateUnlessAsked(t *testing.T) {
	for _, c := range localInterfaceCandidates(false) {
		ip := net.ParseIP(c.Value)
		if ip == nil {
			t.Fatalf("candidate %q is not an IP literal", c.Value)
		}
		if isInternalIP(ip) {
			t.Errorf("private address %s offered without includePrivate", c.Value)
		}
	}
}

// The echo probe must reject anything that is not a usable public address —
// a captive portal or an error page would otherwise be written into every
// subscription link.
func TestFetchEchoIPRejectsNonPublicBodies(t *testing.T) {
	for _, body := range []string{"", "127.0.0.1", "10.0.0.5", "100.64.0.1", "<html>error</html>", "not an ip"} {
		if ip := parseEchoBody([]byte(body)); ip != "" {
			t.Errorf("body %q accepted as %q", body, ip)
		}
	}
	if ip := parseEchoBody([]byte(" 203.0.113.7\n")); ip != "203.0.113.7" {
		t.Errorf("public IP body = %q, want 203.0.113.7", ip)
	}
}
