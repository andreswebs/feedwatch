package core

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// TagMatch selects how a multi-tag filter combines. The zero value matches
// feeds carrying every requested tag, which is the safer default: a lane is
// normally an intersection, not a union.
type TagMatch string

const (
	// MatchAll requires a feed to carry every requested tag.
	MatchAll TagMatch = "all"
	// MatchAny requires a feed to carry at least one requested tag.
	MatchAny TagMatch = "any"
)

// CanonicalTags normalizes a tag set for storage and comparison: each tag is
// trimmed and lowercased, duplicates are dropped, and the result is sorted, so
// two invocations naming the same lane in different spellings or orders produce
// identical stored bytes. It returns an empty, non-nil slice for no tags.
func CanonicalTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		norm := strings.ToLower(strings.TrimSpace(t))
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	sort.Strings(out)
	return out
}

// ValidateTags reports the first tag that cannot be stored, as a usage-category
// error. A tag is rejected when it is empty after trimming, or contains a comma
// or any whitespace: commas because a slice flag splits on them, whitespace
// because it makes a tag unquotable in a shell or a cron script.
func ValidateTags(tags []string) error {
	for _, t := range tags {
		switch {
		case strings.TrimSpace(t) == "":
			return tagUsageErr("tag must not be empty, got " + strconv.Quote(t))
		case strings.Contains(t, ","):
			return tagUsageErr("tag must not contain a comma, got " + strconv.Quote(t))
		case strings.ContainsFunc(t, unicode.IsSpace):
			return tagUsageErr("tag must not contain whitespace, got " + strconv.Quote(t))
		}
	}
	return nil
}

// ParseTagMatch resolves a --match value. The empty string is MatchAll, so a
// zero-valued filter matches the documented default.
func ParseTagMatch(s string) (TagMatch, error) {
	switch TagMatch(s) {
	case "", MatchAll:
		return MatchAll, nil
	case MatchAny:
		return MatchAny, nil
	default:
		return "", tagUsageErr("match must be 'all' or 'any', got " + strconv.Quote(s))
	}
}

// tagUsageErr builds the usage-category error this file reports. The root
// package's usageErr helper is not importable from core, so the FeedError is
// built directly.
func tagUsageErr(msg string) error {
	return &FeedError{Category: CatUsage, Message: msg, Err: ErrUsage}
}

// TagCount reports how many subscriptions carry one tag. It is the unit of the
// lane vocabulary the `tags` command reports.
type TagCount struct {
	Tag   string `json:"tag"`
	Feeds int    `json:"feeds"`
}
