package subconv

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const standaloneClashTemplate = `
x-qingzhou-template-groups: true
proxy-groups:
  - name: 🚀 节点选择
    type: select
    proxies: ["♻️ 自动选择", "all", "DIRECT"]
  - name: ♻️ 自动选择
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    proxies: ["all"]
rules:
  - DOMAIN,example.com,🚀 节点选择
  - MATCH,🚀 节点选择
`

func TestClashTemplateOnlyGroups(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		links := nodeLinks()[:count]
		doc := renderClashDoc(t, standaloneClashTemplate, links...)
		if names := groupNames(doc); strings.Join(names, ",") != "🚀 节点选择,♻️ 自动选择" {
			t.Fatalf("node count %d: groups %v", count, names)
		}
		if _, present := doc["x-qingzhou-template-groups"]; present {
			t.Fatal("panel option leaked into client config")
		}
		for _, group := range mapSlice(doc["proxy-groups"]) {
			members := group["proxies"].([]any)
			if len(members) == 0 {
				t.Fatal("empty proxy group")
			}
			for _, member := range members {
				if member == "all" || member == grpFallbackClash {
					t.Fatalf("invalid member %v", member)
				}
			}
		}
		if rules := doc["rules"].([]any); len(rules) != 2 || rules[1] != "MATCH,🚀 节点选择" {
			t.Fatalf("rules = %v", rules)
		}
	}
}

func TestClashTemplateOnlyProfilesAndAI(t *testing.T) {
	for _, profile := range []RoutingProfile{ProfileLegacy, ProfileCNDirect, ProfileProxyAll} {
		proxies := ParseLinks(nodeLinks())
		proxies[0].AI = true
		output, err := ClashWithProfile(proxies, standaloneClashTemplate, profile)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(output), &doc); err != nil {
			t.Fatal(err)
		}
		rules := doc["rules"].([]any)
		for _, name := range []string{grpSelectClash, grpFixedClash, grpFallbackClash, grpAIClash, "qingzhou-ai"} {
			if strings.Contains(fmt.Sprint(doc), name) {
				t.Fatalf("profile %v references generated policy %s", profile, name)
			}
		}
		if rules[len(rules)-1] != "MATCH,🚀 节点选择" {
			t.Fatalf("catch-all not last: %v", rules)
		}
		if profile == ProfileProxyAll && !strings.Contains(fmt.Sprint(rules), "GEOSITE,CN,🚀 节点选择") {
			t.Fatal("profile did not use the template primary")
		}
	}
}

func TestClashTemplateOnlyFallbackAndNameCollision(t *testing.T) {
	proxies := ParseLinks(nodeLinks())
	proxies[0].Name = "🚀 节点选择"
	template := strings.ReplaceAll(standaloneClashTemplate, "  - MATCH,🚀 节点选择\n", "")
	output, err := Clash(proxies, template)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(output), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(doc), "🚀 节点选择 #2") || !strings.Contains(fmt.Sprint(doc["rules"]), "MATCH,🚀 节点选择") {
		t.Fatal("missing deduplication or catch-all")
	}
}

func TestClashTemplateOnlyRequiresGroups(t *testing.T) {
	for _, template := range []string{
		"x-qingzhou-template-groups: true\n",
		"x-qingzhou-template-groups: true\nproxy-groups: [{name: test}, {name: test}]",
	} {
		if _, err := Clash(ParseLinks(nodeLinks()), template); err == nil {
			t.Fatal("invalid template accepted")
		}
	}
}
