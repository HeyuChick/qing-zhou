package store

import "testing"

func TestNodeKeyCompatibilityEnableKeepsSharedSiblingDisabled(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "old-node-keys")
	aliases := map[string][]string{"new-a": {"new-a", "old-shared"}, "new-b": {"new-b", "old-shared"}}
	if err := st.SetNodeDisabled(uid, "old-shared", true); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyNodePrefsWithAliases(uid, nil, []string{"new-a"}, aliases); err != nil {
		t.Fatal(err)
	}
	disabled, err := st.DisabledNodeKeys(uid)
	if err != nil {
		t.Fatal(err)
	}
	if disabled["old-shared"] || disabled["new-a"] || !disabled["new-b"] {
		t.Fatalf("enabling one node changed sibling: %v", disabled)
	}
	if err = st.ApplyNodePrefsWithAliases(uid, nil, []string{"new-b"}, aliases); err != nil {
		t.Fatal(err)
	}
	disabled, err = st.DisabledNodeKeys(uid)
	if err != nil || len(disabled) != 0 {
		t.Fatalf("explicitly enabled sibling still blocked: %v %v", disabled, err)
	}
}
func TestNodeKeyCompatibilityFailureRollsBackAliasAndSibling(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "old-node-key-rollback")
	aliases := map[string][]string{"new-a": {"new-a", "old-shared"}, "new-b": {"new-b", "old-shared"}}
	if err := st.SetNodeDisabled(uid, "old-shared", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_sibling_key BEFORE INSERT ON user_disabled_nodes WHEN NEW.node_key='new-b' BEGIN SELECT RAISE(FAIL,'fixture write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyNodePrefsWithAliases(uid, nil, []string{"new-a"}, aliases); err == nil {
		t.Fatal("expected protected-sibling write failure")
	}
	disabled, err := st.DisabledNodeKeys(uid)
	if err != nil || len(disabled) != 1 || !disabled["old-shared"] {
		t.Fatalf("partial enable committed: %v %v", disabled, err)
	}
	if _, err = st.db.Exec(`DROP TRIGGER fail_sibling_key`); err != nil {
		t.Fatal(err)
	}
	if err = st.ApplyNodePrefsWithAliases(uid, nil, []string{"new-a", "new-b"}, aliases); err != nil {
		t.Fatal(err)
	}
	disabled, err = st.DisabledNodeKeys(uid)
	if err != nil || len(disabled) != 0 {
		t.Fatal("explicit bulk enable failed", disabled, err)
	}
}
