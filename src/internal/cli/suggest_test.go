package cli

import "testing"

func TestNearestField(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantOK  bool
		comment string
	}{
		{"single typo", "tilte", "title", true, "transposition within threshold"},
		{"missing char", "athor", "author", true, "one deletion"},
		{"extra char", "linkk", "link", true, "one insertion"},
		{"no close match", "zzzzzz", "", false, "distance beyond threshold"},
		{"empty", "", "", false, "empty input has no useful suggestion"},
		{"exact match suggests nothing useful", "title", "title", true, "distance 0 is closest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nearestField(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("nearestField(%q) ok = %v, want %v (%s)", tc.input, ok, tc.wantOK, tc.comment)
			}
			if ok && got != tc.want {
				t.Errorf("nearestField(%q) = %q, want %q (%s)", tc.input, got, tc.want, tc.comment)
			}
		})
	}
}

// nearestField must be deterministic across repeated calls, including when more
// than one candidate is equidistant: ties break by shortest candidate then
// lexical order, so the suggestion never flaps with map iteration.
func TestNearestFieldDeterministic(t *testing.T) {
	const input = "tilte"
	first, ok := nearestField(input)
	if !ok {
		t.Fatalf("nearestField(%q) returned no suggestion", input)
	}
	for i := 0; i < 50; i++ {
		got, ok := nearestField(input)
		if !ok || got != first {
			t.Fatalf("nearestField(%q) not deterministic: got %q (ok=%v), want %q", input, got, ok, first)
		}
	}
}
