package subconv

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"qingzhou/internal/singbox"
)

func wsLinkFixture(protocol string) singbox.LinkParams {
	return singbox.LinkParams{
		Type: protocol, Tag: "public-ws-" + protocol, Host: "203.0.113.10", Port: 443,
		UUID: "11111111-2222-4333-8444-555555555555", Password: "pw+ space:@/?#&", TLS: true,
		SNI: "tls.example.test", ALPN: "http/1.1", Fingerprint: "chrome", Network: "ws",
		Path: "/ws%2Fpath?token=a%2Bb&literal=%252F&ed=application-value", WSHost: "cdn.example.test",
		WSHeaders:      map[string][]string{"host": {"cdn.example.test"}, "X-Token": {"a+b=1"}},
		WSMaxEarlyData: 2048, WSEarlyDataHeader: "Sec-WebSocket-Protocol",
	}
}

func TestPublicWebSocketRoundTripAllProtocols(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		for _, insecure := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/verified", true: "/explicit-insecure"}[insecure], func(t *testing.T) {
				params := wsLinkFixture(protocol)
				params.Insecure = insecure
				link := singbox.BuildShareLink(params)
				p, err := ParseLink(link)
				if err != nil {
					t.Fatal(err)
				}
				if protocol == "trojan" {
					if p.Password != params.Password {
						t.Fatalf("password changed: %q", p.Password)
					}
				} else if p.UUID != params.UUID {
					t.Fatal("UUID changed")
				}
				assertPublicWS(t, p, params)
				proxy := clashProxy(p, ClashOptions{})
				if proxy == nil || proxy["network"] != "ws" {
					t.Fatal("lost WS transport")
				}
				ws := proxy["ws-opts"].(map[string]any)
				if ws["path"] != params.Path || ws["max-early-data"] != 2048 || ws["early-data-header-name"] != params.WSEarlyDataHeader {
					t.Fatalf("clash WS=%v", ws)
				}
				if proxy["skip-cert-verify"] == true != insecure {
					t.Fatalf("verification changed: %v", proxy)
				}
				round := clashToLink(proxy)
				q, err := ParseLink(round)
				if err != nil {
					t.Fatal(err)
				}
				assertPublicWS(t, q, params)
				body, err := Clash([]*Proxy{p}, "")
				if err != nil {
					t.Fatal(err)
				}
				imported := ParseClashYAML(body)
				if len(imported) != 1 {
					t.Fatalf("clash import count=%d", len(imported))
				}
				assertPublicWS(t, imported[0], params)
			})
		}
	}
}

func assertPublicWS(t *testing.T, p *Proxy, want singbox.LinkParams) {
	t.Helper()
	out := singboxOutbound(p)
	ws, ok := out["transport"].(map[string]any)
	if !ok {
		t.Fatal("transport missing")
	}
	if ws["type"] != "ws" || ws["path"] != want.Path || ws["max_early_data"] != want.WSMaxEarlyData || ws["early_data_header_name"] != want.WSEarlyDataHeader {
		t.Fatalf("WS transport changed: %v", ws)
	}
	headers := ws["headers"].(map[string]any)
	if headers["Host"] != want.WSHost || headers["X-Token"] != "a+b=1" {
		t.Fatalf("headers lost: %v", headers)
	}
	tls := out["tls"].(map[string]any)
	if tls["enabled"] != true || tls["server_name"] != want.SNI || !reflect.DeepEqual(tls["alpn"], []string{"http/1.1"}) || (tls["insecure"] == true) != want.Insecure {
		t.Fatalf("TLS changed: %v", tls)
	}
}

func TestPublicWebSocketMultiHeaderAndSurgeBoundaries(t *testing.T) {
	params := wsLinkFixture("vmess")
	params.WSHeaders["X-List"] = []string{"one", "two"}
	p, err := ParseLink(singbox.BuildShareLink(params))
	if err != nil {
		t.Fatal(err)
	}
	out := singboxOutbound(p)
	headers := out["transport"].(map[string]any)["headers"].(map[string]any)
	if !reflect.DeepEqual(headers["X-List"], []string{"one", "two"}) {
		t.Fatal("header list flattened")
	}
	good, _ := ParseLink("trojan://good@203.0.113.20:443#safe-node")
	clash, err := Clash([]*Proxy{p, good}, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err = yaml.Unmarshal([]byte(clash), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Proxies) != 1 || !strings.Contains(clash, "omitted 1") || strings.Contains(clash, "X-List") {
		t.Fatal("unsupported list not clearly omitted")
	}
	surge := Surge([]*Proxy{p, good}, "")
	if !strings.Contains(surge, "omitted 1") || strings.Contains(surge, "ws=true") {
		t.Fatal("Surge unsupported list silently flattened")
	}
	for _, protocol := range []string{"vmess", "trojan"} {
		params = wsLinkFixture(protocol)
		params.Password = "safe-secret"
		p, err = ParseLink(singbox.BuildShareLink(params))
		if err != nil {
			t.Fatal(err)
		}
		out := Surge([]*Proxy{p}, "")
		for _, want := range []string{"ws=true", "ws-path=" + params.Path, "ws-headers=Host:cdn.example.test|X-Token:a+b=1", `alpn="http/1.1"`, "optional early data is not supported"} {
			if !strings.Contains(out, want) {
				t.Fatalf("Surge lost %q: %s", want, out)
			}
		}
		if strings.Contains(out, "skip-cert-verify=true") {
			t.Fatal("Surge relaxed verification")
		}
		params.Path = "/bad, ws=false"
		bad, _ := ParseLink(singbox.BuildShareLink(params))
		out = Surge([]*Proxy{bad}, "")
		if !strings.Contains(out, "omitted 1") || strings.Contains(out, "ws=false") {
			t.Fatal("Surge path injected a directive")
		}
	}
}

func TestPublicWebSocketLegacyLinksAndInvalidHeaders(t *testing.T) {
	for _, link := range []string{
		"vless://u@example.test:443?type=ws&security=tls&host=cdn.test&path=%2Fws%3Fq%3Da%252Fb&max_early_data=2560&early_data_header_name=Sec-WebSocket-Protocol",
		"trojan://pw@example.test:443?type=ws&path=%2Fws%3Fq%3Da%252Fb&ed=2560&eh=Sec-WebSocket-Protocol",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","add":"example.test","port":"443","id":"u","aid":"0","net":"ws","host":"cdn.test","path":"/ws?q=a%2Fb","tls":"tls","max_early_data":2560,"early_data_header_name":"Sec-WebSocket-Protocol"}`)),
	} {
		p, err := ParseLink(link)
		if err != nil {
			t.Fatal(err)
		}
		w, err := p.websocket()
		if err != nil || w.path != "/ws?q=a%2Fb" || w.maxEarlyData != 2560 {
			t.Fatalf("legacy WS changed: %+v %v", w, err)
		}
	}
	for _, headers := range []string{`{"X-Test":"safe\r\nInjected: yes"}`, `{"Host":["a","b"]}`, `{"Host":"a","host":"b"}`, `{"X-Test":42}`, `{"Bad Header":"a"}`} {
		link := "vless://u@example.test:443?type=ws&qz-ws-headers=" + url.QueryEscape(headers)
		if _, err := ParseLink(link); err == nil {
			t.Fatalf("invalid required headers accepted: %s", headers)
		}
	}
}

func TestPublicWebSocketPlaintextIsExplicit(t *testing.T) {
	for _, protocol := range []string{"vless", "trojan"} {
		legacy := wsLinkFixture(protocol)
		legacy.TLS = false
		p, err := ParseLink(singbox.BuildShareLink(legacy))
		if err != nil {
			t.Fatal(err)
		}
		if p.param("security") != "tls" {
			t.Fatal("legacy caller default changed")
		}
		plain := legacy
		plain.TLSDisabled = true
		p, err = ParseLink(singbox.BuildShareLink(plain))
		if err != nil {
			t.Fatal(err)
		}
		out := singboxOutbound(p)
		if tls, ok := out["tls"].(map[string]any); ok && tls["enabled"] != false {
			t.Fatal("plaintext changed to TLS")
		}
		if protocol == "trojan" {
			if clashProxy(p, ClashOptions{}) != nil || surgeProxy(p) != "" {
				t.Fatal("unsupported plaintext Trojan misrepresented")
			}
		}
	}
}

func TestPublicWebSocketSingboxConfigCheck(t *testing.T) {
	bin := os.Getenv("QZ_SINGBOX_TEST_BIN")
	if bin == "" {
		t.Skip("set QZ_SINGBOX_TEST_BIN")
	}
	outbounds := []any{}
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		params := wsLinkFixture(protocol)
		params.WSHeaders["X-List"] = []string{"one", "two"}
		p, err := ParseLink(singbox.BuildShareLink(params))
		if err != nil {
			t.Fatal(err)
		}
		outbounds = append(outbounds, singboxOutbound(p))
	}
	raw, err := json.Marshal(map[string]any{"outbounds": outbounds})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "public-ws.json")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := exec.Command(bin, "check", "-c", file).CombinedOutput()
	if err != nil {
		t.Fatalf("candidate core rejected public WS config: %v\n%s", err, result)
	}
}

func TestPublicWebSocketMihomoConfigCheck(t *testing.T) {
	bin := os.Getenv("QZ_MIHOMO_TEST_BIN")
	if bin == "" {
		t.Skip("set QZ_MIHOMO_TEST_BIN")
	}
	proxies := []map[string]any{}
	names := []string{}
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		p, err := ParseLink(singbox.BuildShareLink(wsLinkFixture(protocol)))
		if err != nil {
			t.Fatal(err)
		}
		proxies = append(proxies, clashProxy(p, ClashOptions{}))
		names = append(names, p.Name)
	}
	raw, err := yaml.Marshal(map[string]any{"proxies": proxies, "proxy-groups": []any{map[string]any{"name": "WS-Check", "type": "select", "proxies": names}}, "rules": []string{"MATCH,WS-Check"}})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "ws-mihomo.yaml")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := exec.Command(bin, "-t", "-f", file, "-d", t.TempDir()).CombinedOutput()
	if err != nil {
		t.Fatalf("mihomo rejected public WS config: %v\n%s", err, result)
	}
}

func TestPublicWebSocketHostOnlyCompatibilityAndUnsafeFields(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		p := wsLinkFixture(protocol)
		p.WSHeaders = nil
		without := singbox.BuildShareLink(p)
		p.WSHeaders = map[string][]string{"host": {p.WSHost}}
		with := singbox.BuildShareLink(p)
		if NodeKey(without) != NodeKey(with) {
			t.Fatal("Host-only normalization changed node identity")
		}
		p.WSEarlyDataHeader = "X-Bad\r\nHeader"
		if singbox.BuildShareLink(p) != "" {
			t.Fatal("invalid ED header exported")
		}
	}
	if _, err := ParseLink("vless://u@example.test:443?type=ws&host=bad%0D%0AX-Evil%3Ayes"); err == nil {
		t.Fatal("unsafe standard Host accepted")
	}
}
