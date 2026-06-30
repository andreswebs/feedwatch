---
id: fee-n6j6
status: closed
deps: [fee-j4w1]
links: []
created: 2026-06-30T03:43:07Z
type: bug
priority: 2
tags: [http]
---

# fetch: permanent redirect (301/308) does not rewrite stored feed URL

A permanent redirect (301/308) is followed during `poll` but the stored feed URL is not rewritten to the redirect target. Found during manual QA (TC-FETCH-007); see `docs/qa.result.bak.md` BUG-004.

## Design

Expected (REQ 10): after a 301/308 to a new feed URL, the stored subscription URL is updated to the redirect target (subject to the private-address policy).

Observed: `poll` follows the redirect and fetches items (`polled:1, new_items:3`), but `list` still shows the original redirecting URL, for both 301 and 308.

```sh
feedwatch --db "$DB" add "$FIX/redirect?to=$FIX/feeds/rss20.xml&code=308" --alias r
feedwatch --db "$DB" poll --all                       # polled 1, new_items 3
sqlite3 "$DB" "SELECT url FROM feeds;"                 # still the /redirect?... URL
```

The fetch layer already detects permanent redirects (`internal/fetch` unit test `TestCheckRedirectRecordsPermanent` passes); the gap is that `poll`/store never persists the final URL. Wiring the recorded permanent-redirect target back into the `feeds.url` update on a successful poll should close it.

## Acceptance Criteria

- After a 301 or 308 to a new feed URL, `feedwatch list` shows the stored URL updated to the redirect target.
- The rewrite respects the private-address policy (no rewrite into blocked private space unless `--allow-private`).
- Temporary redirects (302/307) do not rewrite the stored URL.

## Implementation Plan

Decision (confirmed): fold the permanent-redirect URL rewrite into `RecordSuccess` so the rewrite and the success bookkeeping commit in one transaction; if the redirect target already exists as another subscription, skip the rewrite (no merge).

The fetch layer already records the target: `core.FetchResult.FinalURL` and `core.FetchResult.Permanent` (`src/internal/core/fetch.go`), set by the SSRF `checkRedirect` hook for `301`/`308` only. The private-address policy is enforced upstream (a redirect into blocked private space fails the fetch, so it never reaches the success path), and `302`/`307` set `Permanent=false`, so both acceptance criteria hold for free.

1. Widen `RecordSuccess` to carry the final URL. Update the `Store` interface (`src/internal/store/store.go`), the `RecordSuccess` helper (`src/internal/poll/lifecycle.go`), and the sqlite implementation (`src/internal/store/sqlite/feeds.go`):

   ```go
   RecordSuccess(ctx context.Context, url string, fetchedAt, nextDue time.Time, finalURL string) error
   ```

   When `finalURL == "" || finalURL == url`, behavior is identical to today. Otherwise, within the existing success transaction, update `feeds.url` and cascade to `items.feed_url` (the schema has `ON DELETE CASCADE` but no `ON UPDATE CASCADE`, so both rows are updated in the same transaction). If the target URL already exists as a different feed (primary-key conflict), skip the rewrite and keep the original URL.

2. In `consumeSuccess` (`src/internal/poll/consume.go`), pass the rewrite target only for permanent redirects:

   ```go
   finalURL := ""
   if oc.result.Permanent && oc.result.FinalURL != "" && oc.result.FinalURL != oc.feed.URL {
       finalURL = oc.result.FinalURL
   }
   // existing SetValidators / UpsertItems stay keyed on oc.feed.URL;
   // RecordSuccess performs the rename atomically as its final step.
   ```

   Because `RecordSuccess` runs last and renames inside its own transaction, the earlier `SetValidators`/`UpsertItems` writes (keyed on the original URL) stay correct, and the fragile mid-loop mutation of `oc.feed.URL` is avoided. A `304` permanent redirect still flows through `consumeSuccess`, so the rewrite applies on a not-modified response too.

3. Item ordering in `Run` keys by the original `feed.URL`; since the rename is the last write and the in-memory `oc.feed.URL` is not mutated, item assembly is unaffected.

Verification:

- Tests: after a `301` and a `308` to a new URL, `list` shows the updated URL; a `302`/`307` does not rewrite; a redirect whose target is already subscribed skips the rewrite; items stored under the old URL remain queryable under the new URL.
- `make build` green; learnings entry.

## Notes

**2026-06-30T03:43:07Z**

Source: manual QA report docs/qa.result.bak.md BUG-004 (TC-FETCH-007). Severity Medium, Priority P1.

**2026-06-30T13:59:35Z**

Folded permanent-redirect URL rewrite into RecordSuccess (now takes finalURL string). consumeSuccess passes the target only when result.Permanent && FinalURL != '' && FinalURL != feed.URL, so 301/308 rewrite and cascade items in one tx; 302/307 do not; private-address policy holds for free (SSRF guard fails the fetch before the success path). A target already subscribed skips the rewrite (no merge). SQLite rename needs PRAGMA defer_foreign_keys=ON inside the tx because the items->feeds FK is immediate and not DEFERRABLE. Note: the poll envelope's items[].feed_url shows the old URL for the rewriting poll (assembled in-memory pre-rename); subsequent items queries return them under the new URL. Tests: store-level rename/skip/same-url; poll-level permanent/temporary/same/304. make build green; manual QA against qafixtures 308 + 302 confirms all 3 acceptance criteria. Unblocks fee-fz8p.
