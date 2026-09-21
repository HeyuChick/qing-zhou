package store

import "testing"

func TestReplaceSourceNodesPreservesExistingSortOrder(t *testing.T) {
	st := newRefundStore(t)
	sourceID, err := st.CreateSource(NodeSource{Name: "source", URL: "https://source.example/sub"})
	if err != nil {
		t.Fatal(err)
	}
	old := []Node{
		{Type: "external", Name: "first", Protocol: "trojan", ShareLink: "trojan://first"},
		{Type: "external", Name: "second", Protocol: "trojan", ShareLink: "trojan://second"},
	}
	if err := st.ReplaceSourceNodes(sourceID, old, nil, ""); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReorderNodes([]int64{nodes[1].ID, nodes[0].ID}); err != nil {
		t.Fatal(err)
	}

	refreshed := []Node{
		{Type: "external", Name: "first renamed", Protocol: "trojan", ShareLink: "trojan://first"},
		{Type: "external", Name: "second", Protocol: "trojan", ShareLink: "trojan://second"},
		{Type: "external", Name: "new", Protocol: "trojan", ShareLink: "trojan://new"},
	}
	if err := st.ReplaceSourceNodes(sourceID, refreshed, nil, ""); err != nil {
		t.Fatal(err)
	}
	nodes, err = st.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	got := []string{nodes[0].Name, nodes[1].Name, nodes[2].Name}
	want := []string{"second", "first renamed", "new"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("source refresh order = %v, want %v", got, want)
		}
	}
}
