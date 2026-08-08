package api

import (
	"strconv"
	"time"

	"qingzhou/internal/store"
)

// Node topology for the user-facing 节点列表: which machine a node enters on,
// which machines it is relayed through, and where it finally exits. The admin
// 链路拓扑 tab derives the same picture client-side from the raw inbound /
// server / egress tables, but a user may not read those, so it is assembled
// here and only names cross the wire.
//
// Deliberately no hosts or ports: the entry address is already in the user's own
// share link, but a landing machine's address is not, and a relayed node exists
// precisely so that address stays unpublished. Names (and the optional location
// label) are what make the path legible, and they leak nothing dialable.

// nodeHop is one segment of a node's path. Kind is entry | relay | egress.
type nodeHop struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// nodeTopo is a node's full path plus a warning for a chain that no longer
// resolves.
type nodeTopo struct {
	Hops []nodeHop `json:"hops"`
	// Warn is "" (intact), "landing" (a relay's landing inbound is gone) or
	// "egress" (the configured proxy egress is gone). Both mean traffic now
	// leaves from the previous hop's IP instead of where it was meant to —
	// worth surfacing, since it silently changes the node's exit address.
	Warn string `json:"warn,omitempty"`
}

// topoIndex holds the lookups a batch of nodes needs, read once per request
// rather than per node.
type topoIndex struct {
	byTag     map[string]*store.SbInbound
	byID      map[int64]*store.SbInbound
	servers   map[int64]*store.Server
	egresses  map[int64]*store.SbEgress
	localName string
}

func (a *API) newTopoIndex() *topoIndex {
	ix := &topoIndex{
		byTag:    map[string]*store.SbInbound{},
		byID:     map[int64]*store.SbInbound{},
		servers:  map[int64]*store.Server{},
		egresses: map[int64]*store.SbEgress{},
		// server_id 0 is the panel's own machine. The admin page calls it 「本机」,
		// which means nothing to a subscriber looking at a route — from their side
		// it is simply the machine they dial first.
		localName: "主机",
	}
	if ibs, err := a.st.ListSbInbounds(); err == nil {
		for _, ib := range ibs {
			ix.byTag[ib.Tag] = ib
			ix.byID[ib.ID] = ib
		}
	}
	if svs, err := a.st.ListServers(); err == nil {
		for _, sv := range svs {
			ix.servers[sv.ID] = sv
		}
	}
	if egs, err := a.st.ListSbEgresses(); err == nil {
		for _, e := range egs {
			ix.egresses[e.ID] = e
		}
	}
	return ix
}

func (ix *topoIndex) serverName(id int64) (name, location string) {
	if id == 0 {
		return ix.localName, ""
	}
	if sv := ix.servers[id]; sv != nil {
		name = sv.Name
		location = sv.Location
	}
	if name == "" {
		name = "服务器 #" + strconv.FormatInt(id, 10)
	}
	return name, location
}

// topoFor walks a self-built node's upstream chain to its exit. Returns nil for
// an external node (imported share link — we host nothing along its path and
// know nothing about it beyond the link itself).
func (ix *topoIndex) topoFor(tag string) *nodeTopo {
	ib := ix.byTag[tag]
	if ib == nil {
		return nil
	}
	name, loc := ix.serverName(ib.ServerID)
	t := &nodeTopo{Hops: []nodeHop{{Kind: "entry", Name: name, Location: loc, Protocol: ib.Type}}}

	// Same walk as the admin topology's chainOf, including its cycle guard: a
	// chain that loops would otherwise hang this request.
	seen := map[int64]bool{ib.ID: true}
	cur := ib
	for cur.UpstreamInboundID != 0 {
		next := ix.byID[cur.UpstreamInboundID]
		if next == nil || seen[next.ID] {
			t.Warn = "landing"
			return t
		}
		seen[next.ID] = true
		n, l := ix.serverName(next.ServerID)
		t.Hops = append(t.Hops, nodeHop{Kind: "relay", Name: n, Location: l, Protocol: next.Type})
		cur = next
	}
	if cur.EgressID != 0 {
		if eg := ix.egresses[cur.EgressID]; eg != nil {
			t.Hops = append(t.Hops, nodeHop{Kind: "egress", Name: eg.Name, Protocol: eg.Type})
		} else {
			t.Warn = "egress"
		}
	}
	// UpstreamBroken survives a landing being deleted out from under a relay:
	// the stored upstream_inbound_id was cleared, so the walk above sees a clean
	// direct exit and would report the downgrade as normal.
	if t.Warn == "" && ib.UpstreamBroken {
		t.Warn = "landing"
	}
	return t
}

// planRef identifies one of the user's plans for grouping purposes.
type planRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// planGrants resolves which of the user's 套餐 grant each node, so the 节点列表
// can be sectioned the way a subscriber thinks about it: "这份套餐给了我哪些线路".
//
// This is ACCESS, not billing, and the two genuinely differ. Access comes from a
// plan's node groups (same rule as AccessibleGroupIDs). Billing comes from
// UserOwnedInbounds, which hands every reachable node to the single
// highest-priority bucket — a signup grant covering the union of the user's plan
// groups owns literally everything until it is used up. Grouping by that would
// collapse every section into one and then reshuffle the moment a bucket ran
// out, which is not what the user asked to see.
//
// A node reachable through two plans is returned under both; that is the honest
// answer, not a bug to dedupe away.
func (a *API) planGrants(u *store.User) func(nodeEntry) []planRef {
	now := time.Now().Unix()
	pkgNames, _ := a.st.PackageNames()
	freeGroup, _ := a.st.GetSettingInt64("free_group_id", 0)
	groupCount, _ := a.st.GroupCount()

	// Same precedence as buildPlanViews: the live package name wins over the
	// name frozen into the bucket at purchase time, so a renamed package reads
	// the same here and on the 我的套餐 card.
	label := func(b *store.Bucket) string {
		if b.PackageID > 0 {
			if live, ok := pkgNames[b.PackageID]; ok && live != "" {
				return live
			}
		}
		if b.Name != "" {
			return b.Name
		}
		return "套餐 #" + strconv.FormatInt(b.ID, 10)
	}

	buckets, _ := a.st.ListBuckets(u.ID)

	// No node groups configured at all — nothing to attribute by, so fall back to
	// the billing owner, the only plan identity that exists in that setup.
	if groupCount == 0 {
		owners, _ := a.st.UserGroupOwners(u.ID, now)
		var all []planRef
		if b := owners[0]; b != nil {
			all = []planRef{{ID: b.ID, Name: label(b)}}
		}
		return func(nodeEntry) []planRef { return all }
	}

	// group id → the plans granting it. Mirrors AccessibleGroupIDs' predicate
	// exactly (plan bucket, not expired) so a section can never claim a node the
	// access check itself would refuse, or miss one it allows.
	byGroup := map[int64][]planRef{}
	for _, b := range buckets {
		if b.Kind != "plan" || !b.NotExpired(now) {
			continue
		}
		gids, err := a.st.PlanGroupIDs(b.PackageID)
		if err != nil {
			continue
		}
		ref := planRef{ID: b.ID, Name: label(b)}
		for _, g := range gids {
			byGroup[g] = append(byGroup[g], ref)
		}
	}

	return func(e nodeEntry) []planRef {
		if refs := byGroup[e.GroupID]; len(refs) > 0 {
			return refs
		}
		// Reachable but granted by no plan: the free group, which every account
		// gets regardless of what it has bought.
		if freeGroup > 0 && e.GroupID == freeGroup {
			return []planRef{{ID: 0, Name: "免费线路"}}
		}
		return nil
	}
}
