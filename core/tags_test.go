package core_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/core"
)

// CanonicalTags trims, lowercases, deduplicates, and sorts a tag set in one
// pass, so two invocations naming the same lane in different spellings or orders
// produce identical stored bytes.
func TestCanonicalTagsNormalizes(t *testing.T) {
	got := core.CanonicalTags([]string{"AI", " agents ", "ai"})
	want := []string{"agents", "ai"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("CanonicalTags() = %v, want %v", got, want)
	}
}

// CanonicalTags returns an empty, non-nil slice for no tags, so a caller can
// marshal it to [] without a nil check.
func TestCanonicalTagsEmptyIsNonNil(t *testing.T) {
	got := core.CanonicalTags(nil)

	if got == nil {
		t.Fatal("CanonicalTags(nil) = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("CanonicalTags(nil) = %v, want empty", got)
	}
}

// ValidateTags accepts a well-formed tag set.
func TestValidateTagsAcceptsWellFormed(t *testing.T) {
	if err := core.ValidateTags([]string{"ai", "Agents", "go-lang"}); err != nil {
		t.Errorf("ValidateTags() = %v, want nil", err)
	}
}

// ValidateTags rejects a tag that is empty after trimming, as a usage-category
// error quoting what the user typed.
func TestValidateTagsRejectsEmpty(t *testing.T) {
	for _, tag := range []string{"", "   "} {
		t.Run(strings.ReplaceAll(tag, " ", "_"), func(t *testing.T) {
			err := core.ValidateTags([]string{tag})
			if err == nil {
				t.Fatalf("ValidateTags(%q) = nil, want error", tag)
			}

			var fe *core.FeedError
			if !errors.As(err, &fe) {
				t.Fatalf("ValidateTags(%q) error is %T, want *core.FeedError", tag, err)
			}
			if fe.Category != core.CatUsage {
				t.Errorf("Category = %q, want %q", fe.Category, core.CatUsage)
			}
			if !errors.Is(err, core.ErrUsage) {
				t.Errorf("errors.Is(err, core.ErrUsage) = false, want true")
			}
		})
	}
}

// ValidateTags rejects a tag carrying a comma (a slice flag splits on them) or
// any whitespace (unquotable in a shell or cron script), validating the raw
// input so the message names what the user typed.
func TestValidateTagsRejectsIllegalCharacters(t *testing.T) {
	tests := []struct {
		name string
		tag  string
	}{
		{"comma", "a,b"},
		{"space", "machine learning"},
		{"tab", "a\tb"},
		{"newline", "a\nb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := core.ValidateTags([]string{"ok", tt.tag})
			if err == nil {
				t.Fatalf("ValidateTags(%q) = nil, want error", tt.tag)
			}

			var fe *core.FeedError
			if !errors.As(err, &fe) {
				t.Fatalf("ValidateTags(%q) error is %T, want *core.FeedError", tt.tag, err)
			}
			if fe.Category != core.CatUsage {
				t.Errorf("Category = %q, want %q", fe.Category, core.CatUsage)
			}
			if !errors.Is(err, core.ErrUsage) {
				t.Errorf("errors.Is(err, core.ErrUsage) = false, want true")
			}
			if quoted := strconv.Quote(tt.tag); !strings.Contains(err.Error(), quoted) {
				t.Errorf("error %q does not quote the offending value %s", err, quoted)
			}
		})
	}
}

// ParseTagMatch resolves the accepted --match values, treating the empty string
// as the documented MatchAll default.
func TestParseTagMatchAccepted(t *testing.T) {
	tests := []struct {
		in   string
		want core.TagMatch
	}{
		{"", core.MatchAll},
		{"all", core.MatchAll},
		{"any", core.MatchAny},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := core.ParseTagMatch(tt.in)
			if err != nil {
				t.Fatalf("ParseTagMatch(%q) = %v, want nil error", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseTagMatch(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ParseTagMatch rejects an unrecognized value as a usage-category error naming
// the accepted values.
func TestParseTagMatchRejectsUnknown(t *testing.T) {
	_, err := core.ParseTagMatch("some")
	if err == nil {
		t.Fatal("ParseTagMatch(\"some\") = nil, want error")
	}

	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatUsage {
		t.Errorf("Category = %q, want %q", fe.Category, core.CatUsage)
	}
	if !errors.Is(err, core.ErrUsage) {
		t.Errorf("errors.Is(err, core.ErrUsage) = false, want true")
	}
	for _, want := range []string{"all", "any", `"some"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

// The zero value of every tag-carrying filter preserves today's "match
// everything" behavior: no tags requested and the intersection default.
func TestZeroFiltersMatchEverything(t *testing.T) {
	if got := (core.ListFilter{}); len(got.Tags) != 0 || got.Match != "" {
		t.Errorf("zero ListFilter = %+v, want no tags and empty Match", got)
	}
	if got := (core.ItemQuery{}); len(got.Tags) != 0 || got.Match != "" {
		t.Errorf("zero ItemQuery Tags/Match = %v/%q, want empty", got.Tags, got.Match)
	}
	if got := (core.PrunePolicy{}); len(got.Tags) != 0 || got.Match != "" {
		t.Errorf("zero PrunePolicy Tags/Match = %v/%q, want empty", got.Tags, got.Match)
	}

	match, err := core.ParseTagMatch(string(core.ListFilter{}.Match))
	if err != nil {
		t.Fatalf("ParseTagMatch(zero Match) = %v, want nil error", err)
	}
	if match != core.MatchAll {
		t.Errorf("zero Match resolves to %q, want %q", match, core.MatchAll)
	}
}
