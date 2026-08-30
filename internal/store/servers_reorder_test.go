package store

import "testing"

func TestReorderServersPersistsDisplayOrder(t *testing.T) {
	st := newRefundStore(t)
	ids := make([]int64, 3)
	for i, name := range []string{"A", "B", "C"} {
		id, err := st.CreateServer(Server{Name: name, Host: name})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	if err := st.ReorderServers([]int64{ids[2], ids[0], ids[1]}); err != nil {
		t.Fatal(err)
	}
	servers, err := st.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(servers))
	for i, sv := range servers {
		got[i] = sv.Name
		if sv.SortOrder != int64(i) {
			t.Fatalf("server %s sort_order=%d, want %d", sv.Name, sv.SortOrder, i)
		}
	}
	if !equalStrings(got, []string{"C", "A", "B"}) {
		t.Fatalf("server order = %v", got)
	}
	if err := st.ReorderServers([]int64{ids[0], ids[0], ids[2]}); err == nil {
		t.Fatal("duplicate server ids must be rejected")
	}

	// New servers append instead of jumping ahead of the manually sorted list.
	if _, err := st.CreateServer(Server{Name: "D", Host: "D"}); err != nil {
		t.Fatal(err)
	}
	servers, err = st.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if servers[len(servers)-1].Name != "D" {
		t.Fatalf("new server did not append: %+v", servers)
	}
}
