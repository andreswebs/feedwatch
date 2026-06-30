---
id: fee-h4bz
status: closed
deps: [fee-bpq3]
links: []
created: 2026-06-30T03:43:28Z
type: bug
priority: 3
tags: [cli]
---

# import: OPML accepts malformed/non-absolute feed URLs

OPML `import` accepts malformed, non-absolute feed URLs that `add` would reject, with no validation parity. Found during manual QA (TC-OPML-003); see `docs/qa.result.bak.md` BUG-007.

## Design

Observed: an outline with `xmlUrl="not-a-valid-url"` is imported as a subscription. `add` rejects the same value ("add requires an absolute http(s) feed URL"). Outlines with no `xmlUrl`/`url` are correctly reported in `failed` ("outline declares a feed type but has no xmlUrl or url"), but a malformed-but-present URL is not.

```sh
# outline: <outline type="rss" text="badurl" xmlUrl="not-a-valid-url"/>
feedwatch --db "$DB" import bad.opml          # {"added":N,...,"failed":[]}
feedwatch --db "$DB" list                     # stored url: "not-a-valid-url"
```

Low severity: import is intentionally lenient (no fetch validation), but it should at least validate URL syntax (absolute http(s)) to match `add` and route bad entries to `failed` instead of storing un-pollable URLs.

## Acceptance Criteria

- `import` routes outlines whose `xmlUrl`/`url` is not an absolute http(s) URL into the `failed` list with a clear reason.
- Valid entries continue to import; the duplicate-skip and no-url-failed behaviors are unchanged.

## Implementation Plan

Give `import` the same absolute-`http(s)` URL check `add` enforces, by extracting the predicate so both share it.

1. In `src/internal/cli/add.go`, extract the bare predicate from `validateFeedURL`:

   ```go
   func isAbsoluteHTTPURL(raw string) bool {
       u, err := url.Parse(raw)
       return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
   }
   ```

   `validateFeedURL` keeps its add-specific message but delegates the test to `isAbsoluteHTTPURL`, so `add` behavior is unchanged.

2. In `src/internal/cli/import.go` `importFeeds`, inside the `for _, feed := range feeds` loop, after the duplicate-skip check and before `AddFeed`, route malformed URLs to `failed`:

   ```go
   if !isAbsoluteHTTPURL(feed.XMLURL) {
       res.Failed = append(res.Failed, ImportFail{
           XMLURL: feed.XMLURL,
           Reason: "outline xmlUrl/url is not an absolute http(s) URL",
       })
       continue
   }
   ```

3. Ordering is deliberate: the existing duplicate-skip check stays before validation, so a duplicate (even a malformed one already stored) is still counted as `skipped`, not `failed`. Outlines with no `xmlUrl`/`url` remain handled by the existing `opml.Invalid` pre-seed into `failed`.

Verification:

- Table test in the import test: malformed `xmlUrl` routes to `failed` with the reason; a valid URL is `added`; a missing url stays `failed` (unchanged); a duplicate stays `skipped`.
- `make build` green; learnings entry.

## Notes

**2026-06-30T03:43:29Z**

Source: manual QA report docs/qa.result.bak.md BUG-007 (TC-OPML-003). Severity Low, Priority P2.

**2026-06-30T13:38:32Z**

Added URL-syntax parity between import and add. Extracted isAbsoluteHTTPURL(raw) from validateFeedURL in cli/add.go (add keeps its message, delegates the test). importFeeds now routes any present-but-non-absolute-http(s) xmlUrl into failed with reason 'outline xmlUrl/url is not an absolute http(s) URL', placed after the duplicate-skip check so duplicates still count as skipped and missing-URL outlines stay handled by opml.Invalid. New table-style test TestImportRejectsMalformedURL in cli/import_test.go; make build green.
