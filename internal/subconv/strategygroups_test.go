package subconv

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestCrossClientMediaAndAIRouting(t *testing.T) {
	links := []string{
		"vless://11111111-1111-1111-1111-111111111111@1.1.1.1:443?security=reality#BWG-AI",
		"hysteria2://pw@2.2.2.2:443?security=tls&sni=www.ucla.edu#LL-AI",
		"trojan://pw@cdn.example.com:443?security=tls&sni=cdn.example.com#CDN-Media",
		"trojan://pw@la.example.com:443?security=tls&sni=la.example.com#LA-Backup",
	}
	ps := ParseLinks(links)
	ps[0].AI = true
	ps[1].AI = true

	t.Run("clash", func(t *testing.T) {
		out, err := Clash(ps, `rules: ["GEOSITE,cn,DIRECT"]`)
		if err != nil {
			t.Fatal(err)
		}
		doc := map[string]any{}
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		media := groupByName(doc, grpMediaClash)
		mediaAuto := groupByName(doc, grpMediaAutoClash)
		if media == nil || mediaAuto == nil || media["type"] != "select" || mediaAuto["type"] != "fallback" {
			t.Fatalf("missing clash media groups: media=%v mediaAuto=%v", media, mediaAuto)
		}
		autoProxies, _ := mediaAuto["proxies"].([]any)
		if len(autoProxies) != 4 || autoProxies[0] != "CDN-Media" || autoProxies[1] != "LA-Backup" || autoProxies[2] != "BWG-AI" {
			t.Errorf("unexpected media auto fallback order: %v", autoProxies)
		}
		rules, _ := doc["rules"].([]any)
		if len(rules) != 4 ||
			rules[0] != "RULE-SET,qingzhou-ai,"+grpAIClash ||
			rules[1] != "GEOSITE,cn,DIRECT" ||
			rules[2] != "RULE-SET,qingzhou-media,"+grpMediaClash ||
			rules[3] != "MATCH,"+grpSelectClash {
			t.Errorf("unexpected clash rules order: %v", rules)
		}
		providers, _ := doc["rule-providers"].(map[string]any)
		for k, wantURL := range map[string]string{"qingzhou-ai": clashAIRuleURL, "qingzhou-media": clashMediaRuleURL} {
			p, _ := providers[k].(map[string]any)
			if p == nil || p["url"] != wantURL || p["proxy"] != "DIRECT" {
				t.Errorf("provider %s mismatch: %v", k, p)
			}
		}
	})

	t.Run("singbox", func(t *testing.T) {
		out, err := SingboxWithProfile(ps, "", ProfileCNDirect)
		if err != nil {
			t.Fatal(err)
		}
		doc := map[string]any{}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		media := singboxOutboundByTag(doc, tagMedia)
		mediaAuto := singboxOutboundByTag(doc, tagMediaAuto)
		if media == nil || mediaAuto == nil || mediaAuto["type"] != "urltest" {
			t.Fatalf("missing sing-box media outbounds: media=%v mediaAuto=%v", media, mediaAuto)
		}
		if refs := stringSlice(mediaAuto["outbounds"]); len(refs) != 2 || refs[0] != "CDN-Media" || refs[1] != "LA-Backup" {
			t.Errorf("media-auto urltest must contain strictly non-AI nodes, got %v", refs)
		}
		route, _ := doc["route"].(map[string]any)
		rules := mapSlice(route["rules"])
		idxAI, idxCN, idxMedia := -1, -1, -1
		for i, r := range rules {
			if r["rule_set"] == "qingzhou-ai" && r["outbound"] == tagAI {
				idxAI = i
			}
			if r["rule_set"] == "geosite-cn" && r["outbound"] == "direct" {
				idxCN = i
			}
			if r["rule_set"] == "qingzhou-media" && r["outbound"] == tagMedia {
				idxMedia = i
			}
		}
		if idxAI < 0 || idxCN < idxAI || idxMedia < idxCN {
			t.Errorf("singbox rule ordering wrong: ai=%d cn=%d media=%d", idxAI, idxCN, idxMedia)
		}
		for _, rs := range mapSlice(route["rule_set"]) {
			if rs["tag"] == "qingzhou-ai" || rs["tag"] == "qingzhou-media" {
				if rs["download_detour"] != "direct" || !strings.Contains(rs["url"].(string), "connect.heyuchick.com/ruleset/") {
					t.Errorf("ruleset %v not using direct mirror", rs)
				}
			}
		}
	})

	t.Run("surge", func(t *testing.T) {
		out := Surge(ps, "")
		if !strings.Contains(out, grpMediaClash+" = select, "+grpMediaAutoClash+", "+grpSelectClash+", CDN-Media, LA-Backup") {
			t.Errorf("surge media select missing:\n%s", out)
		}
		if !strings.Contains(out, grpMediaAutoClash+" = fallback, CDN-Media, LA-Backup, LL-AI,") {
			t.Errorf("surge media auto fallback missing:\n%s", out)
		}
		iAI := strings.Index(out, "RULE-SET,"+surgeAIRuleURL+","+grpAIClash)
		iCN := strings.Index(out, "GEOIP,CN,DIRECT")
		iMedia := strings.Index(out, "RULE-SET,"+surgeMediaRuleURL+","+grpMediaClash)
		iFinal := strings.Index(out, "FINAL,"+grpSelectClash)
		if !(iAI >= 0 && iCN > iAI && iMedia > iCN && iFinal > iMedia) {
			t.Errorf("surge rule ordering wrong (%d, %d, %d, %d):\n%s", iAI, iCN, iMedia, iFinal, out)
		}
	})

	t.Run("no-ai-no-media-split", func(t *testing.T) {
		plain := ParseLinks(links)
		cOut, err := Clash(plain, "")
		if err != nil {
			t.Fatal(err)
		}
		sbOut, err := Singbox(plain, "")
		if err != nil {
			t.Fatal(err)
		}
		sgOut := Surge(plain, "")
		if strings.Contains(cOut, grpMediaClash) || strings.Contains(sbOut, `"qingzhou-media"`) || strings.Contains(sgOut, grpMediaClash) {
			t.Fatal("media group/rule emitted even when no AI nodes exist")
		}
	})
}
