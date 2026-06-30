# feedwatch Beta - Requirements

## Introduction

This document specifies a set of refinements to the feedwatch command-line
contract identified while operating the tool against a large subscription set.
Each refinement closes a gap between what an invocation reported and what an
agent could actually rely on: failures that were invisible on the result
stream, a freshness axis that could not be queried, an import whose success did
not imply the feeds were usable, a field projection that rejected its own
identity field, and a silent change of feed identity after a redirect.

These requirements extend the baseline defined in
[../001-initial-implementation/requirements.md](../001-initial-implementation/requirements.md)
and preserve its agent-first principles: structured output, meaningful exit
codes, deterministic and idempotent behavior, schema discoverability, and a
clean separation of results on stdout from diagnostics on stderr. Where a
requirement here changes a behavior defined in the baseline, that change is
called out, and the compatibility impact is summarized in
[Appendix A](#appendix-a-compatibility-notes).

This document describes the observable behavior and constraints of the tool. It
does not prescribe internal implementation beyond constraints that are
themselves requirements. Concrete field and flag names are stated where they
form part of the observable contract.

## Requirements

### 1. Poll Failure Visibility in the Result Envelope

Amends baseline Req 3 (Outcome Signaling) and Req 15 (Failure Handling).

**User Story**: As an AI agent, I want a poll's stdout envelope to report how
many feeds failed and which ones, so that I can detect and triage a partial
failure from a single stream without correlating it against stderr.

**Acceptance Criteria**:

- The system shall include in the `poll` result envelope a count of feeds
  successfully polled (`succeeded`) and a count of feeds that failed (`failed`).
- The system shall preserve the existing attempted-feed count (`polled`) and
  shall maintain the invariant that `polled` equals `succeeded` plus `failed`.
- The system shall include in the `poll` result envelope a `failures` list in
  which each entry identifies the feed by URL and carries its failure
  `category` and, where applicable, its HTTP `status`.
- The system shall present the `failures` list as an empty list, not as an
  absent or null field, when no feeds failed.
- IF a failure has no associated HTTP status (for example a network, parse, or
  timeout failure), THEN the system shall omit the `status` field from that
  failure entry.
- The system shall continue to write the full per-feed error detail, including
  the human-readable message, to stderr.
- The system shall not change the existing poll exit-code semantics, such that
  full success, total failure, and partial failure remain distinguished by exit
  code.

### 2. Fetch-Time Query Axis

Amends baseline Req 12 (Item Model) and Req 13 (Querying History).

**User Story**: As an AI agent, I want to see and query the time at which
feedwatch first recorded each item, so that I can reliably find what newly
arrived regardless of whether a feed declares or correctly formats publication
dates.

**Acceptance Criteria**:

- The system shall expose the time at which it first recorded each item, its
  fetch time (`fetched_at`), as a selectable item field.
- The system shall populate the fetch-time field for every stored item, such
  that the fetch time is never null.
- The system shall provide a way (`--time-field`) to select whether item
  time-window filters apply to the publication time (`published`) or the fetch
  time (`fetched`), defaulting to the publication time.
- WHEN the caller selects the fetch-time axis for a `--since` or `--until`
  filter, the system shall match items by their fetch time.
- The system shall continue to allow ordering results by either publication
  time or fetch time, independently of the selected filter axis.

### 3. Honest Handling of Missing Publication Dates

Amends baseline Req 12 (Item Model) and Req 13 (Querying History). This
requirement supersedes the baseline behavior that coalesced a null publication
time to the fetch time for filtering and ordering. It is a deliberate breaking
change to publication-axis date queries.

**User Story**: As an AI agent, I want publication-axis date-window queries to
include only items that actually carry a publication date, so that dateless
items do not masquerade as recently published.

**Acceptance Criteria**:

- WHERE an item carries no parseable publication date, the system shall store
  its publication time as null. A feed legitimately omitting a publication date
  is a valid feed, not an error.
- WHEN a `--since` or `--until` filter applies to the publication axis, the
  system shall exclude items whose publication time is null.
- The system shall not substitute the fetch time for a null publication time
  when filtering or ordering on the publication axis.
- WHEN ordering on the publication axis, the system shall place items with a
  null publication time last under descending order and first under ascending
  order.
- WHEN a publication-axis date filter excludes one or more items because their
  publication time is null, the system shall report the count of excluded items
  in the result envelope (`omitted_no_date`).
- WHEN a publication-axis date filter excludes one or more null-publication
  items, the system shall emit an informational log line on stderr stating how
  many items were excluded and on which axis.
- The system shall leave the fetch-time axis unaffected by this requirement,
  since the fetch time is never null.
- The system shall update its machine-readable schema and usage documentation
  so that the documented contract reflects the publication axis excluding
  dateless items.

### 4. Validated Import

Amends baseline Req 17 (OPML Interoperability).

**User Story**: As an AI agent, I want import to verify that each feed actually
resolves and parses by default, so that a reported successful import means the
feeds are usable rather than merely recorded.

**Acceptance Criteria**:

- WHEN importing subscriptions, the system shall by default validate each feed
  by fetching and parsing it before subscribing, applying the same validation
  the `add` command applies.
- WHILE validating an import, the system shall validate feeds concurrently,
  honoring the configured concurrency limit.
- WHILE validating an import, the system shall apply the same transient-failure
  retry policy it uses for other fetches.
- IF a feed fails validation, THEN the system shall not subscribe to it and
  shall record it among the per-entry failures with a reason.
- The system shall report the added count as the number of feeds that were
  successfully validated and subscribed.
- The system shall not abort the entire import because individual feeds fail
  validation.
- WHERE the caller skips validation (`--no-validate`), the system shall
  subscribe to each syntactically valid feed without fetching it, restoring the
  fast bulk-add behavior.
- The system shall describe import results so that a successful import does not
  imply reachability when validation was skipped.

### 5. Field Selection Ergonomics

Amends baseline Req 13 (Querying History).

**User Story**: As an AI agent, I want to name the always-present identity field
in a projection without error and to receive a helpful message when I mistype a
field name, so that field selection tolerates natural usage while still catching
real mistakes.

**Acceptance Criteria**:

- The system shall accept the always-present feed-identity field (`feed_url`)
  within a `--fields` projection and shall treat naming it as a no-op rather
  than an error.
- IF a `--fields` projection names an unrecognized field, THEN the system shall
  reject the request as a usage error and shall not return partial results.
- WHEN rejecting an unrecognized field name, WHERE that name closely resembles a
  valid field name, the system shall include a suggestion of the intended field
  in the error message.

### 6. Permanent-Redirect Rename Visibility

Amends baseline Req 9 (Polling) and Req 10 (Conditional Requests and Fetching).

**User Story**: As an AI agent, I want a poll to tell me when a feed's canonical
URL changed because of a permanent redirect, so that I learn the new identity
instead of discovering it through a later query that silently returns nothing.

**Acceptance Criteria**:

- The system shall continue to rename a feed to its canonical URL when a poll
  follows a permanent redirect (HTTP `301` or `308`), cascading the rename to
  that feed's stored items.
- WHEN a poll renames a feed following a permanent redirect, the system shall
  report the rename in the `poll` result envelope, identifying both the prior
  URL and the new URL.
- WHEN a poll renames one or more feeds, the system shall emit a corresponding
  informational log line on stderr.
- The system shall present the rename list as an empty list, not as an absent or
  null field, when no feeds were renamed.

## Appendix A: Compatibility Notes

The following requirements change behavior defined in the baseline and may
affect existing callers:

- Req 3 removes the coalescing of a null publication time to the fetch time on
  the publication axis. Publication-axis `--since` and `--until` queries that
  previously returned dateless items will no longer return them; such items
  remain reachable through the fetch-time axis defined in Req 2. The baseline
  documentation that promised the coalescing behavior is superseded.
- Req 4 makes import validate feeds by default. An import of an outline
  containing unreachable or non-feed URLs will now report a smaller added count
  and corresponding per-entry failures, where it previously reported all
  entries as added. The prior behavior is available with `--no-validate`.

The following requirements are purely additive and do not change existing
behavior: Req 1 (new envelope fields), Req 2 (new field and filter axis), Req 5
(`feed_url` accepted in projections; unknown-field errors gain a suggestion),
and Req 6 (new envelope field and log line).
