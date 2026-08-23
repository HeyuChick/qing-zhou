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
