---
id: fee-0l84
status: closed
deps: [fee-8cau, fee-2heq]
links: []
created: 2026-06-29T19:33:03Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-isds
tags: [cmd]
---

# discover command

Implement the read-only discover command: HTML autodiscovery plus a bounded common-path probe, each candidate parse-validated with a sitemap-guarding content-type check, returning title/url/type/source; no store writes. Refs: docs/cli-design.md (Feed Discovery); docs/research/urfave-cli.reference.md.

## Design

The `discover` command: read-only listing of candidate feeds for a URL.

```go
&cli.Command{ Name:"discover", ArgsUsage:"URL",
  Arguments: []cli.Argument{ &cli.StringArg{Name:"url"} }, Action: discoverAction }

type Candidate struct {
  Title string `json:"title,omitempty"`
  URL   string `json:"url"`
  Type  string `json:"type,omitempty"`
  Source string `json:"source"` // "autodiscovery" | "probe"
}
type DiscoverResult struct { Candidates []Candidate `json:"candidates"` }
```

Two stages, both read-only (no store writes):

1. Fetch the URL; if HTML, scan for `<link rel="alternate">` autodiscovery links
   (type + href), resolved against the base URL. Source `autodiscovery`.
2. Probe a bounded fixed path list against the origin: `/feed`, `/rss`,
   `/feed.xml`, `/atom.xml`, `/index.xml`. Source `probe`.

Validate every candidate by parsing it with the `parse.Parser`; drop ones that
do not parse. Content-type short-circuit: reject generic XML that is not a feed
(avoids sitemaps). Probing uses the same per-host politeness and timeouts as a
normal fetch.

TDD plan (httptest serving an HTML page with link tags + feed endpoints):

1. (tracer) a page with a `rel="alternate"` link yields that candidate with
   source `autodiscovery`.
2. a site with no link tag but a valid `/feed` yields a `probe` candidate.
3. a `/sitemap.xml`-style generic XML is not returned (content-type guard).
4. each returned candidate actually parses as a feed.
5. `discover` performs no store writes.

Deep-module note: discovery depends only on Fetcher + Parser (no store), so it is
an early, self-contained command.

## Acceptance Criteria

- `discover` returns autodiscovery + bounded-probe candidates, each parse-
  validated, with a `source` field; sitemaps rejected; read-only.
- Behaviors 1-5 covered against httptest.
- Supports Req 8. `make validate` passes.

## Notes

**2026-06-29T23:54:43Z**

Implemented discover in two layers: pure logic in internal/discover (discover.go) depending only on Fetcher+Parser (no store), wired by internal/cli/discover.go. Discover(ctx, Deps, pageURL) fetches the page; if HTML, scans `<link rel="alternate">` with a feed-type attr (rss+xml/atom+xml/feed+json/json/rdf+xml), resolving hrefs against the page; then probes /feed,/rss,/feed.xml,/atom.xml,/index.xml against the origin. Every candidate is validated: content-type short-circuit rejects text/html outright, then parse.Parser.Parse drops non-feeds (gofeed returns 'Failed to detect feed type' on a sitemap `<urlset>`, so generic XML is rejected). Autodiscovery runs first and a seen-set dedupes so an autodiscovery hit on a probe path wins. Candidates carry title/url/type/source; returned slice is always non-nil (make) so the envelope is {"candidates":[]} not null. ENABLING CHANGE: added Title to parse.ParsedFeed (populated from gofeed feed.Title in gofeed.go) since the Parser interface previously exposed no feed title and Candidate needs one for probe hits; additive, no consumer broke. Tests: internal/discover/discover_test.go behaviors 1-4 via httptest+fetch.New()+parse.New() (loopback dialed directly, no --allow-private needed); internal/cli/discover_test.go covers behavior 5 (injected InMemoryStore stays empty), empty=[] not null, and bad-URL usage error (exit 1). make build green, 0 lint. Registered in root.go commands(). Live-smoke verified JSON + --format text.
