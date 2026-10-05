package subconv

import (
	"encoding/base64"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestLegacyClashSerializerFrozenBytesAndNoURIClaims(t *testing.T) {
	raw := `proxies:
- name: same-name
  type: vmess
  server: node.example.test
  port: 443
  uuid: 11111111-2222-4333-8444-555555555555
  alterId: 0
  tls: true
  servername: tls.test
  skip-cert-verify: true
  network: ws
  ws-opts:
    path: /ws
    headers: {Host: cdn.test}
`
	var doc clashDoc
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"add":"node.example.test","aid":"0","allowInsecure":"1","host":"cdn.test","id":"11111111-2222-4333-8444-555555555555","net":"ws","path":"/ws","port":"443","ps":"same-name","sni":"tls.test","tls":"tls","type":"none","v":"2"}`
	want := "vmess://" + base64.StdEncoding.EncodeToString([]byte(wantJSON))
	if got := legacyClashToLink(doc.Proxies[0]); got != want {
		t.Fatalf("old importer bytes changed: %s", got)
	}
	proxies := ParseClashYAML(raw)
	if len(proxies) != 1 || len(proxies[0].SourceLegacyKeys) == 0 || proxies[0].SourceLegacyKeys[0] != NodeKey(want) {
		t.Fatal("missing raw-YAML compatibility proof")
	}
	p, err := ParseLink("trojan://pw@example.test:443?qz-source-legacy-key=" + NodeKey(want))
	if err != nil || len(p.SourceLegacyKeys) != 0 {
		t.Fatal("URI claim became source authority")
	}
}
