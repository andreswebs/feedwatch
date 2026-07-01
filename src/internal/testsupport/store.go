package testsupport

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// InMemoryStore is a map-backed store.Store double for fast unit tests of
// commands that do not need SQL semantics. Its behavior mirrors the SQLite
// implementation: alias resolution, conditional-GET validator skipping,
// dedup-and-upsert with tombstones, item query filters and projection, and
// prune-by-age or per-feed count. It is safe for concurrent use.
type InMemoryStore struct {
	mu            sync.Mutex
	clock         core.Clock
	feeds         map[string]core.Feed
	items         map[string]map[string]core.Item
	tombstones    map[string]map[string]bool
	schemaVersion int
	pending       int
}

// NewInMemoryStore returns an empty store using clk for timestamps. A nil clk
// falls back to the wall clock.
func NewInMemoryStore(clk core.Clock) *InMemoryStore {
	if clk == nil {
		clk = core.SystemClock
	}
	return &InMemoryStore{
		clock:         clk,
		feeds:         make(map[string]core.Feed),
		items:         make(map[string]map[string]core.Item),
		tombstones:    make(map[string]map[string]bool),
		schemaVersion: 1,
	}
}

// SetSchema configures the reported schema version and pending-migration count,
// for tests of the migrate command.
func (s *InMemoryStore) SetSchema(version, pending int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schemaVersion = version
	s.pending = pending
}

func (s *InMemoryStore) now() time.Time { return s.clock() }

// resolveLocked maps a URL or unique alias to its canonical URL.
func (s *InMemoryStore) resolveLocked(ref string) (string, bool) {
	if _, ok := s.feeds[ref]; ok {
		return ref, true
	}
	for url, f := range s.feeds {
		if f.Alias != "" && f.Alias == ref {
			return url, true
		}
	}
	return "", false
}

// AddFeed upserts a subscription keyed by URL. An alias bound to a different URL
// is a usage error.
func (s *InMemoryStore) AddFeed(_ context.Context, f core.Feed) (core.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if f.Status == "" {
		f.Status = core.FeedActive
	}
	if f.Alias != "" {
		for url, existing := range s.feeds {
			if existing.Alias == f.Alias && url != f.URL {
				return core.Feed{}, &core.FeedError{
					FeedURL:  f.URL,
					Category: core.CatUsage,
					Message:  "alias " + f.Alias + " already used by " + url,
				}
			}
		}
	}

	now := s.now()
	if existing, ok := s.feeds[f.URL]; ok {
		existing.Alias = f.Alias
		existing.Interval = f.Interval
		existing.UpdatedAt = now
		s.feeds[f.URL] = existing
		return existing, nil
	}

	f.CreatedAt = now
	f.UpdatedAt = now
	s.feeds[f.URL] = f
	return f, nil
}

// GetFeed returns the feed resolved by exact URL or unique alias.
func (s *InMemoryStore) GetFeed(_ context.Context, ref string) (core.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.resolveLocked(ref)
	if !ok {
		return core.Feed{}, &core.FeedError{
			FeedURL: ref, Category: core.CatUsage, Message: "feed not found",
		}
	}
	return s.feeds[url], nil
}

// RemoveFeed unsubscribes the feed resolved by URL or alias, cascading to its
// items. Removing an unknown feed is a no-op.
func (s *InMemoryStore) RemoveFeed(_ context.Context, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.resolveLocked(ref)
	if !ok {
		return nil
	}
	delete(s.feeds, url)
	delete(s.items, url)
	delete(s.tombstones, url)
	return nil
}

// ListFeeds returns subscriptions matching the filter, ordered by URL.
func (s *InMemoryStore) ListFeeds(_ context.Context, filter core.ListFilter) ([]core.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []core.Feed
	for _, f := range s.feeds {
		if filter.Status != "" && f.Status != filter.Status {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// DueFeeds returns active feeds with no next-due time or one at or before now,
// ordered by URL.
func (s *InMemoryStore) DueFeeds(_ context.Context, now time.Time) ([]core.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []core.Feed
	for _, f := range s.feeds {
		if f.Status != core.FeedActive {
			continue
		}
		if f.NextDueAt == nil || !f.NextDueAt.After(now) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// SetStatus enables or disables a feed.
func (s *InMemoryStore) SetStatus(_ context.Context, url string, st core.FeedStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.feeds[url]; ok {
		f.Status = st
		f.UpdatedAt = s.now()
		s.feeds[url] = f
	}
	return nil
}

// SetValidators writes conditional-GET validators, never overwriting a stored
// value with an empty one and skipping the write entirely when both are empty.
func (s *InMemoryStore) SetValidators(_ context.Context, url, etag, lastModified string) error {
	if etag == "" && lastModified == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[url]
	if !ok {
		return nil
	}
	if etag != "" {
		f.ETag = etag
	}
	if lastModified != "" {
		f.LastModified = lastModified
	}
	f.UpdatedAt = s.now()
	s.feeds[url] = f
	return nil
}

// RecordSuccess clears failure state and schedules the next poll. A non-empty
// finalURL distinct from url renames the feed (and its items) to the
// permanent-redirect target, unless that target is already subscribed. It
// returns the new canonical URL when a rename was applied, else "".
func (s *InMemoryStore) RecordSuccess(_ context.Context, url string, fetchedAt, nextDue time.Time, finalURL string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[url]
	if !ok {
		return "", nil
	}
	f.FailureCount = 0
	f.LastError = ""
	f.LastErrorAt = nil
	fa, nd := fetchedAt, nextDue
	f.LastFetchAt = &fa
	f.NextDueAt = &nd
	f.UpdatedAt = s.now()

	target := url
	if finalURL != "" && finalURL != url {
		if _, exists := s.feeds[finalURL]; !exists {
			target = finalURL
		}
	}
	if target != url {
		f.URL = target
		delete(s.feeds, url)
		if items, ok := s.items[url]; ok {
			delete(s.items, url)
			for k, it := range items {
				it.FeedURL = target
				items[k] = it
			}
			s.items[target] = items
		}
		if tomb, ok := s.tombstones[url]; ok {
			delete(s.tombstones, url)
			s.tombstones[target] = tomb
		}
	}
	s.feeds[target] = f
	if target != url {
		return target, nil
	}
	return "", nil
}

// RecordFailure increments failure state and schedules a backed-off retry.
func (s *InMemoryStore) RecordFailure(_ context.Context, url string, _ core.Category, msg string, at, nextDue time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[url]
	if !ok {
		return nil
	}
	f.FailureCount++
	f.LastError = msg
	errAt, nd := at, nextDue
	f.LastErrorAt = &errAt
	f.NextDueAt = &nd
	f.UpdatedAt = s.now()
	s.feeds[url] = f
	return nil
}

// UpsertItems inserts items for a feed, returning only those whose
// (feed_url, dedup_key) was previously absent. A live row's mutable content is
// refreshed; a tombstoned row is left untouched so a pruned item is never
// resurrected or re-emitted as new.
func (s *InMemoryStore) UpsertItems(_ context.Context, feedURL string, items []core.Item) ([]core.Item, error) {
	if len(items) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items[feedURL] == nil {
		s.items[feedURL] = make(map[string]core.Item)
	}
	now := s.now()

	var newItems []core.Item
	for _, it := range items {
		it.FeedURL = feedURL
		if s.tombstones[feedURL][it.DedupKey] {
			continue // pruned fingerprint: never resurrect
		}
		if existing, ok := s.items[feedURL][it.DedupKey]; ok {
			it.FetchedAt = existing.FetchedAt
			it.Seen = existing.Seen
			s.items[feedURL][it.DedupKey] = it
			continue
		}
		if it.FetchedAt.IsZero() {
			it.FetchedAt = now
		}
		it.Seen = true
		s.items[feedURL][it.DedupKey] = it
		newItems = append(newItems, it)
	}
	return newItems, nil
}

// QueryItems returns stored, non-tombstoned items matching the query, honoring
// since/until/contains filters, ordering, pagination, and field projection. It
// also reports how many items a publication-axis date window excluded solely for
// having a null publication time.
func (s *InMemoryStore) QueryItems(_ context.Context, q core.ItemQuery) (core.ItemQueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	feedFilter := s.feedURLSetLocked(q.Feeds)
	pubWindow := q.TimeField != "fetched" && (q.Since != nil || q.Until != nil)

	var out []core.Item
	omitted := 0
	for url, byKey := range s.items {
		if feedFilter != nil && !feedFilter[url] {
			continue
		}
		for key, it := range byKey {
			if s.tombstones[url][key] {
				continue
			}
			if !matchesNonDateFilters(it, q) {
				continue
			}
			if pubWindow && it.PublishedAt == nil {
				omitted++ // dateless item dropped from a publication-axis window
				continue
			}
			if !matchesDateFilter(it, q) {
				continue
			}
			out = append(out, it)
		}
	}

	sortItems(out, q.Order)
	out = paginate(out, q.Limit, q.Offset)
	if len(q.Fields) > 0 {
		for i := range out {
			out[i] = project(out[i], q.Fields)
		}
	}
	return core.ItemQueryResult{Items: out, OmittedNoDate: omitted}, nil
}

// feedURLSetLocked resolves a list of url-or-alias references to a set of
// canonical URLs, or nil when refs is empty (match all).
func (s *InMemoryStore) feedURLSetLocked(refs []string) map[string]bool {
	if len(refs) == 0 {
		return nil
	}
	set := make(map[string]bool)
	for _, ref := range refs {
		if url, ok := s.resolveLocked(ref); ok {
			set[url] = true
		}
	}
	return set
}

// PruneItems tombstones item rows per the policy, preserving the dedup
// fingerprint, and returns the number tombstoned.
func (s *InMemoryStore) PruneItems(_ context.Context, p core.PrunePolicy) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	if p.KeepBefore != nil {
		for url, byKey := range s.items {
			for key, it := range byKey {
				if s.tombstones[url][key] {
					continue
				}
				if coalesce(it).Before(*p.KeepBefore) {
					s.tombstoneLocked(url, key)
					total++
				}
			}
		}
	}

	if p.MaxPerFeed > 0 {
		for url, byKey := range s.items {
			var live []core.Item
			for key, it := range byKey {
				if !s.tombstones[url][key] {
					live = append(live, it)
				}
			}
			sortItems(live, core.ItemOrder{Desc: true})
			for i := p.MaxPerFeed; i < len(live); i++ {
				s.tombstoneLocked(url, live[i].DedupKey)
				total++
			}
		}
	}
	return total, nil
}

// tombstoneLocked marks an item pruned and clears its body, preserving the
// dedup fingerprint.
func (s *InMemoryStore) tombstoneLocked(url, key string) {
	if s.tombstones[url] == nil {
		s.tombstones[url] = make(map[string]bool)
	}
	s.tombstones[url][key] = true
	it := s.items[url][key]
	it.ContentHTML = ""
	it.ContentText = ""
	it.Summary = ""
	s.items[url][key] = it
}

// SchemaVersion reports the applied migration version.
func (s *InMemoryStore) SchemaVersion(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.schemaVersion, nil
}

// Pending reports how many migrations have not yet been applied.
func (s *InMemoryStore) Pending(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending, nil
}

// Migrate applies pending migrations, returning the count applied.
func (s *InMemoryStore) Migrate(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	applied := s.pending
	s.schemaVersion += s.pending
	s.pending = 0
	return applied, nil
}

// Close releases resources; the in-memory store has none.
func (s *InMemoryStore) Close() error { return nil }

// coalesce returns the published time, falling back to the fetched time, which
// is the value ordering and date filters compare on.
func coalesce(it core.Item) time.Time {
	if it.PublishedAt != nil {
		return *it.PublishedAt
	}
	return it.FetchedAt
}

// matchesNonDateFilters reports whether an item satisfies the query's
// substring (and any non-date) filters, independent of the date window.
func matchesNonDateFilters(it core.Item, q core.ItemQuery) bool {
	if q.Contains != "" {
		needle := strings.ToLower(q.Contains)
		hay := strings.ToLower(it.Title + "\x00" + it.ContentText + "\x00" + it.ContentHTML)
		if !strings.Contains(hay, needle) {
			return false
		}
	}
	return true
}

// matchesDateFilter reports whether an item falls within the query's
// since/until window on the selected axis. With no window every item matches.
// On the publication axis a null-publication item inside an active window is
// already excluded and counted by the caller, so it is rejected here too.
func matchesDateFilter(it core.Item, q core.ItemQuery) bool {
	if q.Since == nil && q.Until == nil {
		return true
	}
	at := it.FetchedAt
	if q.TimeField != "fetched" {
		if it.PublishedAt == nil {
			return false
		}
		at = *it.PublishedAt
	}
	if q.Since != nil && at.Before(*q.Since) {
		return false
	}
	if q.Until != nil && at.After(*q.Until) {
		return false
	}
	return true
}

// sortItems orders items by the query's field and direction, breaking ties on
// the dedup key, mirroring the SQLite ORDER BY. On the publication axis an item
// with a null publication time sorts last under descending order and first under
// ascending order, matching SQLite's NULL-below-any-value ordering.
func sortItems(items []core.Item, o core.ItemOrder) {
	sort.SliceStable(items, func(i, j int) bool {
		if o.Field == "fetched" {
			return lessByTime(items[i].FetchedAt, items[j].FetchedAt,
				items[i].DedupKey, items[j].DedupKey, o.Desc)
		}
		return lessByPublished(items[i], items[j], o.Desc)
	})
}

// lessByPublished orders two items on the publication axis, treating a null
// publication time as smaller than any value (last under desc, first under asc).
func lessByPublished(a, b core.Item, desc bool) bool {
	an, bn := a.PublishedAt == nil, b.PublishedAt == nil
	if an || bn {
		if an != bn {
			// Null sorts smaller: under desc the non-null wins, under asc the null does.
			return an != desc
		}
		return tieByKey(a.DedupKey, b.DedupKey, desc) // both null: order by key
	}
	return lessByTime(*a.PublishedAt, *b.PublishedAt, a.DedupKey, b.DedupKey, desc)
}

// lessByTime orders by time with a dedup-key tiebreak, in the given direction.
func lessByTime(ti, tj time.Time, ki, kj string, desc bool) bool {
	if !ti.Equal(tj) {
		if desc {
			return ti.After(tj)
		}
		return ti.Before(tj)
	}
	return tieByKey(ki, kj, desc)
}

// tieByKey breaks an ordering tie on the dedup key, in the given direction.
func tieByKey(ki, kj string, desc bool) bool {
	if desc {
		return ki > kj
	}
	return ki < kj
}

// paginate applies limit and offset to an ordered slice.
func paginate(items []core.Item, limit, offset int) []core.Item {
	if offset > 0 {
		if offset >= len(items) {
			return nil
		}
		items = items[offset:]
	}
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}

// projectedFields are the agent-facing field names a query may select. Identity
// and ordering fields are always retained regardless of projection.
var projectedFields = map[string]bool{
	"id": true, "title": true, "link": true, "summary": true,
	"content_html": true, "content_text": true, "content_mime_type": true,
	"base_url": true, "author": true, "categories": true, "enclosures": true,
	"published_at": true, "updated_at": true, "fetched_at": true,
}

// project returns a copy of it carrying only the always-retained identity and
// ordering fields plus the requested projectable fields, mirroring the SQLite
// column projection.
func project(it core.Item, fields []string) core.Item {
	want := make(map[string]bool, len(fields))
	for _, f := range fields {
		if projectedFields[f] {
			want[f] = true
		}
	}
	// Always retained: feed_url, dedup_key, published_at, fetched_at.
	out := core.Item{
		FeedURL:     it.FeedURL,
		DedupKey:    it.DedupKey,
		PublishedAt: it.PublishedAt,
		FetchedAt:   it.FetchedAt,
	}
	if want["id"] {
		out.GUID = it.GUID
	}
	if want["title"] {
		out.Title = it.Title
	}
	if want["link"] {
		out.Link = it.Link
	}
	if want["summary"] {
		out.Summary = it.Summary
	}
	if want["content_html"] {
		out.ContentHTML = it.ContentHTML
	}
	if want["content_text"] {
		out.ContentText = it.ContentText
	}
	if want["content_mime_type"] {
		out.ContentMIMEType = it.ContentMIMEType
	}
	if want["base_url"] {
		out.BaseURL = it.BaseURL
	}
	if want["author"] {
		out.Author = it.Author
	}
	if want["categories"] {
		out.Categories = it.Categories
	}
	if want["enclosures"] {
		out.Enclosures = it.Enclosures
	}
	if want["updated_at"] {
		out.UpdatedAt = it.UpdatedAt
	}
	return out
}
