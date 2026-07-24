package store

import "testing"

// ReorderPackages rewrites sort_order to the given id order, and ListPackages
// (ORDER BY sort_order) reflects it — including a reversal.
func TestReorderPackages(t *testing.T) {
	st := newRefundStore(t)
	a := mkPlan(t, st, "A", 10, 10, 30)
	b := mkPlan(t, st, "B", 10, 10, 30)
	c := mkPlan(t, st, "C", 10, 10, 30)

	if err := st.ReorderPackages([]int64{c.ID, a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	got := listNames(t, st)
	if want := []string{"C", "A", "B"}; !equalStrings(got, want) {
		t.Fatalf("after reorder got %v, want %v", got, want)
	}

	// Reversal sticks too.
	if err := st.ReorderPackages([]int64{b.ID, a.ID, c.ID}); err != nil {
		t.Fatal(err)
	}
	if got := listNames(t, st); !equalStrings(got, []string{"B", "A", "C"}) {
		t.Fatalf("after second reorder got %v", got)
	}
}

func listNames(t *testing.T, st *Store) []string {
	t.Helper()
	pkgs, err := st.ListPackages()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		names = append(names, p.Name)
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
