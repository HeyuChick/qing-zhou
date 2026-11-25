package subconv

import "fmt"

// Built-in policy names. The top-level selector is the stable target used by
// routing rules. Fixed keeps the user's explicit node choice; fallback follows
// node order and changes only when the current node is unavailable; AI contains
// only nodes that belong to an admin-marked AI group.
const (
	grpSelectClash    = "✈️ 节点选择"
	grpFixedClash     = "◎ 固定节点"
	grpFallbackClash  = "⚡ 故障转移"
	grpAIClash        = "★ AI 节点"
	grpMediaClash     = "🎬 视频与大流量"
	grpMediaAutoClash = "🎬 大流量自动"
	legacyAutoClash   = "♻️ 自动选择"

	tagProxy     = "proxy" // sing-box route.final points here
	tagFixed     = "fixed"
	tagFallback  = "fallback"
	tagAI        = "ai"
	tagMedia     = "media"
	tagMediaAuto = "media-auto"
)

const urltestURL = "http://www.gstatic.com/generate_204"

const (
	clashAIRuleURL      = "https://connect.heyuchick.com/ruleset/ai-clash.mrs"
	clashMediaRuleURL   = "https://connect.heyuchick.com/ruleset/media-clash.txt"
	singboxAIRuleURL    = "https://connect.heyuchick.com/ruleset/ai-singbox.srs"
	singboxMediaRuleURL = "https://connect.heyuchick.com/ruleset/media-singbox.srs"
	surgeAIRuleURL      = "https://connect.heyuchick.com/ruleset/ai-surge.list"
	surgeMediaRuleURL   = "https://connect.heyuchick.com/ruleset/media-surge.list"
)

type strategyGroups struct {
	all   []string
	ai    []string
	nonAI []string
}

func buildStrategyGroups(ps []*Proxy) strategyGroups {
	var groups strategyGroups
	for _, p := range ps {
		groups.all = append(groups.all, p.Name)
		if p.AI {
			groups.ai = append(groups.ai, p.Name)
		} else {
			groups.nonAI = append(groups.nonAI, p.Name)
		}
	}
	return groups
}

// hasMediaSplit reports whether both AI-marked and non-AI nodes exist so video
// and large-download traffic can be kept off metered/premium AI nodes.
func (g strategyGroups) hasMediaSplit() bool {
	return len(g.ai) > 0 && len(g.nonAI) > 0
}

// aiFallbackOrder prefers explicitly AI-capable nodes, then retains every
// other accessible node as a last-resort proxy. It never adds DIRECT: the guard
// exists specifically to prevent AI traffic from leaking onto a direct route.
func (g strategyGroups) aiFallbackOrder() []string {
	out := append([]string(nil), g.ai...)
	ai := make(map[string]bool, len(g.ai))
	for _, name := range g.ai {
		ai[name] = true
	}
	for _, name := range g.all {
		if !ai[name] {
			out = append(out, name)
		}
	}
	return out
}

// nonAIFallbackOrder prefers non-AI nodes first and keeps AI nodes only as a
// last-resort safety net when every non-AI node fails health checks.
func (g strategyGroups) nonAIFallbackOrder() []string {
	out := append([]string(nil), g.nonAI...)
	out = append(out, g.ai...)
	return out
}

// reservedTags contains every generated policy/outbound name. A share-link
// fragment may be attacker-controlled, so a colliding node is renamed instead
// of making the whole client configuration invalid or self-referential.
func reservedTags() map[string]bool {
	return map[string]bool{
		tagProxy: true, tagFixed: true, tagFallback: true, tagAI: true, tagMedia: true, tagMediaAuto: true,
		grpSelectClash: true, grpFixedClash: true, grpFallbackClash: true, grpAIClash: true, grpMediaClash: true, grpMediaAutoClash: true,
		legacyAutoClash: true,
		"direct":        true, "DIRECT": true, "GLOBAL": true, "REJECT": true, "PASS": true,
	}
}

func dedupeNames(ps []*Proxy) {
	dedupeNamesWithReserved(ps, nil)
}

// dedupeNamesWithReserved additionally protects template-defined selector tags.
func dedupeNamesWithReserved(ps []*Proxy, extra map[string]bool) {
	seen := reservedTags()
	for name := range extra {
		seen[name] = true
	}
	for _, p := range ps {
		n := p.Name
		if n == "" {
			n = p.Server
		}
		if n == "" {
			n = p.Protocol
		}
		p.Name = uniqueName(n, seen)
		seen[p.Name] = true
	}
}

func uniqueName(base string, used map[string]bool) string {
	if base == "" {
		base = "节点"
	}
	if !used[base] {
		return base
	}
	for k := 2; ; k++ {
		cand := fmt.Sprintf("%s #%d", base, k)
		if !used[cand] {
			return cand
		}
	}
}
