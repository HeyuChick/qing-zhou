package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

type userMeteringFixture struct {
	st       *Store
	servers  []int64
	inbounds []int64
	tags     []string
	owners   []int64
	pkg      *Package
}

func newUserMeteringFixture(t *testing.T, hops int) userMeteringFixture {
	t.Helper()
	f := userMeteringFixture{st: newRefundStore(t), servers: make([]int64, hops), inbounds: make([]int64, hops), tags: make([]string, hops)}
	f.st.SetSecretKey([]byte("user-metering-state-fixture"))
	for i := 0; i < hops; i++ {
		var err error
		f.servers[i], err = f.st.CreateServer(Server{Name: fmt.Sprintf("hop-%d", i), Host: fmt.Sprintf("192.0.2.%d", i+10), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		f.tags[i] = fmt.Sprintf("user-stage-%d", i)
	}
	for i := hops - 1; i >= 0; i-- {
		var upstream int64
		if i+1 < hops {
			upstream = f.inbounds[i+1]
		}
		var err error
		f.inbounds[i], err = f.st.SaveSbInbound(&SbInbound{ServerID: f.servers[i], Type: "vless", Tag: f.tags[i], ListenPort: 2443, Options: `{}`, Enabled: true, UpstreamInboundID: upstream})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.pkg = mkPlan(t, f.st, "metering-staged", 10, 10, 30)
	bindPlanToInbound(t, f.st, f.pkg.ID, f.tags[0])
	for i := 0; i < 2; i++ {
		uid := mkUser(t, f.st, fmt.Sprintf("stage-owner-%d", i))
		buy(t, f.st, uid, f.pkg)
		f.owners = append(f.owners, uid)
	}
	if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	if err := f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f userMeteringFixture) config(t *testing.T, hop int) ([]byte, error) {
	t.Helper()
	users, err := f.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		return nil, err
	}
	return f.st.BuildSingboxConfigForServer(f.servers[hop], singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
}

func (f userMeteringFixture) apply(t *testing.T, hop int) []byte {
	t.Helper()
	raw, err := f.config(t, hop)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[hop], raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRelayMeteringUsersThreeHopStagingAndNoop(t *testing.T) {
	f := newUserMeteringFixture(t, 3)
	users, err := f.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if len(users[f.tags[1]]) != 0 || len(users[f.tags[2]]) != 0 {
		t.Fatal("fixture must have no direct entitlement at downstream hops")
	}
	for _, hop := range []int{0, 1} {
		if _, err = f.config(t, hop); err == nil {
			t.Fatalf("hop %d switched before its target accepted users", hop)
		}
	}
	before := make([][]byte, 3)
	for hop := 2; hop >= 0; hop-- {
		before[hop] = f.apply(t, hop)
	}
	rows, err := f.st.RelayMeteringUsers()
	if err != nil || len(rows) != 4 {
		t.Fatalf("per-user hop mapping: %d %v", len(rows), err)
	}
	for _, row := range rows {
		if row.State != "active" || !row.Enabled || row.SourceNames == "[]" {
			t.Fatalf("incomplete route acknowledgment: %+v", row)
		}
	}
	stamp, err := f.st.RelayMeteringProgress()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	afterStamp, err := f.st.RelayMeteringProgress()
	if err != nil || stamp != afterStamp {
		t.Fatalf("no-op planner changed readiness: %v", err)
	}
	after, err := f.st.RelayMeteringUsers()
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range after {
		if row.IdentityName != rows[i].IdentityName || row.Credential != rows[i].Credential {
			t.Fatal("no-op planner rotated a credential")
		}
	}
	for hop := 2; hop >= 0; hop-- {
		raw, err := f.config(t, hop)
		if err != nil || !bytes.Equal(raw, before[hop]) {
			t.Fatalf("no-op hop %d changed config: %v", hop, err)
		}
	}
}

func TestRelayMeteringUsersAckNeedsAppliedUserRoutes(t *testing.T) {
	for _, change := range []string{"missing-rule", "wildcard-rule", "wrong-user", "wrong-outbound", "earlier-override", "inverted-rule", "restricted-rule", "inverted-earlier-override"} {
		t.Run(change, func(t *testing.T) {
			f := newUserMeteringFixture(t, 2)
			f.apply(t, 1)
			raw, err := f.config(t, 0)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := f.st.RelayMeteringUsers()
			if err != nil {
				t.Fatal(err)
			}
			target := rows[0]
			var cfg map[string]interface{}
			if err = json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			route := cfg["route"].(map[string]interface{})
			rules := route["rules"].([]interface{})
			var kept []interface{}
			for _, value := range rules {
				rule := value.(map[string]interface{})
				if rule["outbound"] == target.outboundTag() {
					switch change {
					case "missing-rule":
						continue
					case "wildcard-rule":
						delete(rule, "auth_user")
					case "wrong-user":
						rule["auth_user"] = []string{"another-owner"}
					case "wrong-outbound":
						rule["outbound"] = "direct"
					case "inverted-rule":
						rule["invert"] = true
					case "restricted-rule":
						rule["network"] = "tcp"
					}
				}
				kept = append(kept, value)
			}
			if change == "earlier-override" {
				kept = append([]interface{}{map[string]interface{}{"inbound": []string{f.tags[0]}, "outbound": "direct"}}, kept...)
			}
			if change == "inverted-earlier-override" {
				kept = append([]interface{}{map[string]interface{}{"inbound": []string{"unrelated-inbound"}, "invert": true, "outbound": "direct"}}, kept...)
			}
			route["rules"] = kept
			bad, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.st.RecordRelayConfigApplied(f.servers[0], bad); err != nil {
				t.Fatal(err)
			}
			var state string
			if err = f.st.db.QueryRow(`SELECT state FROM relay_metering_users WHERE id=?`, target.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state == "active" {
				t.Fatal("isolated outbound incorrectly endorsed missing/wrong user route")
			}
			if err = f.st.RecordRelayConfigApplied(f.servers[0], raw); err != nil {
				t.Fatal(err)
			}
			if err = f.st.db.QueryRow(`SELECT state FROM relay_metering_users WHERE id=?`, target.ID).Scan(&state); err != nil || state != "active" {
				t.Fatalf("correct route not acknowledged: %s %v", state, err)
			}
		})
	}
}

func TestRelayMeteringUsersTargetAckRevokesStaleReadiness(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	landing := f.apply(t, 1)
	f.apply(t, 0)
	rows, err := f.st.RelayMeteringUsers()
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err = json.Unmarshal(landing, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, value := range cfg["inbounds"].([]interface{}) {
		inbound := value.(map[string]interface{})
		for _, value := range inbound["users"].([]interface{}) {
			user := value.(map[string]interface{})
			if user["name"] == rows[0].IdentityName {
				user["uuid"] = "00000000-0000-0000-0000-000000000000"
			}
		}
	}
	bad, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[1], bad); err != nil {
		t.Fatal(err)
	}
	if _, err = f.config(t, 0); err == nil {
		t.Fatal("source trusted an applied target without its credential")
	}
}

func TestRelayMeteringUsersAddRemoveWithoutRekey(t *testing.T) {
	f := newUserMeteringFixture(t, 3)
	var oldEntry []byte
	for hop := 2; hop >= 0; hop-- {
		raw := f.apply(t, hop)
		if hop == 0 {
			oldEntry = raw
		}
	}
	old, err := f.st.RelayMeteringUsers()
	if err != nil {
		t.Fatal(err)
	}
	uid := mkUser(t, f.st, "stage-owner-new")
	buy(t, f.st, uid, f.pkg)
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.config(t, 0); err == nil {
		t.Fatal("new owner switched before downstream preparation")
	}
	for hop := 2; hop >= 1; hop-- {
		f.apply(t, hop)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[0], oldEntry); err != nil {
		t.Fatal(err)
	}
	var staleState string
	if err = f.st.db.QueryRow(`SELECT u.state FROM relay_metering_users u JOIN relay_metering_links l ON l.id=u.link_id WHERE u.user_id=? AND l.source_server_id=? AND u.generation=l.generation`, uid, f.servers[0]).Scan(&staleState); err != nil || staleState == "active" {
		t.Fatalf("old source config endorsed a newly added user: %s %v", staleState, err)
	}
	f.apply(t, 0)
	current, err := f.st.RelayMeteringUsers()
	if err != nil || len(current) != 6 {
		t.Fatalf("new owner missing a hop: %d %v", len(current), err)
	}
	for _, prior := range old {
		for _, row := range current {
			if prior.ID == row.ID && (prior.IdentityName != row.IdentityName || prior.Credential != row.Credential) {
				t.Fatal("adding user rekeyed existing users")
			}
		}
	}
	if _, err = f.st.db.Exec(`UPDATE users SET status='banned' WHERE id=?`, f.owners[0]); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	for hop := 2; hop >= 0; hop-- {
		raw := f.apply(t, hop)
		for _, prior := range old {
			if prior.UserID == f.owners[0] && strings.Contains(string(raw), prior.IdentityName) {
				t.Fatal("removed owner revived from an old relay row")
			}
		}
	}
	var disabled int
	if err = f.st.db.QueryRow(`SELECT COUNT(*) FROM relay_metering_users WHERE user_id=? AND enabled=0`, f.owners[0]).Scan(&disabled); err != nil || disabled != 2 {
		t.Fatalf("history removed instead of disabled: %d %v", disabled, err)
	}
	if _, err = f.st.db.Exec(`UPDATE users SET status='active' WHERE id=?`, f.owners[0]); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.config(t, 0); err == nil {
		t.Fatal("re-enabled owner bypassed fresh downstream acceptance")
	}
	for hop := 2; hop >= 0; hop-- {
		f.apply(t, hop)
	}
	for _, prior := range old {
		var identity, credential, state string
		if err = f.st.db.QueryRow(`SELECT identity_name,credential,state FROM relay_metering_users WHERE id=?`, prior.ID).Scan(&identity, &credential, &state); err != nil || identity != prior.IdentityName || credential != prior.Credential || state != "active" {
			t.Fatalf("re-enabled owner was rekeyed or not staged: %s %v", state, err)
		}
	}
}

func TestRelayMeteringUsersDirectAndRelayedOwnerAtMiddle(t *testing.T) {
	f := newUserMeteringFixture(t, 3)
	middlePlan := mkPlan(t, f.st, "middle-direct", 10, 10, 30)
	bindPlanToInbound(t, f.st, middlePlan.ID, f.tags[1])
	buy(t, f.st, f.owners[0], middlePlan)
	if err := f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	users, err := f.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	for hop := 2; hop >= 0; hop-- {
		f.apply(t, hop)
	}
	rows, err := f.st.RelayMeteringUsers()
	if err != nil {
		t.Fatal(err)
	}
	links, err := f.st.RelayMeteringLinks()
	if err != nil {
		t.Fatal(err)
	}
	var incomingName string
	var downstream *RelayMeteringUser
	for _, row := range rows {
		if row.UserID != f.owners[0] {
			continue
		}
		for _, link := range links {
			if link.ID != row.LinkID {
				continue
			}
			if link.TargetServerID == f.servers[1] {
				incomingName = row.IdentityName
			}
			if link.SourceServerID == f.servers[1] {
				downstream = row
			}
		}
	}
	if incomingName == "" || downstream == nil || downstream.State != "active" {
		t.Fatal("original owner did not traverse middle hop")
	}
	var names []string
	if err = json.Unmarshal([]byte(downstream.SourceNames), &names); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if !seen[incomingName] {
		t.Fatal("middle hop lost relayed owner")
	}
	for _, user := range users[f.tags[1]] {
		if user.OwnerID == f.owners[0] && !seen[user.Name] {
			t.Fatal("middle hop lost same owner's direct identity")
		}
	}
}

func TestRelayMeteringUsersLogicalRoutePrecedesPhysicalFallback(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	other, err := f.st.CreateServer(Server{Name: "logical-exit", Host: "192.0.2.99", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := f.st.SaveSbInbound(&SbInbound{ServerID: other, Type: "vless", Tag: "logical-user-landing", ListenPort: 2443, Options: `{}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := f.st.PlanGroupIDs(f.pkg.ID)
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := f.st.CreateNode(Node{Type: "self_built", Name: "user-selected-exit", InboundTag: f.tags[0], RouteUpstreamInboundID: landing, Enabled: true, GroupIDs: groups})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	f.apply(t, 1)
	users, err := f.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := f.st.BuildSingboxConfigForServer(other, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.RecordRelayConfigApplied(other, raw); err != nil {
		t.Fatal(err)
	}
	f.apply(t, 0)
	links, err := f.st.RelayMeteringLinks()
	if err != nil {
		t.Fatal(err)
	}
	routeByLink := map[int64]int64{}
	for _, link := range links {
		routeByLink[link.ID] = link.RouteNodeID
	}
	rows, err := f.st.RelayMeteringUsers()
	if err != nil || len(rows) != 4 {
		t.Fatalf("expected two owners on two selectable paths: %d %v", len(rows), err)
	}
	for _, row := range rows {
		if row.State != "active" {
			t.Fatalf("logical/physical routing not independently applied: %+v", row)
		}
		var names []string
		if err = json.Unmarshal([]byte(row.SourceNames), &names); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if isRouteIdentityFor(name, routeID) != (routeByLink[row.LinkID] == routeID) {
				t.Fatalf("identity %q went to wrong physical/logical path", name)
			}
		}
	}
}

func TestRelayMeteringUsersNewGenerationRetainsHistoricalOwners(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	oldLanding := f.apply(t, 1)
	f.apply(t, 0)
	old, err := f.st.RelayMeteringUsers()
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.st.GetSbInbound(f.inbounds[1])
	if err != nil {
		t.Fatal(err)
	}
	target.ListenPort++
	if _, err = f.st.SaveSbInbound(target); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[1], oldLanding); err != nil {
		t.Fatal(err)
	}
	if _, err = f.config(t, 0); err == nil {
		t.Fatal("old generation config endorsed the changed target")
	}
	f.apply(t, 1)
	f.apply(t, 0)
	current, err := f.st.RelayMeteringUsers()
	if err != nil || len(current) != 4 {
		t.Fatalf("expected retained old and new generations: %d %v", len(current), err)
	}
	for _, prior := range old {
		for _, row := range current {
			if row.ID == prior.ID && (row.UserID != prior.UserID || row.LinkID != prior.LinkID || row.IdentityName != prior.IdentityName || row.Credential != prior.Credential) {
				t.Fatal("generation change rebound historical identity")
			}
		}
	}
}

func TestRelayMeteringUsersOptInDoesNotNarrowP0(t *testing.T) {
	st, a, b, _, _, users := meteringRelayFixture(t) // existing Trojan landing
	if st.RelayUserMeteringEnabled() {
		t.Fatal("P1 enabled by default")
	}
	if err := st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	raw, err := st.BuildSingboxConfigForServer(b, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RecordRelayConfigApplied(b, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = st.BuildSingboxConfigForServer(a, singbox.DefaultBaseConfig, "127.0.0.1:18080", users); err != nil {
		t.Fatal(err)
	}
	if err = st.ConfigureTrafficMetering(true, false, true); err == nil {
		t.Fatal("unverified P1 landing protocol activation succeeded")
	}
	if st.RelayUserMeteringEnabled() {
		t.Fatal("failed P1 preflight polluted the persisted switch")
	}
	if err = st.PrepareRelayMetering(); err != nil {
		t.Fatal("failed P1 activation blocked later P0 user/config changes:", err)
	}
	if err = st.ConfigureTrafficMetering(true, false, false); err != nil {
		t.Fatal(err)
	}
	if err = st.PrepareRelayMetering(); err != nil {
		t.Fatal("P0 failed after disabling P1:", err)
	}
}

func TestRelayMeteringUsersActivationPreflightIsAtomic(t *testing.T) {
	for _, problem := range []string{"same-machine", "cycle", "disabled-target"} {
		t.Run(problem, func(t *testing.T) {
			f := newUserMeteringFixture(t, 3)
			if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
				t.Fatal(err)
			}
			target, err := f.st.GetSbInbound(f.inbounds[2])
			if err != nil {
				t.Fatal(err)
			}
			switch problem {
			case "same-machine":
				target.ServerID = f.servers[1]
			case "cycle":
				target.UpstreamInboundID = f.inbounds[0]
			case "disabled-target":
				target.Enabled = false
			}
			if _, err = f.st.SaveSbInbound(target); err != nil {
				t.Fatal(err)
			}
			if err = f.st.ConfigureTrafficMetering(true, true, true); err == nil {
				t.Fatal("invalid topology enabled P1")
			}
			p1, err := f.st.GetSetting(RelayUserMeteringSetting)
			if err != nil || p1 != "false" {
				t.Fatalf("P1 switch changed on failed preflight: %q %v", p1, err)
			}
			cumulative, err := f.st.GetSetting("traffic_cumulative_metering")
			if err != nil || cumulative != "false" {
				t.Fatalf("counter mode changed on failed preflight: %q %v", cumulative, err)
			}
		})
	}
}
