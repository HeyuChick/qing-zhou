package subconv

import (
	"encoding/base64"
	"strings"
	"testing"
)

// mihomo and sing-box both abort on the WHOLE config when one node is malformed,
// so a single bad entry in an imported airport subscription would otherwise leave
// every user with no working profile. Bad nodes must be dropped at parse time,
// and the good ones around them must survive.
func TestParseLink_RejectsUnusableNodes(t *testing.T) {
	bad := map[string]string{
		"port above uint16":    "vless://u@1.2.3.4:99999?security=tls&sni=a.com#x",
		"port zero":            "vless://u@1.2.3.4:0?security=tls&sni=a.com#x",
		"unknown ss cipher":    "ss://" + b64("foo:bar") + "@1.2.3.4:443#x",
		"empty ss cipher":      "ss://" + b64("nocolonhere") + "@1.2.3.4:443#x",
		"ss port out of range": "ss://" + b64("aes-256-gcm:pw") + "@1.2.3.4:70000#x",
	}
	for name, link := range bad {
		if p, err := ParseLink(link); err == nil {
			t.Errorf("%s: accepted a node that cannot work: %+v", name, p)
		}
	}

	good := []string{
		"vless://u@1.2.3.4:443?security=tls&sni=a.com#ok",
		"ss://" + b64("aes-256-gcm:pw") + "@1.2.3.4:8388#ok",
		"ss://" + b64("2022-blake3-aes-256-gcm:pw") + "@1.2.3.4:8388#ok",
		"trojan://pw@1.2.3.4:443?sni=a.com#ok",
	}
	for _, link := range good {
		if _, err := ParseLink(link); err != nil {
			t.Errorf("rejected a valid node %q: %v", link, err)
		}
	}
}

// One bad node in a list must not take the others with it.
func TestParseList_BadNodeDoesNotPoisonTheRest(t *testing.T) {
	blob := strings.Join([]string{
		"vless://u@1.2.3.4:443?security=tls&sni=a.com#good1",
		"ss://" + b64("bogus-cipher:pw") + "@5.6.7.8:443#bad",
		"vless://u@9.9.9.9:99999?security=tls&sni=b.com#alsobad",
		"trojan://pw@2.2.2.2:443?sni=c.com#good2",
	}, "\n")

	ps := ParseList(blob)
	if len(ps) != 2 {
		t.Fatalf("kept %d nodes, want the 2 good ones: %+v", len(ps), ps)
	}
	sb, err := Singbox(ps, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"good1", "good2"} {
		if !strings.Contains(sb, want) {
			t.Errorf("config lost %s", want)
		}
	}
	if strings.Contains(sb, "bogus-cipher") || strings.Contains(sb, "99999") {
		t.Error("an unusable node reached the generated config")
	}
}

// grpc / httpupgrade / h2 transports used to be dropped silently: the client
// then dialled plain TCP at a server speaking gRPC, so the node never connected
// and nothing in the config said why.
func TestSingbox_NonWebSocketTransports(t *testing.T) {
	cases := map[string]struct{ link, wantType, wantField string }{
		"grpc":        {"vless://u@1.2.3.4:443?security=tls&sni=a.com&type=grpc&serviceName=svc#g", `"type": "grpc"`, `"service_name": "svc"`},
		"httpupgrade": {"vless://u@1.2.3.4:443?security=tls&sni=a.com&type=httpupgrade&path=/up#h", `"type": "httpupgrade"`, `"path": "/up"`},
		"h2":          {"vless://u@1.2.3.4:443?security=tls&sni=a.com&type=h2&path=/p#h2", `"type": "http"`, `"path": "/p"`},
		"ws":          {"vless://u@1.2.3.4:443?security=tls&sni=a.com&type=ws&path=/w#w", `"type": "ws"`, `"path": "/w"`},
		"trojan grpc": {"trojan://pw@1.2.3.4:443?sni=a.com&type=grpc&serviceName=t#tg", `"type": "grpc"`, `"service_name": "t"`},
	}
	for name, c := range cases {
		sb, err := Singbox(ParseLinks([]string{c.link}), "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, want := range []string{c.wantType, c.wantField} {
			if !strings.Contains(sb, want) {
				t.Errorf("%s: config missing %s\n%s", name, want, sb)
			}
		}
	}
}

// An unrecognised transport must yield no transport block rather than being
// passed through — sing-box rejects an unknown transport type by refusing the
// entire config.
func TestSingbox_UnknownTransportOmitted(t *testing.T) {
	sb, err := Singbox(ParseLinks([]string{"vless://u@1.2.3.4:443?security=tls&sni=a.com&type=quicgarbage#x"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb, "quicgarbage") {
		t.Errorf("unknown transport leaked into the config:\n%s", sb)
	}
	if !strings.Contains(sb, `"type": "vless"`) {
		t.Error("the node itself should still be emitted, just without a transport")
	}
}

// b64 is the userinfo encoding shadowsocks share links use.
func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
