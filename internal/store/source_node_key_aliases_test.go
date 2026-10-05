package store

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"qingzhou/internal/subconv"
)

const sourceLegacyVMessJSON = `{"add":"node.example.test","aid":"0","allowInsecure":"1","host":"cdn.test","id":"11111111-2222-4333-8444-555555555555","net":"ws","path":"/ws","port":"443","ps":"same-name","sni":"tls.test","tls":"tls","type":"none","v":"2"}`
const sourceWSYAML = `proxies:
- name: same-name
  type: vmess
  server: node.example.test
  port: 443
  uuid: 11111111-2222-4333-8444-555555555555
  alterId: 0
  tls: true
  servername: tls.test
  skip-cert-verify: true
  network: ws
  ws-opts:
    path: /ws
    headers: {Host: cdn.test}
`

func sourceCompatNodes(t *testing.T, raw string) []Node {
	t.Helper()
	proxies := subconv.ParseList(raw)
	if len(proxies) == 0 {
		t.Fatal("empty fixture")
	}
	nodes := make([]Node, 0, len(proxies))
	for _, p := range proxies {
		nodes = append(nodes, Node{Name: p.Name, Protocol: p.Protocol, ShareLink: p.Raw, ImportLegacyKeys: p.SourceLegacyKeys})
	}
	return nodes
}
func sourceCompatFixture(t *testing.T) (*Store, int64, string) {
	t.Helper()
	st := newRefundStore(t)
	source, err := st.CreateSource(NodeSource{Name: "provider", URL: "https://source.example.test/sub"})
	if err != nil {
		t.Fatal(err)
	}
	old := "vmess://" + base64.StdEncoding.EncodeToString([]byte(sourceLegacyVMessJSON))
	if err = st.ReplaceSourceNodes(source, []Node{{Name: "same-name", Protocol: "vmess", ShareLink: old}}, nil, ""); err != nil {
		t.Fatal(err)
	}
	return st, source, old
}
func TestSourceNodeKeyAliasesUpgradeRepeatOmissionAndDelete(t *testing.T) {
	st, source, old := sourceCompatFixture(t)
	uid := mkUser(t, st, "source-node-prefs")
	oldKey := subconv.NodeKey(old)
	if err := st.SetNodeDisabled(uid, oldKey, true); err != nil {
		t.Fatal(err)
	}
	nodes := sourceCompatNodes(t, sourceWSYAML)
	current := subconv.NodeKey(nodes[0].ShareLink)
	if current == oldKey {
		t.Fatal("fixture must reproduce serializer key change")
	}
	for i := 0; i < 2; i++ {
		if err := st.ReplaceSourceNodes(source, nodes, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	aliases, err := st.SourceNodeKeyAliases([]int64{source})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, key := range aliases[source][current] {
		found = found || key == oldKey
	}
	if !found {
		t.Fatalf("old disabled key missing: %v", aliases)
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM user_disabled_nodes WHERE user_id=? AND node_key=?`, uid, oldKey); n != 1 {
		t.Fatal("refresh mutated user preference")
	}
	// A node can be temporarily absent while another remains in a valid feed.
	if err = st.ReplaceSourceNodes(source, sourceCompatNodes(t, "trojan://other@other.example.test:443#other"), nil, ""); err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceSourceNodes(source, nodes, nil, ""); err != nil {
		t.Fatal(err)
	}
	aliases, err = st.SourceNodeKeyAliases([]int64{source})
	if err != nil || len(aliases[source][current]) == 0 {
		t.Fatal("temporary omission erased historical preference", aliases, err)
	}
	count := scalar(t, st, `SELECT COUNT(*) FROM node_source_key_aliases WHERE source_id=?`, source)
	if count != int64(len(aliases[source][current])) {
		t.Fatal("repeated refresh grew duplicate aliases")
	}
	if err = st.DeleteSource(source); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM node_source_key_aliases WHERE source_id=?`, source); n != 0 {
		t.Fatal("source deletion leaked lineage")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM user_disabled_nodes WHERE user_id=?`, uid); n != 1 {
		t.Fatal("source deletion deleted user preference")
	}
}
func TestSourceNodeKeyAliasesCannotCrossSourceOrChangedCredentials(t *testing.T) {
	for _, change := range []string{"other-source", "host", "credential", "uri-claim"} {
		t.Run(change, func(t *testing.T) {
			st, source, old := sourceCompatFixture(t)
			body := sourceWSYAML
			switch change {
			case "other-source":
				var err error
				source, err = st.CreateSource(NodeSource{Name: "same-name", URL: "https://other-source.example.test/sub"})
				if err != nil {
					t.Fatal(err)
				}
			case "host":
				body = strings.ReplaceAll(body, "node.example.test", "another.example.test")
			case "credential":
				body = strings.ReplaceAll(body, "11111111-2222-4333-8444-555555555555", "99999999-2222-4333-8444-555555555555")
			case "uri-claim":
				body = "trojan://unrelated@new.example.test:443?qz-node-key=" + subconv.NodeKey(old) + "#same-name"
			}
			nodes := sourceCompatNodes(t, body)
			if err := st.ReplaceSourceNodes(source, nodes, nil, ""); err != nil {
				t.Fatal(err)
			}
			aliases, err := st.SourceNodeKeyAliases([]int64{source})
			if err != nil {
				t.Fatal(err)
			}
			if len(aliases[source][subconv.NodeKey(nodes[0].ShareLink)]) != 0 {
				t.Fatal("unproved alias inherited", aliases)
			}
		})
	}
}
func TestSourceNodeKeyAliasesRefreshFailureIsAtomic(t *testing.T) {
	st, source, old := sourceCompatFixture(t)
	before, _ := st.GetSource(source)
	if _, err := st.db.Exec(`CREATE TRIGGER fail_source_alias BEFORE INSERT ON node_source_key_aliases BEGIN SELECT RAISE(FAIL,'fixture alias write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceSourceNodes(source, sourceCompatNodes(t, sourceWSYAML), nil, ""); err == nil {
		t.Fatal("expected metadata failure")
	}
	nodes, _ := st.ListNodes()
	after, _ := st.GetSource(source)
	if len(nodes) != 1 || nodes[0].ShareLink != old || after.LastFetched != before.LastFetched {
		t.Fatal("failed alias write partially refreshed cache")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM node_source_key_aliases`); n != 0 {
		t.Fatal("alias escaped rollback")
	}
	if _, err := st.db.Exec(`DROP TRIGGER fail_source_alias`); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceSourceNodes(source, sourceCompatNodes(t, sourceWSYAML), nil, ""); err != nil {
		t.Fatal(err)
	}
}
func TestSourceNodeKeyAliasesSharedLegacyEnablesOnlyRequestedNode(t *testing.T) {
	st, source, old := sourceCompatFixture(t)
	uid := mkUser(t, st, "shared-source-key")
	oldKey := subconv.NodeKey(old)
	if err := st.SetNodeDisabled(uid, oldKey, true); err != nil {
		t.Fatal(err)
	}
	// Extra headers did not exist in the old serializer, so both replacements
	// genuinely project to the same previously cached node.
	a := strings.Replace(sourceWSYAML, "headers: {Host: cdn.test}", "headers: {Host: cdn.test, X-Variant: a}", 1)
	b := strings.Replace(sourceWSYAML, "headers: {Host: cdn.test}", "headers: {Host: cdn.test, X-Variant: b}", 1)
	nodes := append(sourceCompatNodes(t, a), sourceCompatNodes(t, b)...)
	if err := st.ReplaceSourceNodes(source, nodes, nil, ""); err != nil {
		t.Fatal(err)
	}
	aliases, err := st.SourceNodeKeyAliases([]int64{source})
	if err != nil {
		t.Fatal(err)
	}
	keyA, keyB := subconv.NodeKey(nodes[0].ShareLink), subconv.NodeKey(nodes[1].ShareLink)
	if keyA == keyB || len(aliases[source][keyA]) == 0 || len(aliases[source][keyB]) == 0 {
		t.Fatal("invalid shared legacy fixture")
	}
	if err = st.ApplyNodePrefsWithAliases(uid, nil, []string{keyA}, aliases[source]); err != nil {
		t.Fatal(err)
	}
	disabled, err := st.DisabledNodeKeys(uid)
	if err != nil || disabled[keyA] || !disabled[keyB] || disabled[oldKey] {
		t.Fatalf("sibling revived: %v %v", disabled, err)
	}
}
func TestSourceNodeKeyAliasMigrationRollback(t *testing.T) {
	st := openUnmigrated(t)
	chain := st.migrations()
	if err := st.runMigrations(chain[:9]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_alias_version BEFORE INSERT ON schema_migrations WHEN NEW.version='000010_source_node_key_aliases' BEGIN SELECT RAISE(FAIL,'fixture version failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err == nil {
		t.Fatal("expected migration failure")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM sqlite_schema WHERE name='node_source_key_aliases'`); n != 0 {
		t.Fatal("partial schema leaked")
	}
	if _, err := st.db.Exec(`DROP TRIGGER fail_alias_version`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := st.Migrate(); err != nil {
			t.Fatal(err)
		}
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, fmt.Sprintf("%06d_source_node_key_aliases", 10)); n != 1 {
		t.Fatal("duplicate marker")
	}
}
