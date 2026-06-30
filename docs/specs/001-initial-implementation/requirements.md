# feedwatch - Requirements

## Introduction

feedwatch is an agent-first command-line tool for watching RSS and Atom feeds.
Its primary user is an AI agent rather than a human at a terminal, so every
requirement favors structured output, meaningful exit codes, deterministic and
idempotent behavior, schema discoverability, and discrete one-shot invocations
over interactive prompts or long-running processes.

feedwatch is a reliable sensor, not a brain. It fetches, parses, normalizes,
stores, deduplicates, and queries feed content, and it leaves all content
intelligence (summarizing, ranking, relevance, triage) to the calling agent.
Subscription and item state persists across invocations so that each poll
reports only what is new, while the cadence of polling is driven externally by
an agent, cron, or a system timer.

This document defines the functional and non-functional requirements for
feedwatch using EARS (Easy Approach to Requirements Syntax). It describes the
observable behavior and constraints of the tool. It does not prescribe internal
implementation beyond constraints that are themselves requirements.

## Requirements

### 1. Execution and Invocation Model

**User Story**: As an AI agent, I want feedwatch to run as discrete one-shot
commands that persist state between runs, so that I can control polling cadence
externally and rely on consistent, inspectable state.

**Acceptance Criteria**:

- The system shall execute each command as a short-lived invocation that reads
  persistent state, performs its work, and exits.
- The system shall persist subscription and item state across invocations.
- The system shall not run as a daemon and shall not enter a blocking loop.
- The system shall not initiate fetching on a schedule of its own.
- The system shall behave idempotently, such that repeating an invocation with
  unchanged inputs and state produces no additional observable effects.
- WHEN invoked, the system shall complete its work and return control without
  awaiting further interactive input.
- WHEN interrupted by SIGINT or SIGTERM during a poll, the system shall stop
  starting new fetches, persist the feeds that already completed, emit the
  result for the completed work, and exit with status 130 (SIGINT) or 143
  (SIGTERM).

### 2. Output Contract

**User Story**: As an AI agent, I want results as structured JSON on stdout and
diagnostics separated on stderr, so that I can parse output reliably without it
being polluted by logs or errors.

**Acceptance Criteria**:

- The system shall emit result data as JSON on stdout by default.
- The system shall use a consistent top-level envelope across the stdout output
  of all commands.
- The system shall write log messages and error objects as structured JSON to
  stderr.
- The system shall never write diagnostic, log, or error output to stdout.
- The system shall accept a `--format` option with the values `json` and
  `text`, defaulting to `json`.
- WHERE `--format text` is selected, the system shall render stdout as
  terminal-friendly text and shall render stderr as friendly text.
- The system shall never include color codes in JSON output.
- WHEN rendering text output to a stream, the system shall enable color only if
  that stream is a terminal, evaluating stdout and stderr independently.
- IF `--no-color` is provided, OR the `NO_COLOR` environment variable is set, OR
  the terminal type is `dumb`, THEN the system shall disable color.
- The system shall not convey information by color alone; every color-coded
  status shall also be indicated by a symbol or text.
- WHEN `--log-level` is set to error, warn, info, or debug, the system shall
  emit only log messages at or above the selected level.
- WHERE `--quiet` is provided, the system shall suppress non-error log output.

### 3. Outcome Signaling and Exit Codes

**User Story**: As an AI agent, I want distinct exit codes per outcome, so that
I can branch on the result without parsing output.

**Acceptance Criteria**:

- WHEN a command completes with full success, the system shall exit with code 0.
- IF an invocation has a usage or configuration error, THEN the system shall
  exit with code 1.
- WHEN every targeted feed fails, the system shall exit with code 2.
- WHEN some targeted feeds succeed and others fail, the system shall exit with
  code 3.
- WHEN a per-feed failure occurs, the system shall report it on stderr as a
  structured object including the feed URL, an error category, and a message.
- The system shall classify per-feed error categories as at least network, http,
  parse, and timeout.

### 4. Discoverability

**User Story**: As an AI agent, I want to discover the full command contract
programmatically, so that I do not have to guess argument shapes or output
formats.

**Acceptance Criteria**:

- The system shall provide a `schema` command that emits a machine-readable JSON
  description of every command, including its arguments, its flags with name,
  type, and default, its exit codes, and a schema for its output.
- WHEN `schema` is invoked with a command name, the system shall emit the
  description for that command only.
- The system shall support `--help` and `-h` for every command, including
  concise usage and examples.

### 5. Configuration

**User Story**: As an AI agent, I want configuration through flags and
environment variables with predictable precedence, so that current behavior is
always explicit and never hidden in a file.

**Acceptance Criteria**:

- The system shall accept configuration through command-line flags and
  environment variables only.
- The system shall not read or require a configuration file.
- The system shall resolve configuration precedence as flags first, then
  environment variables, then built-in defaults.
- The system shall use XDG-based default locations for persistent state.
- The system shall allow configuration of at least the store location, the HTTP
  user agent, the default fetch interval, the worker concurrency, the connect
  and overall per-feed timeouts, an outbound proxy, a custom certificate
  authority bundle, a minimum TLS version, the private-redirect allowance, and
  the log level.
- The system shall apply the default values listed in Appendix A when a setting
  is not explicitly configured.

### 6. State Persistence and Storage

**User Story**: As an AI agent, I want durable, self-managing state of record,
so that I can trust feedwatch as the source of truth across runs and machines.

**Acceptance Criteria**:

- The system shall persist subscriptions, per-item seen state, per-feed
  conditional-request validators, and per-feed failure counters.
- The system shall store state in a local file-based store by default.
- WHERE a remote database connection string is configured, the system shall use
  the remote store in place of the local store.
- The system shall select the storage backend from a single setting that holds
  either a filesystem path or a connection string.
- The system shall apply any pending schema upgrades automatically during an
  invocation, requiring no manual setup on a fresh machine.
- IF a schema upgrade fails, THEN the system shall abort the command, report an
  error, and leave persisted state unchanged.
- IF the persisted state was written by a newer schema version than the running
  program understands, THEN the system shall refuse to operate and report an
  error.
- The system shall provide a way to report the current schema version and the
  count of pending upgrades.
- The system shall remain durable and crash-safe under the concurrent writes of
  a multi-feed poll.

### 7. Subscription Management

**User Story**: As an AI agent, I want to subscribe to explicit feed URLs and
manage them by URL or alias, so that subscription identity is stable and
unambiguous.

**Acceptance Criteria**:

- The system shall treat the feed URL as the canonical identity of a
  subscription.
- WHEN `add` is given a feed URL, the system shall validate that the URL
  resolves to a parseable feed before subscribing.
- IF `add` is given a URL that does not resolve to a parseable feed, THEN the
  system shall reject it and direct the caller to `discover`.
- WHERE an alias is provided to `add`, the system shall associate that alias
  with the feed.
- WHERE a per-feed interval is provided to `add`, the system shall use it as
  that feed's poll interval.
- WHEN any command accepts a feed reference, the system shall resolve either the
  exact URL or a unique alias.
- WHEN `rm` is given a feed URL or alias, the system shall remove that
  subscription.
- WHEN `list` is invoked, the system shall report each subscription with its
  status, alias, failure count, and last error.
- WHEN `add` is given a URL that is already subscribed, the system shall succeed
  without creating a duplicate and shall apply any newly provided alias or
  interval to the existing subscription.

### 8. Feed Discovery

**User Story**: As an AI agent, I want a read-only way to turn a site URL into
candidate feed URLs, so that I can choose a feed to add without the tool
guessing on my behalf.

**Acceptance Criteria**:

- WHEN `discover` is given a URL, the system shall return candidate feeds without
  modifying any persisted state.
- The system shall discover candidates both from HTML autodiscovery links and by
  probing a bounded set of common feed paths.
- The system shall validate each candidate by parsing it before returning it.
- The system shall exclude non-feed content such as sitemaps from the
  candidates.
- The system shall return each candidate with its title, URL, type, and the
  discovery source that found it.
- The system shall not subscribe to any feed as part of discovery.

### 9. Polling and Deduplication

**User Story**: As an AI agent, I want polling to return only genuinely new
items and to be safely repeatable, so that I never reprocess the same item.

**Acceptance Criteria**:

- WHEN `poll` is invoked without feed arguments, the system shall fetch only the
  feeds whose interval has elapsed.
- WHILE a feed declares a time-to-live, the system shall respect it when deciding
  whether the feed is due.
- WHEN `poll` is given feed arguments, the system shall fetch those named feeds.
- WHERE `--force` or `--all` is provided, the system shall fetch regardless of
  due status.
- WHERE a feed specifies no interval and declares no time-to-live, the system
  shall treat it as due according to the default poll interval.
- The system shall identify each item by its feed URL together with a
  deduplication key.
- The system shall derive the deduplication key from the item GUID or Atom id
  when present, falling back to the item link, then the title.
- The system shall guarantee that an item whose identity was already recorded is
  not reported as new on a later poll.
- WHEN a poll encounters an item whose identity was not previously recorded, the
  system shall report it as new and mark it seen as part of the successful poll.
- WHEN a poll succeeds, the system shall persist newly seen items so they remain
  available for later querying.
- The system shall report, per poll, counts of feeds polled, feeds skipped, and
  new items.

### 10. Conditional Requests and Fetching

**User Story**: As an operator, I want efficient and safe fetching, so that
feedwatch avoids needless downloads and cannot be turned into a request-forgery
vector.

**Acceptance Criteria**:

- WHEN fetching a feed for which a validator is stored, the system shall send
  conditional-request headers.
- WHEN a server responds with 304 Not Modified, the system shall skip parsing
  and report no new items for that feed.
- The system shall update a stored validator only when its value changes.
- The system shall not overwrite a stored validator with an empty value.
- The system shall enforce on every fetch both a connect timeout that defaults
  to 5 seconds and an overall per-feed timeout that defaults to 30 seconds.
- WHEN a caller supplies a URL that resolves directly to a private, loopback, or
  link-local address, the system shall permit the request by default.
- IF a public URL redirects to a private, loopback, or link-local address, THEN
  the system shall block the request unless private redirects are allowed.
- WHEN following any redirect, the system shall re-check the resolved address
  against the private-address policy.
- WHEN a feed responds with a permanent redirect (301 or 308), the system shall
  update the stored feed URL, subject to the private-address policy.
- WHERE a proxy, a certificate authority bundle, or a minimum TLS version is
  configured, the system shall apply it to outbound requests.
- The system shall enforce a minimum TLS version that defaults to 1.2 on
  outbound HTTPS requests.

### 11. Concurrency and Politeness

**User Story**: As an operator, I want concurrent but polite fetching, so that
polling many feeds is fast without hammering any single host.

**Acceptance Criteria**:

- WHEN polling multiple due feeds, the system shall fetch them concurrently up
  to a configurable worker limit that defaults to 8.
- The system shall serialize requests to the same host and apply a delay between
  them that defaults to 1 second.
- The system shall aggregate results in a stable order regardless of the order
  in which fetches complete.

### 12. Item Model and Content

**User Story**: As an AI agent, I want every item normalized into a stable
schema with both clean text and raw HTML, so that I can reason over content
uniformly regardless of source format.

**Acceptance Criteria**:

- The system shall normalize every item into a stable schema with consistent
  field names.
- The system shall retain both the original HTML content and a derived plaintext
  rendition of each item.
- The system shall store each item's base URL and source content MIME type so
  that relative links can be resolved later.
- The system shall normalize all item dates to fixed-width RFC3339 UTC on write.
- IF an item's date cannot be parsed, THEN the system shall store the date as
  null and shall not fabricate a value.
- WHEN ordering or filtering by date and an item's published date is null, the
  system shall use the item's fetch time in its place.
- The system shall select item content from content:encoded or Atom content,
  falling back to the description when that content is empty.
- The system shall derive the item summary from the feed's description or
  equivalent short field.
- The system shall determine the author by cascading from the item author, to
  the feed-level author, to the Dublin Core creator.
- The system shall maintain a single internal seen state per item and shall not
  expose a read or unread layer.
- The system shall not require or permit the agent to mutate item seen state
  directly.

### 13. Querying History

**User Story**: As an AI agent, I want to re-query stored items with practical
filters, so that I can recover from crashes and apply my own processing
watermark.

**Acceptance Criteria**:

- WHEN `items` is invoked, the system shall return stored items matching the
  provided filters.
- The system shall support filtering by feed, and the feed filter shall be
  repeatable.
- The system shall support filtering by since and until time bounds.
- The system shall accept time bounds as RFC3339 timestamps or as relative
  durations.
- The system shall support a substring match over item title and content.
- The system shall support limit and offset pagination.
- The system shall support ordering by published or fetched time, in ascending
  or descending direction.
- The system shall return the full set of item fields by default.
- WHERE a field projection is provided, the system shall return exactly the
  requested fields plus the always-on `feed_url` identity field.
- IF a field projection names an unknown field, THEN the system shall reject the
  invocation with a usage error.

### 14. Parsing and Robustness

**User Story**: As an operator, I want robust parsing of real-world feeds, so
that malformed or oddly encoded feeds still yield usable items.

**Acceptance Criteria**:

- The system shall parse RSS 0.9x, RSS 1.0, RSS 2.0, Atom 1.0, and JSON Feed.
- The system shall tolerate malformed XML and extract what it can.
- The system shall resolve character encoding by the precedence of byte-order
  mark, then XML declaration, then HTTP Content-Type charset, then a lossy
  UTF-8 fallback.
- The system shall resolve CDATA sections and character entities when storing
  content.
- IF a feed cannot be parsed, THEN the system shall record a parse failure for
  that feed and continue processing other feeds.

### 15. Failure Handling and Feed Lifecycle

**User Story**: As an operator, I want transient errors absorbed and persistently
failing feeds backed off and disabled, so that a healthy feed is not penalized
for a blip and a dead feed does not waste every poll.

**Acceptance Criteria**:

- WHEN a transient network error occurs during a fetch, the system shall retry
  up to 3 times within the same invocation before recording a failure.
- The system shall classify connection resets, timeouts, temporary DNS failures,
  5xx responses, and 429 responses as transient.
- WHEN a 429 response includes a Retry-After value, the system shall honor it.
- The system shall not retry deterministic errors such as 4xx responses other
  than 429, parse failures, or blocked URLs.
- WHEN a feed fails after in-call retries are exhausted, the system shall
  increment its persisted failure count and record the last error and the time
  it occurred.
- WHILE a feed is within its backoff window, the system shall skip it during
  due-only polls until the backoff elapses.
- The system shall increase the backoff duration as consecutive failures
  accumulate.
- WHEN a feed reaches a configurable consecutive-failure threshold that defaults
  to 10, the system shall disable it and skip it during polls.
- WHEN a feed is fetched successfully, the system shall reset its failure count
  to zero and clear its last error.
- The system shall surface disabled feeds in `list` with their failure count and
  last error.
- WHEN `enable` is invoked for a feed, the system shall clear its disabled state
  and resume polling it.
- WHEN `disable` is invoked for a feed, the system shall skip that feed during
  polls until it is re-enabled.

### 16. Retention

**User Story**: As an operator running feedwatch over many feeds long term, I
want optional, explicit pruning of stored history, so that storage does not grow
without bound while dedup stays correct.

**Acceptance Criteria**:

- The system shall retain all stored items indefinitely by default.
- The system shall not prune stored items automatically.
- WHEN `prune` is invoked with an age limit, the system shall delete items older
  than that limit.
- WHEN `prune` is invoked with a per-feed count limit, the system shall delete
  the oldest items beyond that count for each feed.
- The system shall preserve each pruned item's deduplication fingerprint so that
  a pruned item still advertised by a feed is not reported as new.

### 17. OPML Interoperability

**User Story**: As an AI agent, I want to move subscription lists in and out via
OPML, so that feedwatch interoperates with other readers.

**Acceptance Criteria**:

- WHEN `import` is given an OPML outline, the system shall add each feed found at
  any nesting depth.
- The system shall read each feed's xmlUrl, falling back to url when xmlUrl is
  absent.
- WHERE an outline text or title is available and free as an alias, the system
  shall assign it.
- WHEN import encounters a feed that is already subscribed, the system shall skip
  it and report it as a duplicate.
- IF an individual entry fails to import, THEN the system shall continue
  importing the remaining entries and report the failed entry.
- The system shall report per-entry import results as JSON.
- WHEN `export` is invoked, the system shall write the current subscriptions and
  their aliases as valid OPML 2.0.
- The system shall support importing from a file or standard input, and
  exporting to a file or standard output.

### 18. Scheduling

**User Story**: As an operator, I want cadence controlled externally, so that
feedwatch stays a simple sensor and integrates with any scheduler.

**Acceptance Criteria**:

- The system shall rely on an external scheduler such as an agent, cron, or a
  system timer to determine poll cadence.
- The system shall not perform scheduling internally.
- The system shall support being driven by repeated one-shot invocations whose
  output can be appended to a log or pipeline.

### 19. Command Surface

**User Story**: As an AI agent, I want a flat, predictable command surface, so
that I can learn and target commands without navigating nested hierarchies.

**Acceptance Criteria**:

- The system shall expose a flat set of verb subcommands with no nesting.
- The system shall provide commands for adding, removing, and listing
  subscriptions; polling; querying items; pruning history; discovering feeds;
  enabling and disabling feeds; importing and exporting OPML; inspecting and
  applying schema migrations; and emitting the interface schema.
- WHEN version information is requested via `--version`, the system shall print
  it as JSON.
- WHERE shell completion is requested for a supported shell, the system shall
  emit a completion script for that shell.

### 20. Constraints and Non-Functional Requirements

**User Story**: As an operator, I want feedwatch to be deterministic,
self-contained, and free of external service dependencies, so that it is cheap,
testable, and reproducible.

**Acceptance Criteria**:

- The system shall not call external LLM or AI services.
- The system shall not require API keys or network credentials for its core
  operation.
- The system shall not fetch feeds that require authentication; feeds behind
  HTTP authentication, access tokens, or private cookies are out of scope.
- The system shall behave deterministically given the same inputs and persisted
  state.
- The system shall be distributable as a self-contained binary that requires no
  external language runtime or compiler at run time.
- The system shall keep runtime dependencies minimal.
- The system shall construct persisted queries without interpolating
  unsanitized input.
- The system shall operate on a fresh machine with no manual setup beyond
  invocation.

## Appendix A: Default Values

These defaults apply when the corresponding setting is not overridden by a flag
or environment variable.

| Setting                             | Default                         |
| ----------------------------------- | ------------------------------- |
| Worker concurrency                  | 8                               |
| Default poll interval               | 1 hour                          |
| Connect timeout                     | 5 seconds                       |
| Overall per-feed timeout            | 30 seconds                      |
| Per-host politeness delay           | 1 second                        |
| In-call transient retry attempts    | 3                               |
| Consecutive failures before disable | 10                              |
| Initial failure backoff             | the feed's poll interval        |
| Backoff growth                      | doubles per consecutive failure |
| Maximum failure backoff             | 24 hours                        |
| Item retention                      | unlimited (pruning is manual)   |
| Minimum TLS version                 | 1.2                             |
| Private-address redirects           | blocked                         |
