package subconv

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func renderClashDoc(t *testing.T, tpl string, links ...string) map[string]any {
	t.Helper()
	out, err := Clash(ParseLinks(links), tpl)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered YAML does not parse: %v", err)
	}
	return doc
}

func groupNames(doc map[string]any) []string {
	var names []string
	for _, g := range mapSlice(doc["proxy-groups"]) {
		n, _ := g["name"].(string)
		names = append(names, n)
	}
	return names
}

func groupByName(doc map[string]any, name string) map[string]any {
	for _, g := range mapSlice(doc["proxy-groups"]) {
		if n, _ := g["name"].(string); n == name {
			return g
		}
	}
	return nil
}

const twoNodes = 2

func nodeLinks() []string {
	return []string{
		"trojan://pw@a.example.com:443?security=tls&sni=a.example.com#A",
		"trojan://pw@b.example.com:443?security=tls&sni=b.example.com#B",
	}
}

// The built-in template declares no proxy-groups, so the generated set must be
// exactly what it always was.
func TestClashDefaultTemplateGroupsUnchanged(t *testing.T) {
	doc := renderClashDoc(t, "", nodeLinks()...)
	names := groupNames(doc)
	if len(names) != 2 || names[0] != grpSelectClash || names[1] != grpAutoClash {
		t.Errorf("default groups = %v", names)
	}
}

// A template that declares its own groups used to have them silently discarded.
func TestClashTemplateGroupsSurvive(t *testing.T) {
	tpl := `
proxy-groups:
  - name: 我的分组
    type: select
    proxies: ["DIRECT", "♻️ 自动选择"]
rules: []
`
	doc := renderClashDoc(t, tpl, nodeLinks()...)
	names := groupNames(doc)
	if len(names) == 0 || names[0] != "我的分组" {
		t.Fatalf("custom group missing or not first: %v", names)
	}
	// The generated groups are still appended — the MATCH rule points at the
	// primary selector, which must exist.
	if groupByName(doc, grpSelectClash) == nil {
		t.Error("generated primary selector was dropped")
	}
	if groupByName(doc, grpAutoClash) == nil {
		t.Error("generated auto group was dropped")
	}
}

// A template cannot know the per-user node names, so `all` stands in for them.
func TestClashAllPlaceholderExpands(t *testing.T) {
	tpl := `
proxy-groups:
  - name: 全部节点
    type: url-test
    proxies: ["all"]
  - name: 混合
    type: select
    proxies: ["DIRECT", "all", "REJECT"]
rules: []
`
	doc := renderClashDoc(t, tpl, nodeLinks()...)
	g := groupByName(doc, "全部节点")
	proxies, _ := g["proxies"].([]any)
	if len(proxies) != twoNodes {
		t.Fatalf("`all` expanded to %v, want the 2 nodes", proxies)
	}
	for _, p := range proxies {
		if p == allPlaceholder {
			t.Errorf("placeholder survived expansion: %v", proxies)
		}
	}
	// Expansion happens in place, so neighbours keep their positions.
	mixed, _ := groupByName(doc, "混合")["proxies"].([]any)
	if len(mixed) != twoNodes+2 || mixed[0] != "DIRECT" || mixed[len(mixed)-1] != "REJECT" {
		t.Errorf("in-place expansion broke ordering: %v", mixed)
	}
}

// A name collision must resolve in the admin's favour, and must never yield two
// groups sharing one name.
func TestClashCustomGroupWinsOnNameCollision(t *testing.T) {
	tpl := `
proxy-groups:
  - name: ` + grpSelectClash + `
    type: select
    proxies: ["DIRECT", "all"]
rules: []
`
	doc := renderClashDoc(t, tpl, nodeLinks()...)
	n := 0
	for _, name := range groupNames(doc) {
		if name == grpSelectClash {
			n++
		}
	}
	if n != 1 {
		t.Errorf("primary selector appears %d times, want 1", n)
	}
	g := groupByName(doc, grpSelectClash)
	proxies, _ := g["proxies"].([]any)
	if len(proxies) == 0 || proxies[0] != "DIRECT" {
		t.Errorf("admin's definition was overwritten: %v", proxies)
	}
	// The MATCH rule still resolves.
	rules, _ := doc["rules"].([]any)
	last, _ := rules[len(rules)-1].(string)
	if !strings.HasSuffix(last, grpSelectClash) {
		t.Errorf("MATCH rule = %q", last)
	}
}

// mihomo refuses to start on an empty proxy-group, which would take down every
// node rather than just the group.
func TestClashEmptyCustomGroupIsFilled(t *testing.T) {
	tpl := `
proxy-groups:
  - name: 空组
    type: select
    proxies: []
rules: []
`
	doc := renderClashDoc(t, tpl, nodeLinks()...)
	proxies, _ := groupByName(doc, "空组")["proxies"].([]any)
	if len(proxies) != twoNodes {
		t.Errorf("empty group was left empty: %v", proxies)
	}
}
