package ftpfs

import "testing"

func TestSortEntriesAlphabetically(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Name: "zeta", Path: "zeta"},
		{Name: "alpha", Path: "alpha"},
		{Name: "beta", Path: "beta"},
	}

	sortEntries(entries)

	if entries[0].Name != "alpha" || entries[1].Name != "beta" || entries[2].Name != "zeta" {
		t.Fatalf("unexpected order: %#v", entries)
	}
}

func TestSortEntriesWithDotEntriesFilteredByListingPath(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Name: "..", Path: ".."},
		{Name: "beta", Path: "beta"},
		{Name: ".", Path: "."},
		{Name: "alpha", Path: "alpha"},
	}

	filtered := entries[:0]
	for _, entry := range entries {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		filtered = append(filtered, entry)
	}
	sortEntries(filtered)

	if len(filtered) != 2 {
		t.Fatalf("expected dot entries removed, got %#v", filtered)
	}
	if filtered[0].Name != "alpha" || filtered[1].Name != "beta" {
		t.Fatalf("unexpected order after filtering: %#v", filtered)
	}
}
