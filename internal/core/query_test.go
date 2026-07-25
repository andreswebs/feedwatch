package core

import (
	"sort"
	"testing"
)

// ItemFieldNames returns the projectable field names plus the always-on feed_url
// identity field, sorted, so callers can build stable suggestions and help text
// without leaking map-iteration nondeterminism.
func TestItemFieldNames(t *testing.T) {
	got := ItemFieldNames()

	if !sort.StringsAreSorted(got) {
		t.Errorf("ItemFieldNames() not sorted: %v", got)
	}

	set := make(map[string]bool, len(got))
	for _, n := range got {
		if set[n] {
			t.Errorf("ItemFieldNames() has duplicate %q", n)
		}
		set[n] = true
	}

	for f := range ValidItemFields {
		if !set[f] {
			t.Errorf("ItemFieldNames() missing projectable field %q", f)
		}
	}
	if !set["feed_url"] {
		t.Errorf("ItemFieldNames() missing always-on identity field %q", "feed_url")
	}
	if want := len(ValidItemFields) + 1; len(got) != want {
		t.Errorf("len(ItemFieldNames()) = %d, want %d", len(got), want)
	}
}
