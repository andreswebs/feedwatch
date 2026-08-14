# Embedding feedwatch as a library

feedwatch is a Go library with a command-line frontend, not a CLI with a library
extracted from it. Everything the `feedwatch` binary does, it does by calling
`App` methods and rendering the result types they return, so an embedded program
gets exactly the CLI's behavior without shelling out or parsing JSON.

This document is task-oriented: how to construct an `App`, which method serves
which use case, how to handle failures, and how to replace the storage backend or
run background polling in-process. The reference for every type and method is the
godoc:

```sh
go doc github.com/andreswebs/feedwatch
go doc -all github.com/andreswebs/feedwatch/core
```

For the rationale behind the layering, see
[ADR 0007](adr/0007-library-and-frontends.md).

## The public surface

Four packages are public. Everything under `internal/` is not.

| Package            | Contents                                                                          |
| ------------------ | --------------------------------------------------------------------------------- |
| `feedwatch`        | `App`, `New`, the options, `Config`, the request and result types, `Head`          |
| `feedwatch/core`   | domain types (`Feed`, `Item`, `Enclosure`), the error taxonomy, `FeedError`        |
| `feedwatch/store`  | the `Store` interface: the backend extension point                                |
| `feedwatch/daemon` | `Scheduler`, the embeddable poll loop                                             |

The stability commitment these packages carry is stated once, in the `feedwatch`
package documentation under "Stability". In short: the four packages are
supported, `internal/` is not, the Go API may change in a minor release while the
project is pre-1.0, and the JSON output contract is versioned separately by
`SchemaVersion`.

## Constructing an App

`New` takes a resolved `Config` and any number of options. `Defaults()` is the
same default set the CLI starts from, so an embedder inherits the documented
defaults rather than restating them.

```go
cfg := feedwatch.Defaults()
cfg.Store = filepath.Join(dir, "feedwatch.db")

app, err := feedwatch.New(cfg)
if err != nil {
	return err
}
defer func() { _ = app.Close() }()
```

No library package reads flags or environment variables. Assembling a `Config`
from an input surface is the frontend's job: the CLI overlays flags on top of
environment variables on top of `Defaults()`, and an embedder overlays whatever it
likes.

### Lifecycle

- `New` validates the configuration and performs **no I/O**. No store is opened,
  no directory created, no request made.
- The store opens on the first use case that needs one, and pending migrations
  are applied once per `App` at that moment. A fresh machine needs no setup step,
  and a use case that touches no store (`Discover`) creates no database file.
- `Close` releases only what the `App` opened. A store passed to `WithStore`
  belongs to the caller and is never closed by the `App`.
- `Close` is idempotent, so deferring it is always safe.
- An `App` is safe for concurrent use. Collaborators are resolved once, under a
  mutex, and shared across use cases.

## Use cases

One method per use case, each taking a context and a request and returning a
result and an error.

| Use case                       | Method              | Request            | Result                                  |
| ------------------------------ | ------------------- | ------------------ | --------------------------------------- |
| Subscribe to a validated feed  | `Add`               | `AddRequest`       | `AddResult`                             |
| Unsubscribe, cascading items   | `Remove`            | `RemoveRequest`    | `RmResult`                              |
| List subscriptions and status  | `List`              | `ListRequest`      | `ListResult`                            |
| Poll due (or named) feeds      | `Poll`              | `PollRequest`      | `PollResult`                            |
| Query stored item history      | `Items`             | `ItemsRequest`     | `ItemsResult`/`ProjectedItemsResult`    |
| Trim stored history            | `Prune`             | `PruneRequest`     | `PruneResult`                           |
| Probe feeds without writing    | `Check`             | `CheckRequest`     | `CheckResult`                           |
| List candidate feeds for a URL | `Discover`          | `DiscoverRequest`  | `DiscoverResult`                        |
| Re-enable a disabled feed      | `Enable`            | `EnableRequest`    | `EnableResult`                          |
| Disable a feed manually        | `Disable`           | `DisableRequest`   | `DisableResult`                         |
| Read or edit one feed's tags   | `Tag`               | `TagRequest`       | `TagResult`                             |
| List tags with feed counts     | `Tags`              | `TagsRequest`      | `TagsResult`                            |
| Subscribe from an OPML outline | `Import`            | `ImportRequest`    | `ImportResult`                          |
| Render subscriptions as OPML   | `Export`            | `ExportRequest`    | `ExportResult`                          |
| Apply pending migrations       | `Migrate`           | none               | `MigrateApplied`                        |
| Inspect migration state        | `MigrationStatus`   | none               | `MigrateStatus`                         |

Every request type carries a `Validate() error` method holding the enum checks,
cross-field rules, and relative-time parsing for that use case. The use cases
validate their own requests, so calling `Validate` yourself is optional; a
frontend that wants to reject bad input before doing any work calls it early.

### The result types are the output contract

Each result embeds `Head`, so `schema_version` and `ok` lead the JSON, and its
`MarshalJSON` coalesces owned collections to `[]` rather than `null`. Marshalling
a result yields byte-for-byte what the CLI prints. `PollResult` and `CheckResult`
also expose `ExitCode()`, the aggregate outcome code (0, 2, or 3) the CLI returns.

`ExportResult` is deliberately not an envelope: the OPML document is the payload,
and where it lands is the frontend's decision. The library performs no filesystem
I/O of its own.

### Subscribe, then poll

```go
if _, err := app.Add(ctx, feedwatch.AddRequest{
	URL:   "https://blog.go.dev/feed.atom",
	Alias: "godev",
}); err != nil {
	return err
}

res, err := app.Poll(ctx, feedwatch.PollRequest{})
if err != nil && res.Polled == 0 {
	return err
}
for _, item := range res.Items {
	fmt.Printf("%s\t%s\n", item.Title, item.Link)
}
```

`Add` reaches the network: it proves the URL fetches and parses as a feed before
recording it, so a subscription always names something feedwatch could read.
`Poll` returns only the items it had never seen before and marks them seen, so an
immediate second poll returns none. The full versions of these snippets are the
runnable examples in `example_test.go` (`ExampleApp_Add`, `ExampleApp_Poll`).

### Querying history with a projection

```go
req := feedwatch.ItemsRequest{
	Since:     "7d",
	TimeField: "fetched",
	Order:     "published desc",
	Limit:     50,
	Fields:    []string{"title", "link", "published_at"},
}
res, err := app.Items(ctx, req)
if err != nil {
	return err
}

switch env := req.Envelope(res).(type) {
case feedwatch.ProjectedItemsResult:
	fmt.Println("projected items:", len(env.Items))
case feedwatch.ItemsResult:
	fmt.Println("full items:", len(env.Items))
}
```

`Items` always returns the full `ItemsResult`, so the method signature stays
concrete. When a caller asked for a projection, `ItemsRequest.Envelope` selects
the narrowed shape; it is the single place that choice is made, so no frontend
inspects `Fields` itself. Reading `res.Items` directly and ignoring `Envelope` is
equally valid for a program that does not serialize the result.

The filter axis and the sort axis are independent: `TimeField` chooses which time
the `Since`/`Until` window matches, `Order` chooses which time the results are
sorted by. On the publication axis, items with a null `published_at` are excluded
from a date window and counted in `OmittedNoDate` rather than silently dropped.

### Selecting a lane

A lane is a set of feeds sharing a tag. `ListRequest`, `PollRequest`,
`CheckRequest`, `ItemsRequest`, `PruneRequest`, `RemoveRequest`, and
`ExportRequest` each carry `Tags []string` and `Match string`; `AddRequest` and
`TagRequest` carry the tags to write. `Match` is a per-request field rather than
a global setting because it is meaningless for `Add`, `Tag`, `Discover`, and
`Migrate`, and keeping it on the request types is what lets the CLI surface stay
a projection of the library API.

```go
res, err := app.List(ctx, feedwatch.ListRequest{
	Tags:  []string{"ai", "agents"},
	Match: string(core.MatchAny),
})
```

`core.CanonicalTags` is the canonicalization every write goes through (trim,
lowercase, deduplicate, sort), `core.ValidateTags` rejects an empty tag or one
containing a comma or whitespace as a usage-category error, and
`core.ParseTagMatch` resolves a `Match` string, treating `""` as
`core.MatchAll`. A request's `Validate` calls them, so an embedder gets the same
exit-64 rejections the CLI does without restating the rules.

## Error model

A returned error is a **whole-invocation failure**. Recover the structure with
`errors.As` and branch on `Category`; never match a message string.

```go
var ferr *core.FeedError
if errors.As(err, &ferr) {
	switch ferr.Category {
	case core.CatUsage:
		http.Error(w, ferr.Detail(), http.StatusBadRequest)
	case core.CatStore:
		http.Error(w, ferr.Detail(), http.StatusServiceUnavailable)
	default:
		http.Error(w, ferr.Detail(), http.StatusInternalServerError)
	}
}
```

`*core.FeedError` also exposes `Code()` (a stable machine string), `ExitCode()`
(the `sysexits.h` code from [ADR 0001](adr/0001-exit-code-taxonomy.md)), and
`Hint()` (a remediation suggestion), so an embedder needs no internal package to
re-encode a failure in its own terms.

| Category           | Meaning                                     | CLI exit | Typical handling                          |
| ------------------ | ------------------------------------------- | -------- | ----------------------------------------- |
| `core.CatUsage`    | bad arguments, unknown feed reference       | 64       | reject the request; report the hint       |
| `core.CatConfig`   | unusable configuration                      | 78       | fix the configuration; do not retry       |
| `core.CatStore`    | store unreachable or unusable               | 69       | treat as unavailable; retry later         |
| `core.CatInternal` | unexpected failure, including a panic       | 70       | log and report as a bug                   |
| `core.CatNetwork`  | connection-level failure (per feed)         | result   | retry later; the feed backs off itself    |
| `core.CatHTTP`     | non-success HTTP response (per feed)        | result   | inspect `Status`                          |
| `core.CatTimeout`  | connect or overall deadline expiry (per feed) | result | retry later                               |
| `core.CatParse`    | body is not a usable feed (per feed)        | result   | the feed is broken, not the invocation    |

Static whole-invocation failures are also sentinels, matched with `errors.Is`:
`core.ErrUsage`, `core.ErrConfig`, `core.ErrStoreUnavailable`,
`core.ErrSchemaTooNew`, and `core.ErrInternal`. A stored schema newer than the
binary understands is `core.ErrSchemaTooNew` (exit 65): feedwatch refuses to
operate rather than risk corrupting data written by a future version.

### Per-feed failures are result data, not errors

`Poll` and `Check` never fail an invocation because one feed did. Failed feeds
appear in the result's `Failures` list with the feed URL, the category, the HTTP
status where applicable, and a message, and the aggregate outcome is available
from `ExitCode()`.

One case returns a populated result *and* a non-nil error: a `Poll` whose store
write failed partway through. The feeds already committed are durable, so the
result is a truthful partial envelope worth rendering. `res.Polled > 0`
identifies it; `res.Polled == 0` means the failure was early and nothing was
done, so the result must not be rendered. `ExampleApp_Poll_errors` shows the
whole shape.

### Warnings

Non-fatal advisories (a feed crossing the auto-disable threshold, for one) are
delivered to the `Warner` passed to `WithWarner`, not returned. The library owns
no output stream: with no `Warner` wired, advisories are dropped. The CLI's
`Warner` renders them as NDJSON warning lines on stderr.

```go
app, err := feedwatch.New(cfg, feedwatch.WithWarner(
	func(code, message, hint string, details any) {
		log.Printf("warning: %s: %s", code, message)
	},
))
```

## Implementing a custom store

`store.Store` is the supported extension point. The shipped SQLite adapter stays
internal, so feedwatch commits to the contract without committing to the
implementation.

```go
app, err := feedwatch.New(cfg, feedwatch.WithStore(backend))
```

An injected store bypasses the `Config.Store` scheme selection entirely and stays
the caller's to close. It is still migrated the same way a store feedwatch opened
would be: a backend the embedder supplied must reach the schema version the use
cases expect, and a backend with no schema of its own returns zero from
`SchemaVersion`, `Pending`, and `Migrate`.

The behavioral contract an implementation owes (feed reference resolution, the
usage-category `GetFeed` miss, the atomic dedup upsert, dedup-preserving pruning,
and concurrency safety across distinct feeds) is documented in the `store`
package. Read it before starting: several of those points are load-bearing, and
a backend that gets the `GetFeed` miss category wrong turns a fresh subscription
into a command failure.

### Tag support in the store contract

Lane filtering is pushed into the backend rather than applied in Go over a full
read, so that `Limit`/`Offset` on an item query stay correct and a lane query
never loads every feed. That makes tags part of the `store.Store` contract:

```go
DueFeeds(ctx context.Context, now time.Time, f core.ListFilter) ([]core.Feed, error)
SetTags(ctx context.Context, url string, tags []string) error
TagCounts(ctx context.Context) ([]core.TagCount, error)
```

`DueFeeds` gained the `core.ListFilter` parameter (a breaking change for an
existing implementation), and `SetTags` and `TagCounts` are new. The behavior
each owes:

- **Canonical storage.** `SetTags` replaces a feed's whole tag set, writing an
  empty set when `tags` is empty. Add, remove, and clear semantics are the
  caller's to compute; the store only ever sees the final set. Store the
  canonical form (`core.CanonicalTags`) so the persisted bytes are stable and a
  tag comparison is a plain string comparison.
- **Filter semantics.** `core.ListFilter`, `core.ItemQuery`, and
  `core.PrunePolicy` each carry `Tags []string` and `Match core.TagMatch`. An
  empty `Tags` matches every feed. A non-empty `Tags` under `core.MatchAll` (the
  zero value) matches a feed carrying **every** named tag; under `core.MatchAny`
  it matches a feed carrying **at least one**. A feed with no tags is matched by
  no non-empty `Tags` filter. `ListFeeds`, `QueryItems`, and `PruneItems` all
  honor the filter, and `QueryItems` applies it before `Limit` and `Offset`.
- **`DueFeeds` ignores the filter's `Status`.** Only `Tags` and `Match` are
  honored, because a due feed is active by definition. This is what lets a lane
  be polled on its own schedule rather than only under `--force`.
- **`TagCounts` counts feeds of any status**, returning each distinct tag with
  the number of subscriptions carrying it, sorted by tag, so a disabled feed
  still contributes to its lane's vocabulary. A store with no tagged feeds
  returns an empty slice, not an error.
- **`AddFeed` writes tags on insert but never on the conflict-update path.** A
  re-add must preserve the stored set, so a routine re-add does not drop a feed
  out of its lanes; replacing the set is `SetTags`'s job, which `Add` calls only
  when the request named tags explicitly. That one omission is what implements
  "omitted preserves, given replaces" without the store having to distinguish an
  empty tag set from an absent one.

The test doubles feedwatch uses internally are **not published**. Publishing them
as a `feedwatchtest` conformance package is a deferred nice-to-have, contingent on
a real alternative backend existing; until then, an implementor writes their own
doubles against the documented contract.

## Embedding the daemon

`daemon.Scheduler` is a poll loop over an `App`, for a program that wants
background polling without an external timer. It is not a layer beneath the
frontends: it is another consumer of `App`, and the `App` remains the embedder's
to close.

```go
s := daemon.New(app,
	daemon.WithInterval(5*time.Minute),
	daemon.WithPollOnStart(true),
)

ctx, cancel := context.WithCancel(context.Background())
defer cancel()
go func() { _ = s.Run(ctx) }()

for ev := range s.Events() {
	if ev.Err != nil && ev.Result.Polled == 0 {
		log.Printf("poll failed: %v", ev.Err)
		continue
	}
	log.Printf("%d new item(s)", ev.Result.NewItems)
}
```

Four properties are worth knowing before wiring it in:

- **The interval is a wake cadence, not a per-feed poll interval.** The scheduler
  polls with an empty request and never forces, so the store's per-feed schedule,
  the declared TTLs, and failure backoff all stay in effect. A scheduler that
  forced every feed on every tick would hammer publishers.
- **Runs never overlap.** A tick arriving while a poll is in flight is dropped,
  never queued, so a poll slower than the cadence cannot stack runs.
- **Publishing blocks.** A slow consumer slows the scheduler rather than losing
  events, which means the consumer must drain `Events()`.
- **A poll failure does not stop the loop.** It is published in `Event.Err` and
  the next tick proceeds; whether to stop is the embedder's decision.

`WithTags` and `WithMatch` scope a scheduler to one lane, populating the
`PollRequest` it already issues rather than adding a poll path:

```go
s := daemon.New(app,
	daemon.WithInterval(5*time.Minute),
	daemon.WithTags("ai", "agents"),
	daemon.WithMatch(core.MatchAny),
)
```

Two schedulers over the same `App` can therefore watch two lanes at two
cadences against one store, which keeps deduplication global.

`Run` returns `ctx.Err()` once the context is done and closes the event channel
before returning, so a consumer ranging over `Events()` terminates. A `Scheduler`
is single-use: a second `Run` returns `daemon.ErrAlreadyRunning`.

## Where the examples live

Every use-case snippet above is abridged from a compiling example in the
repository, so it cannot drift from the API. The two illustrative fragments (the
`Warner` and the HTTP status mapping) show how a non-CLI frontend would use the
same values, and are the only lines here without an example behind them:

| Example                    | Shows                                          |
| -------------------------- | ---------------------------------------------- |
| `ExampleNew`               | construction, and that `New` performs no I/O   |
| `ExampleApp_Add`           | subscribing to a validated feed URL            |
| `ExampleApp_Poll`          | polling due feeds and reading new items        |
| `ExampleApp_Items`         | a time window, a projection, and `Envelope`    |
| `ExampleWithStore`         | injecting a custom `store.Store`               |
| `ExampleApp_Poll_errors`   | classifying failures by `core.Category`        |
| `ExampleScheduler`         | embedding the daemon and draining `Events()`   |
| `ExampleScheduler_lane`    | scoping a scheduler to one tagged lane         |

The first six are in `example_test.go` at the repository root; `ExampleScheduler`
and `ExampleScheduler_lane` are in `daemon/example_test.go`. Examples whose behavior depends on the network or
the wall clock carry no `// Output:` comment, so the toolchain compiles them
without running them.
