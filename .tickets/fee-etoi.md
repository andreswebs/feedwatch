---
id: fee-etoi
status: closed
deps: []
links: []
created: 2026-06-30T03:43:28Z
type: bug
priority: 3
tags: [cli]
---

# list: per-feed interval stored but not shown

`list` does not display the per-feed poll interval, even though it is stored and echoed by `add`. Found during manual QA (TC-SUB-003); see `docs/qa.result.bak.md` BUG-003.

## Design

Expected (TC-SUB-003): after `add <url> --interval 30m`, `list` shows the alias and the interval.

Observed: the interval is persisted (`interval_seconds=1800`) and returned by `add` (`"interval":"30m0s"`), but `list` omits the field in both JSON and text output:

```sh
feedwatch --db "$DB" add "$FIX/feeds/rss20.xml" --alias f1 --interval 30m   # {"interval":"30m0s",...}
feedwatch --db "$DB" list | jq ".feeds[0]"                                   # no interval key
sqlite3 "$DB" "SELECT interval_seconds FROM feeds;"                          # 1800 (stored)
```

Low severity: the interval is functional (drives due-ness); this is an observability gap in `list`.

## Acceptance Criteria

- `list` includes the per-feed interval in JSON output (e.g. `"interval":"30m0s"`, or `0`/null for the default).
- `--format text list` shows the interval in its column set.

## Implementation Plan

All changes are confined to `src/internal/cli/list.go`; no store or core changes are needed, because `ListFeeds` already loads `interval_seconds` and `scanFeed` sets `core.Feed.Interval`.

1. Add an `Interval` field to `FeedView`, mirroring `AddResult.Interval` in `src/internal/cli/add.go`:

   ```go
   Interval string `json:"interval,omitempty"`
   ```

2. In `listAction`, populate it from the loaded `core.Feed` with the same guard `add` uses, so a default (zero) interval is omitted from JSON and shown as `-` in text:

   ```go
   if f.Interval > 0 {
       fv.Interval = f.Interval.String()
   }
   ```

3. In `RenderText`, add an `INTERVAL` column to both the header and the row, using `dashIfEmpty(f.Interval)` so the table stays rectangular.

Verification:

- New assertions in the list test: interval present in JSON when set, absent when zero, and present in the text column.
- `make build` green; add a `docs/specs/001-initial-implementation/learnings.md` entry under this ticket's heading.

## Notes

**2026-06-30T03:43:28Z**

Source: manual QA report docs/qa.result.bak.md BUG-003 (TC-SUB-003). Severity Low, Priority P2.

**2026-06-30T13:33:25Z**

Added Interval field to FeedView in src/internal/cli/list.go: populated from core.Feed.Interval with the same '> 0' guard add uses (default/zero omitted from JSON, '-' in text). Added an INTERVAL column to RenderText. The output schema is reflection-derived from ListResult, so 'interval' now appears in list/enable/disable schemas automatically; updated feedViewProps in schema_test.go to match. New tests: TestListReportsInterval (set vs default-omitted in JSON) and TestListTextFormatShowsInterval (text column). Updated docs/usage.md list example and docs/specs/001-initial-implementation/learnings.md. make build green.
