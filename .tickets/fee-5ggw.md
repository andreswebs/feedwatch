---
id: fee-5ggw
status: closed
deps: []
links: []
created: 2026-09-30T19:40:13Z
type: feature
priority: 2
assignee: Andre Silva
tags: [cmd, poll]
---
# poll: --fields projection for new items

Field report from a daily cron script: one `poll` run emitted about 1.1 MB, because the envelope carries the full `content_html` and `content_text` of every new item. `items` already accepts `--fields`, but `poll` does not, so a scheduled caller cannot shrink the payload at the source. The reporter estimates that a projection would cut cron payload size by about 10x (not measured).

Workaround available today: pipe through `jq` to keep only the wanted item keys, or discard the poll items and run `items --since "$POLL_START" --time-field fetched --fields title,link,summary` afterwards.

## Design

Add opt-in `--fields` to `PollRequest`, mirroring `ItemsRequest`. Keep the full item as the default: a summary-only default would silently change the envelope for existing consumers (and the daemon/library embedders), which is a breaking change.

- Validate names through the same path as `items` (`core.ValidItemFields`, `feed_url` accepted as a no-op, `unknownFieldMessage` for did-you-mean). Extract the loop in `ItemsRequest.query` into a shared helper so the two commands cannot drift.
- `App.Poll` keeps returning the concrete `PollResult`; add `PollRequest.Envelope(PollResult) any` that returns a projected envelope when `Fields` is set, mirroring `ItemsRequest.Envelope`. The projected form keeps every count, `failures`, and `renamed` unchanged and replaces `items` with `[]map[string]any` built by `core.ProjectItem`.
- The CLI poll action renders `req.Envelope(res)`, including on the mid-persist partial-result path, so a partial envelope is projected too.
- Help text enumerates `core.ItemFieldNames()` via `withUsage`, as `items` does.
- `schema poll` must describe both shapes; follow how the projected items envelope is expressed (`jsonschema:"opaque"`).
- Projection is output-only: storage and dedup are unaffected.

## Acceptance Criteria

- `poll --fields title,link` returns items with only `feed_url`, `title`, `link`; counts, `failures`, and `renamed` are unchanged.
- `poll` without `--fields` emits byte-identical output to before.
- An unknown field is a usage error (exit 64) with a did-you-mean suggestion, raised before any fetch.
- A mid-persist partial result is projected as well.
- `schema poll` reflects the projected shape; `docs/usage.md`, `docs/library.md`, and `CHANGELOG.md` (Unreleased, Added) are updated.
- `make build` passes.

## Notes

**2026-09-30T19:49:46Z**

Implemented. schema poll left as the reflected full PollResult (items properties are optional, so projected rows conform), matching items; see learnings. Smoke on QA fixtures: full poll 2335 bytes, --fields title,link 709 bytes; stored items keep full content.
