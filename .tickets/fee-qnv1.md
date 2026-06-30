---
id: fee-qnv1
status: closed
deps: [fee-mls1]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [core]
---

# Core domain types

Define the pure domain types in `src/internal/core` that every layer maps to and
from: Feed, Item, Enclosure, FeedStatus, with the agent-facing JSON tags. No
internal dependencies. Refs: docs/cli-design.md (Item Model and Content, State
and Storage, Failure Handling, Architecture).

## Design

Package `src/internal/core` imports no other internal package.

```go
type FeedStatus string

const (
    FeedActive   FeedStatus = "active"
    FeedDisabled FeedStatus = "disabled"
)

type Feed struct {
    URL          string        // canonical identity
    Alias        string        // optional, unique when set
    Interval     time.Duration // 0 means use the configured default
    Status       FeedStatus
    ETag         string        // conditional-GET validator
    LastModified string        // conditional-GET validator
    FailureCount int
    LastError    string
    LastErrorAt  *time.Time
    LastFetchAt  *time.Time
    NextDueAt    *time.Time
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type Enclosure struct {
    URL    string `json:"url"`
    Type   string `json:"type"`
    Length int64  `json:"length,omitempty"`
}

type Item struct {
    FeedURL         string      `json:"feed_url"`
    DedupKey        string      `json:"-"`             // internal identity
    GUID            string      `json:"id,omitempty"`  // raw guid / atom id
    Title           string      `json:"title"`
    Link            string      `json:"link"`
    Summary         string      `json:"summary,omitempty"`
    ContentHTML     string      `json:"content_html,omitempty"`
    ContentText     string      `json:"content_text,omitempty"`
    ContentMIMEType string      `json:"content_mime_type,omitempty"`
    BaseURL         string      `json:"base_url,omitempty"`
    Author          string      `json:"author,omitempty"`
    Categories      []string    `json:"categories,omitempty"`
    Enclosures      []Enclosure `json:"enclosures,omitempty"`
    PublishedAt     *time.Time  `json:"published_at"`  // null when unparseable
    UpdatedAt       *time.Time  `json:"updated_at,omitempty"`
    FetchedAt       time.Time   `json:"-"`
    Seen            bool        `json:"-"`
}
```

Notes:

- JSON tags define the agent-facing item shape (Item Model and Content).
  `published_at` has no `omitempty`, so a nil value serializes as `null` and
  absence is explicit.
- Times are `time.Time` in memory; the store normalizes to fixed-width RFC3339
  UTC. `core` holds data only; it imports neither `store` nor `output`.

TDD plan (behavior = the JSON contract, via `encoding/json` on literals):

1. (tracer) Item marshals with the documented field names; a nil `PublishedAt`
   serializes as `"published_at": null` (not omitted).
2. Enclosure with zero `Length` omits `length`; non-zero includes it.
3. FeedStatus constants marshal as `"active"` / `"disabled"`.

Deep-module note: `core` is data-only; no behavior beyond marshaling contracts.

## Acceptance Criteria

- Feed, Item, Enclosure, FeedStatus defined with the fields above.
- Item JSON matches the Item Model section (field names, null `published_at`).
- `core` imports no other internal package.
- Behaviors 1-3 covered by tests; `make validate` passes.

## Notes

**2026-06-29T20:23:31Z**

Implemented pure domain types in src/internal/core/types.go: Feed, Item, Enclosure, FeedStatus (FeedActive/FeedDisabled). JSON tags define the agent-facing item shape. Tests (types_test.go) cover the documented behaviors: (1) Item marshals with documented field names and nil PublishedAt -> null (no omitempty on published_at); internal fields DedupKey/FetchedAt/Seen are json:"-". (2) Enclosure.Length omitempty. (3) FeedStatus marshals as active/disabled. Verified core has zero internal deps via 'go list -deps'. Note: time fields (LastErrorAt/LastFetchAt/NextDueAt) are \*time.Time pointers; store layer (not core) handles RFC3339 UTC normalization. make build passes clean.
