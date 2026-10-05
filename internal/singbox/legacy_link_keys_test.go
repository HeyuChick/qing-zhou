package singbox

import (
	"encoding/base64"
	"testing"
)

// Frozen pre-change serializer bytes, not the new parser/canonicalizer.
func TestLegacyShareLinkMatchesFrozenSerializer(t *testing.T) {
	p := LinkParams{Type: "vmess", Host: "example.test", Port: 443, UUID: "11111111-2222-4333-8444-555555555555", Tag: "old", TLS: true, SNI: "tls.test", ALPN: "http/1.1", Insecure: true, Network: "ws", WSHost: "cdn.test", Path: "/ws?q=a%2Fb", WSMaxEarlyData: 2048, WSEarlyDataHeader: "Sec-WebSocket-Protocol", WSHeaders: map[string][]string{"X-Test": {"extra"}}}
	want := `{"add":"example.test","aid":"0","allowInsecure":"1","alpn":"http/1.1","fp":"chrome","host":"cdn.test","id":"11111111-2222-4333-8444-555555555555","net":"ws","path":"/ws?q=a%2Fb","port":"443","ps":"old","scy":"auto","sni":"tls.test","tls":"tls","type":"none","v":"2"}`
	if got := LegacyShareLinkForNodeKey(p); got != "vmess://"+base64.StdEncoding.EncodeToString([]byte(want)) {
		t.Fatalf("old VMess bytes changed: %s", got)
	}
	p.Type = "trojan"
	p.Password = "pw+ space"
	p.TLSDisabled = true
	wantURL := "trojan://pw%2B+space@example.test:443?type=ws&path=%2Fws%3Fq%3Da%252Fb&host=cdn.test&max_early_data=2048&early_data_header_name=Sec-WebSocket-Protocol&security=tls&fp=chrome&sni=tls.test&allowInsecure=1#old"
	if got := LegacyShareLinkForNodeKey(p); got != wantURL {
		t.Fatalf("old Trojan bytes changed:\n%s\nwant %s", got, wantURL)
	}
}
