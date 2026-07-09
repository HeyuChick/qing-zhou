package subconv

import "fmt"

// Outbound-group naming. These are referenced by both the Clash and sing-box
// renderers and by the default templates' routing rules, so they must stay in
// sync. The primary group is what routing rules ultimately point traffic at.
// Note: group names use BMP symbols (✈️ ♻️ ⚡), not supplementary-plane emoji
// like 🚀/🔒 — yaml.v3 escapes the latter to \U0001F680, which is ugly and may
// trip non-mihomo Clash parsers.
const (
	grpSelectClash = "✈️ 节点选择" // Clash primary selector (rules MATCH this)
	grpAutoClash   = "♻️ 自动选择" // Clash global url-test (best IP + protocol)
	tagProxy       = "proxy"    // sing-box primary selector (route.final points here)
	tagAuto        = "auto"     // sing-box global url-test
)

const urltestURL = "http://www.gstatic.com/generate_204"

// nodeGroup is a per-panel-group auto-select (url-test) group. Its name is the
// admin's own node-group name (reused verbatim), so the client shows the same
// groups the admin organised in the panel instead of a raw server IP.
type nodeGroup struct {
	name  string   // panel node-group name (displayed)
	names []string // node names in this group, in order
}

// autoGroups is the analysis of a node set used to build url-test groups.
type autoGroups struct {
	all     []string    // every node name, unique, in order
	byGroup []nodeGroup // panel groups worth their own auto-select group
}

// dedupeNames mutates each proxy's Name so the whole set is unique. Url-test and
// selector groups reference nodes by name/tag, so duplicates would silently drop
// or alias nodes. Collisions get a " #N" suffix; empties fall back to server.
func dedupeNames(ps []*Proxy) {
	seen := map[string]bool{}
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

// uniqueName returns base if unused, else the first free " #k" (k≥2) suffix. The
// suffixed candidate is re-checked against used, so it can't collide with a node
// that is literally already named "X #2".
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

// buildAutoGroups analyses an already-deduped proxy set: collects all names and
// the per-panel-group subsets. A group becomes its own auto-select group when it
// has ≥2 nodes and isn't the whole set (which the global "auto" already covers).
func buildAutoGroups(ps []*Proxy) autoGroups {
	var ag autoGroups
	order := []string{}
	by := map[string][]string{}
	for _, p := range ps {
		ag.all = append(ag.all, p.Name)
		if p.Group == "" {
			continue // ungrouped nodes live only in the global auto group
		}
		if _, ok := by[p.Group]; !ok {
			order = append(order, p.Group)
		}
		by[p.Group] = append(by[p.Group], p.Name)
	}
	// A panel group name is reused verbatim as a proxy-group / url-test tag, so it
	// must not collide with a reserved selector tag, DIRECT, or a node name — else
	// the client sees two different things under one tag (duplicate/omitted proxy
	// or a self-referencing selector). Suffix any colliding group name.
	used := map[string]bool{
		tagProxy: true, tagAuto: true, grpSelectClash: true, grpAutoClash: true,
		"direct": true, "DIRECT": true, "GLOBAL": true,
	}
	for _, n := range ag.all {
		used[n] = true
	}
	for _, g := range order {
		if len(by[g]) >= 2 && len(by[g]) < len(ag.all) {
			name := uniqueName(g, used)
			used[name] = true
			ag.byGroup = append(ag.byGroup, nodeGroup{name: name, names: by[g]})
		}
	}
	return ag
}
