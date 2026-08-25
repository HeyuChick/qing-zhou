package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

// One physical listener can expose several user-selectable exits. The
// discriminator is the authenticated logical-route identity, not another port
// or inbound tag; both identities still meter the same owning plan bucket.
func TestLogicalNodeRoutesShareOneInbound(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "routeuser")
	pkg := mkPlan(t, st, "多落地", 10, 100, 30)
	buy(t, st, uid, pkg)

	entryID, err := st.SaveSbInbound(&SbInbound{Type: "vless", Tag: "shared-entry", Listen: "::", ListenPort: 8443, Options: `{}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	landingA, err := st.SaveSbInbound(&SbInbound{Type: "vless", Tag: "landing-a", Listen: "::", ListenPort: 9443, Options: `{}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	landingB, err := st.SaveSbInbound(&SbInbound{Type: "vless", Tag: "landing-b", Listen: "::", ListenPort: 10443, Options: `{}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if entryID == landingA || entryID == landingB {
		t.Fatal("test setup reused inbound ids")
	}

	gid, err := st.CreateGroup(NodeGroup{Name: "共享入口"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlanGroups(pkg.ID, []int64{gid}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []Node{
		{Type: "self_built", Name: "入口 → 落地 A", InboundTag: "shared-entry", RouteUpstreamInboundID: landingA, Enabled: true, GroupIDs: []int64{gid}},
		{Type: "self_built", Name: "入口 → 落地 B", InboundTag: "shared-entry", RouteUpstreamInboundID: landingB, Enabled: true, GroupIDs: []int64{gid}},
	} {
		if _, err := st.CreateNode(n); err != nil {
			t.Fatal(err)
		}
	}

	users, err := st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(users["shared-entry"]); got != 2 {
		t.Fatalf("shared entry users = %d, want one derived identity per route", got)
	}
	if users["shared-entry"][0].Name == users["shared-entry"][1].Name || users["shared-entry"][0].UUID == users["shared-entry"][1].UUID {
		t.Fatalf("logical routes did not get distinct identities: %+v", users["shared-entry"])
	}

	u, _ := st.UserByID(uid)
	links := st.BuildSelfBuiltLinks(u, "entry.example.com")
	if len(links) != 2 {
		t.Fatalf("subscription links = %d, want 2 logical nodes", len(links))
	}
	if links[0].Tag != "shared-entry" || links[1].Tag != "shared-entry" || links[0].Link == links[1].Link {
		t.Fatalf("links must share the entry but carry distinct credentials: %+v", links)
	}

	cfg, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "", users)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(cfg, &doc); err != nil {
		t.Fatal(err)
	}
	physical := 0
	for _, raw := range doc["inbounds"].([]any) {
		ib := raw.(map[string]any)
		if ib["tag"] == "shared-entry" {
			physical++
		}
	}
	if physical != 1 {
		t.Fatalf("generated %d physical shared-entry inbounds, want exactly 1", physical)
	}
	routeRules := 0
	route := doc["route"].(map[string]any)
	for _, raw := range route["rules"].([]any) {
		rule, ok := raw.(map[string]any)
		if !ok || !strings.HasPrefix(asString(rule["outbound"]), "relay-to-") {
			continue
		}
		if _, ok := rule["auth_user"]; ok {
			routeRules++
		}
	}
	if routeRules != 2 {
		t.Fatalf("auth_user relay rules = %d, want 2; config=%s", routeRules, cfg)
	}

	applied, err := st.AddUsageBatch(map[string]UsageDelta{
		users["shared-entry"][0].Name: {Up: 11, Down: 13},
		users["shared-entry"][1].Name: {Up: 17, Down: 19},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("canonical usage identities applied = %d, want 1 owning bucket", applied)
	}
	buckets, _ := st.ListBuckets(uid)
	var usedUp, usedDown int64
	for _, b := range buckets {
		if b.Kind == "plan" {
			usedUp += b.UsedUp
			usedDown += b.UsedDown
		}
	}
	if usedUp != 28 || usedDown != 32 {
		t.Fatalf("logical route traffic = %d/%d, want 28/32", usedUp, usedDown)
	}

	disabled, _ := st.GetSbInbound(landingB)
	disabled.Enabled = false
	if _, err := st.SaveSbInbound(disabled); err != nil {
		t.Fatal(err)
	}
	users, err = st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(users["shared-entry"]); got != 1 {
		t.Fatalf("disabled landing left %d route identities active, want only the healthy route", got)
	}
	if got := len(st.BuildSelfBuiltLinks(u, "entry.example.com")); got != 1 {
		t.Fatalf("disabled landing left %d logical links in subscription, want 1", got)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func TestDeletingLogicalRouteLandingMarksNodeBroken(t *testing.T) {
	st := newRefundStore(t)
	if _, err := st.SaveSbInbound(&SbInbound{Type: "vless", Tag: "entry", ListenPort: 8443, Options: `{}`, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	landingID, err := st.SaveSbInbound(&SbInbound{Type: "vless", Tag: "landing", ListenPort: 9443, Options: `{}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	gid, err := st.CreateGroup(NodeGroup{Name: "route group"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.CreateNode(Node{Type: "self_built", Name: "fixed exit", InboundTag: "entry", RouteUpstreamInboundID: landingID, Enabled: true, GroupIDs: []int64{gid}})
	if err != nil {
		t.Fatal(err)
	}
	servers, err := st.DeleteSbInbound(landingID)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0] != 0 {
		t.Fatalf("entry servers to rebuild = %v, want [0]", servers)
	}
	n, err := st.GetNode(nodeID)
	if err != nil || n == nil {
		t.Fatalf("logical node disappeared with its landing: node=%+v err=%v", n, err)
	}
	if n.RouteUpstreamInboundID != 0 || !n.RouteUpstreamBroken {
		t.Fatalf("deleted landing did not leave a visible fallback warning: %+v", n)
	}
	if groups, err := st.SelfBuiltNodeGroupIDs("entry"); err != nil || len(groups) != 0 {
		t.Fatalf("broken logical route became a legacy/default entitlement: groups=%v err=%v", groups, err)
	}
}
