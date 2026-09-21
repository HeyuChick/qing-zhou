package api

import "testing"

func TestSortNodeEntriesInterleavesNativeAndExternalNodes(t *testing.T) {
	entries := []nodeEntry{
		{Link: "cf://node", SortOrder: 2, SortID: 2},
		{Link: "oci://node", SortOrder: 1, SortID: 1},
		{Link: "other://node", SortOrder: 3, SortID: 3},
	}

	sortNodeEntries(entries)

	got := []string{entries[0].Link, entries[1].Link, entries[2].Link}
	want := []string{"oci://node", "cf://node", "other://node"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node order = %v, want %v", got, want)
		}
	}
}

func TestSortNodeEntriesUsesNodeIDAsTieBreaker(t *testing.T) {
	entries := []nodeEntry{
		{Link: "second", SortOrder: 0, SortID: 2},
		{Link: "first", SortOrder: 0, SortID: 1},
	}

	sortNodeEntries(entries)

	if entries[0].Link != "first" || entries[1].Link != "second" {
		t.Fatalf("tie order = %q, %q", entries[0].Link, entries[1].Link)
	}
}
