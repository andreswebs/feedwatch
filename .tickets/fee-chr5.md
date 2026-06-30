---
id: fee-chr5
status: closed
deps: [fee-qnv1, fee-4m17]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [core]
---

# Interface keystone: Store, Parser, Fetcher, Clock

Define the narrow behavioral interfaces and shared request/result types that let
the lanes and commands build against mocks before real implementations exist.
This is the fan-out keystone for parallel work. Refs: docs/cli-design.md
(Architecture, State and Storage, Parsing and Robustness, Fetching and HTTP).

## Design

Interfaces live in their consumer-facing packages; this ticket lands the
signatures and the shared value types. Concrete implementations come in
E2/E3/E4.

Shared query/value types in `src/internal/core`:

```go
type ListFilter struct{ Status FeedStatus } // "" = any
type ItemOrder struct {
    Field string // "published" | "fetched"
    Desc  bool
}
type ItemQuery struct {
    Feeds    []string // url or alias; empty = all
    Since    *time.Time
    Until    *time.Time
    Contains string
    Limit    int
    Offset   int
    Order    ItemOrder
    Fields   []string // projection; empty = all
}
type PrunePolicy struct {
    KeepBefore *time.Time
    MaxPerFeed int
}
type FetchRequest struct{ URL, ETag, LastModified string }
type FetchResult struct {
    NotModified  bool   // 304
    Status       int
    FinalURL     string // after redirects (for 301/308 rewrite)
    Permanent    bool   // final hop reached via 301/308
    ETag         string
    LastModified string
    Body         []byte // decoded to UTF-8
    MIMEType     string
}
type Clock func() time.Time

var SystemClock Clock = time.Now
```

`src/internal/store`:

```go
type Store interface {
    AddFeed(ctx context.Context, f core.Feed) (core.Feed, error) // upsert
    RemoveFeed(ctx context.Context, ref string) error
    GetFeed(ctx context.Context, ref string) (core.Feed, error)
    ListFeeds(ctx context.Context, f core.ListFilter) ([]core.Feed, error)
    DueFeeds(ctx context.Context, now time.Time) ([]core.Feed, error)
    SetStatus(ctx context.Context, url string, s core.FeedStatus) error
    SetValidators(ctx context.Context, url, etag, lastModified string) error
    RecordSuccess(ctx context.Context, url string, fetchedAt, nextDue time.Time) error
    RecordFailure(ctx context.Context, url string, cat core.Category, msg string, at, nextDue time.Time) error
    UpsertItems(ctx context.Context, feedURL string, items []core.Item) (newItems []core.Item, err error)
    QueryItems(ctx context.Context, q core.ItemQuery) ([]core.Item, error)
    PruneItems(ctx context.Context, p core.PrunePolicy) (deleted int, err error)
    SchemaVersion(ctx context.Context) (int, error)
    Migrate(ctx context.Context) (applied int, err error)
    Close() error
}
```

`src/internal/parse`:

```go
type ParsedFeed struct {
    TTL   time.Duration
    Items []core.Item
}
type Parser interface {
    Parse(ctx context.Context, body []byte, baseURL string) (ParsedFeed, error)
}
```

`src/internal/fetch`:

```go
type Fetcher interface {
    Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error)
}
```

TDD plan (declarations; behavior tests live with impls):

1. (tracer) a hand-written `fakeStore` satisfies `store.Store` (compile-time:
   `var _ store.Store = (*fakeStore)(nil)`).
2. `fakeFetcher` and `fakeParser` satisfy their interfaces.
3. Clock is injectable: a fixed clock returns a constant time.

Deep-module note: interfaces are narrow and consumer-shaped; no method exists
without a known caller. The fakes seeded here become the basis of the E9
harness.

## Acceptance Criteria

- Store, Parser, Fetcher interfaces and Clock defined with the signatures above;
  shared types in `core`.
- Compile-time conformance for hand-written fakes (behaviors 1-3).
- Interfaces reference only `core` types; the dependency graph stays acyclic.
- `make validate` passes.

## Notes

**2026-06-29T20:28:07Z**

Landed the interface keystone. Shared value types in core: ListFilter, ItemOrder, ItemQuery, PrunePolicy (core/query.go), FetchRequest/FetchResult (core/fetch.go), Clock + SystemClock (core/clock.go). Interfaces: store.Store (internal/store/store.go), parse.Parser + ParsedFeed (internal/parse/parse.go), fetch.Fetcher (internal/fetch/fetch.go). Each interface pkg has a _test.go with a hand-written fake and a compile-time 'var_ Iface = (\*fake)(nil)' conformance check; these fakes are the seed for the E9 test harness (fee-zuz2). go list -deps confirms store/parse/fetch import only core -> graph acyclic. Note: store.Store signatures use core.FeedStatus/Category directly; SetValidators takes (url, etag, lastModified) and impls must skip empty validators per cli-design. make build green.
