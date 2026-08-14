package feedwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/andreswebs/feedwatch/core"
)

// TagRequest names the subscription whose tags to read or edit, by URL or
// unique alias. With no write flag it reads. Add and Remove compose in one
// invocation; Set and Clear are each exclusive with everything else.
type TagRequest struct {
	Ref    string   `arg:"ref"`
	Add    []string `flag:"add" usage:"tag to add (repeatable); idempotent"`
	Remove []string `flag:"remove" usage:"tag to remove (repeatable); removing an absent tag is a no-op"`
	Set    []string `flag:"set" usage:"replace the feed's tags with exactly these"`
	Clear  bool     `flag:"clear" usage:"remove every tag from the feed"`
}

// Validate reports whether the request names a usable combination of write
// flags and storable tag names. An empty ref is left to the store's resolution,
// which reports it as a usage-category "feed not found", matching enable.
func (r TagRequest) Validate() error {
	if r.Clear && (len(r.Add) > 0 || len(r.Remove) > 0 || len(r.Set) > 0) {
		return usageErr("--clear cannot be combined with --add, --remove, or --set")
	}
	if len(r.Set) > 0 && (len(r.Add) > 0 || len(r.Remove) > 0) {
		return usageErr("--set cannot be combined with --add or --remove")
	}
	for _, tags := range [][]string{r.Add, r.Remove, r.Set} {
		if err := core.ValidateTags(tags); err != nil {
			return err
		}
	}
	return nil
}

// writes reports whether the request asks for an edit rather than a read.
func (r TagRequest) writes() bool {
	return r.Clear || len(r.Add) > 0 || len(r.Remove) > 0 || len(r.Set) > 0
}

// TagResult is the tag result envelope: the feed's canonical URL, its tags
// after the operation, and the delta this invocation applied. added and removed
// are always present, empty on a read or a no-op edit, so a caller sees what
// changed rather than what it asked for.
type TagResult struct {
	Head
	URL     string   `json:"url"`
	Tags    []string `json:"tags"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// MarshalJSON coalesces the three collections so each always serializes as []
// rather than null.
func (r TagResult) MarshalJSON() ([]byte, error) {
	type alias TagResult
	a := alias(r)
	for _, p := range []*[]string{&a.Tags, &a.Added, &a.Removed} {
		if *p == nil {
			*p = []string{}
		}
	}
	return json.Marshal(a)
}

// Tag reads or edits one subscription's lane tags. With no write flag it
// reports the stored set; --clear empties it, --set replaces it outright, and
// --add/--remove compose against it, with additions applied before removals so
// a tag named in both ends up removed. The reported delta is the actual one, so
// re-adding a carried tag reports nothing added, and an edit that changes
// nothing performs no write at all. An unknown reference is a usage-category
// failure.
func (a *App) Tag(ctx context.Context, req TagRequest) (TagResult, error) {
	if err := req.Validate(); err != nil {
		return TagResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return TagResult{}, err
	}

	feed, err := st.GetFeed(ctx, req.Ref)
	if err != nil {
		return TagResult{}, err
	}

	current := core.CanonicalTags(feed.Tags)
	next := req.apply(current)

	if req.writes() && !slices.Equal(current, next) {
		if err := st.SetTags(ctx, feed.URL, next); err != nil {
			return TagResult{}, err
		}
	}

	return TagResult{
		Head:    OKHead(),
		URL:     feed.URL,
		Tags:    next,
		Added:   missingFrom(current, next),
		Removed: missingFrom(next, current),
	}, nil
}

// apply resolves the request's write flags against the feed's current tags into
// the set it should carry afterwards. The result is canonical, so comparing it
// with the current set is enough to decide whether a write is needed.
func (r TagRequest) apply(current []string) []string {
	switch {
	case r.Clear:
		return []string{}
	case len(r.Set) > 0:
		return core.CanonicalTags(r.Set)
	case len(r.Add) == 0 && len(r.Remove) == 0:
		return current
	}

	next := core.CanonicalTags(append(append([]string(nil), current...), r.Add...))
	for _, drop := range core.CanonicalTags(r.Remove) {
		next = slices.DeleteFunc(next, func(t string) bool { return t == drop })
	}
	return next
}

// missingFrom returns the tags in have that from does not carry, in canonical
// order, which is the set difference both halves of the delta are built from.
func missingFrom(from, have []string) []string {
	out := make([]string, 0, len(have))
	for _, t := range have {
		if !slices.Contains(from, t) {
			out = append(out, t)
		}
	}
	return out
}

// RenderText is the optional human-text projection of the same values: the
// resulting tag set as a single line, following the one-line style of prune
// rather than a table for one row.
func (r TagResult) RenderText(w io.Writer, _ bool) error {
	_, err := fmt.Fprintf(w, "tags: %s\n", dashIfEmpty(strings.Join(r.Tags, ", ")))
	return err
}
