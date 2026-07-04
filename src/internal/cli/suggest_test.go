package cli

import (
	"strings"
	"testing"
)

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

// TestUnknownFieldMessage covers that the error message always appends the full
// valid field list, and still includes the did-you-mean when a close match exists.
func TestUnknownFieldMessage(t *testing.T) {
	t.Run("far field includes valid list but no did-you-mean", func(t *testing.T) {
		msg := unknownFieldMessage("published")
		if !strings.Contains(msg, "valid fields:") {
			t.Errorf("message missing 'valid fields:': %q", msg)
		}
		if !strings.Contains(msg, "published_at") {
			t.Errorf("message missing 'published_at' in field list: %q", msg)
		}
		if strings.Contains(msg, "did you mean") {
			t.Errorf("message should not have did-you-mean for distance-3 input, got: %q", msg)
		}
	})

	t.Run("near typo includes both did-you-mean and valid list", func(t *testing.T) {
		msg := unknownFieldMessage("tilte")
		if !strings.Contains(msg, "did you mean") {
			t.Errorf("message missing did-you-mean for close typo: %q", msg)
		}
		if !strings.Contains(msg, "valid fields:") {
			t.Errorf("message missing 'valid fields:': %q", msg)
		}
	})
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
