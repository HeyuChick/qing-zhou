package subconv

import (
	"strings"
	"testing"
)

func TestRenderMarksAIByExactAccessibleLink(t *testing.T) {
	links := nodeLinks()
	body, _, err := Render(FormatClash, links, map[string]bool{links[1]: true}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "RULE-SET,qingzhou-ai,"+grpAIClash) {
		t.Fatal("AI marker did not reach the native renderer")
	}
}

func TestSurgeAIRoutePrecedesDomesticBypassAndIsNotManual(t *testing.T) {
	ps := ParseLinks(nodeLinks())
	ps[0].AI = true
	out := Surge(ps, "")
	primary := grpSelectClash + " = select, " + grpFixedClash + ", " + grpFallbackClash + ", DIRECT"
	if !strings.Contains(out, primary) {
		t.Errorf("top-level selector =\n%s", out)
	}
	if strings.Contains(out, grpSelectClash+" = select, "+grpAIClash) {
		t.Fatal("AI guard became a manual top-level choice")
	}
	aiRule := "RULE-SET," + surgeAIRuleURL + "," + grpAIClash
	if ai := strings.Index(out, aiRule); ai < 0 {
		t.Fatal("AI route missing")
	} else if cn := strings.Index(out, "GEOIP,CN,DIRECT"); cn < 0 || ai > cn {
		t.Fatal("AI route does not precede the domestic bypass")
	}
	if !strings.Contains(out, grpAIClash+" = fallback, A, B") {
		t.Fatal("AI policy does not prefer AI nodes before ordinary fallback")
	}
}

func TestNoAIMarkerProducesNoAIRouteOrGroup(t *testing.T) {
	clash := renderClashDoc(t, "", nodeLinks()...)
	if groupByName(clash, grpAIClash) != nil {
		t.Fatal("Clash emitted an empty AI group")
	}
	if providers, _ := clash["rule-providers"].(map[string]any); providers["qingzhou-ai"] != nil {
		t.Fatal("Clash emitted an unused AI rule provider")
	}
	if strings.Contains(Surge(ParseLinks(nodeLinks()), ""), "qingzhou-ai") {
		t.Fatal("Surge emitted an unused AI rule")
	}
}


// 品牌定制守护：Surge 广告拦截双库叠加——Loyalsoldier DOMAIN-SET 之后必须紧跟
// anti-AD RULE-SET（10万+中文区广告域名），且先于 GEOIP,CN 与 FINAL。
func TestSurgeDualAdBlock(t *testing.T) {
	out := Surge(ParseLinks(nodeLinks()), "")
	loyal := strings.Index(out, "DOMAIN-SET,https://raw.githubusercontent.com/Loyalsoldier/surge-rules/release/reject.txt,REJECT")
	anti := strings.Index(out, "RULE-SET,https://anti-ad.net/surge.txt,REJECT")
	if loyal < 0 || anti < 0 {
		t.Fatalf("dual ad-block rules missing (loyal=%d anti=%d)", loyal, anti)
	}
	if anti < loyal {
		t.Fatal("anti-AD should follow the Loyalsoldier domain-set")
	}
	if cn := strings.Index(out, "GEOIP,CN,DIRECT"); cn >= 0 && (cn < loyal || cn < anti) {
		t.Fatal("ad-block rules must precede the domestic bypass")
	}
}


// 品牌定制守护：带 mport（端口跳跃）的 hy2 节点在 Surge 导出里必须用跳跃段
// 首端口作固定端口——基础端口可能被运营商 UDP QoS，无跳跃能力的 Surge/Egern
// 客户端打基础端口必死（2026-10-30 CN→BWH 443 被掐事故）。
func TestSurgeHy2HopPortsUseFirstPortOfRange(t *testing.T) {
	out := Surge(ParseLinks([]string{
		"hysteria2://pass@203.0.113.9:443?security=tls&insecure=1&sni=h.example.com&fp=chrome&mport=20000-50000#BWH-hy2",
	}), "")
	i := strings.Index(out, "BWH-hy2 = hysteria2")
	if i < 0 {
		t.Fatalf("hy2 proxy line missing: %s", out)
	}
	rest := out[i:]
	if j := strings.IndexByte(rest, 10); j > 0 {
		rest = rest[:j]
	}
	if !strings.Contains(rest, "203.0.113.9, 20000,") {
		t.Errorf("hop-ports hy2 must advertise the first port of the range, got: %s", rest)
	}
	if strings.Contains(rest, ", 443,") {
		t.Errorf("must not fall back to the QoS-prone base port: %s", rest)
	}
}

