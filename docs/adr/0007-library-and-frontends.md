# 0007: Library and frontends

## Context

feedwatch is currently one program with one frontend. Its domain is
already sliced into packages with narrow interfaces (a store, a
fetcher, a parser, a poll orchestrator) and doubles for each, but
every one of them lives under `internal/`, and the layer that
assembles them into a use case does not exist as a named thing: it is
dissolved into the CLI actions. Each action resolves collaborators,
calls the domain, shapes a result envelope, and picks an exit code, in
one function. The result envelope types themselves are declared in the
CLI package.

Two consequences follow. Nothing is importable, so feedwatch cannot be
embedded. And a second frontend (a TUI, an HTTP server, an MCP server)
would have to either import the CLI package or restate the output
contract, which is exactly the drift the output contract ADR exists to
prevent.

The CLI structure ADR already established the shape of the answer for
one frontend: a framework-free contract with the framework confined to
a replaceable interior. This ADR generalizes it. The CLI becomes one
frontend among several, and the substance moves into an importable
library.

## Decision

### Layers

1. **Library.** An application service, `App`, exposing one method per
   use case over request and result types it owns.
2. **Frontends.** Thin projections of `App`: the CLI, and later a TUI,
   an HTTP server, and an MCP server. A frontend translates its own
   input encoding into a request, calls one `App` method, and renders
   the result in its own encoding. It holds no domain logic.
3. **Daemon.** A scheduler that owns a ticker and an `App` and emits
   poll outcomes on a channel. It is not a layer under the others: it
   is another consumer of `App`, embeddable in-process by any frontend
   that wants background polling.

No frontend is privileged. The HTTP server is not the core; it is a
peer of the CLI, and both are peers of an external embedder.

### Public surface

Four packages are public. Everything else stays internal.

| Package            | Contents                                                     |
| ------------------ | ------------------------------------------------------------ |
| `feedwatch`        | `App`, its constructor and options, request and result types |
| `feedwatch/core`   | domain types, the error category taxonomy, `FeedError`       |
| `feedwatch/store`  | the `Store` interface                                        |
| `feedwatch/daemon` | the scheduler                                                |

The store, fetcher, and parser adapters, the poll orchestrator, the
discovery and OPML packages, the output renderer, and the CLI package
are all internal. An embedder implementing an alternative backend
needs the interface, not the shipped adapter.

`core` cannot be merged into the root package. Internal packages
import the domain types, and the root package imports those internal
packages, so domain types at the root would form an import cycle. The
separate package is a constraint, not a preference.

### The App

`App` holds the ports (store, fetcher, parser, clock) and the resolved
configuration, and exposes one method per use case. Each method takes
a context and a request type and returns a result type and an error:

```go
func (a *App) Items(ctx context.Context, req ItemsRequest) (ItemsResult, error)
```

The result types are the envelope structs, moved out of the CLI
package into the library. They keep their schema head, their
collection-coalescing `MarshalJSON`, and their schema reflection. They
are the contract, and every frontend renders the same values.

Errors returned from `App` carry the existing category taxonomy. A
frontend maps categories to its own failure encoding and adds no
classification of its own.

### Construction

`App` is built with a config plus functional options, matching the
option style already used by the fetcher:

```go
app, err := feedwatch.New(cfg,
    feedwatch.WithStore(customStore),
    feedwatch.WithFetcher(customFetcher),
    feedwatch.WithParser(customParser),
    feedwatch.WithClock(clk),
)
```

Omitted ports are constructed from the config, so the default path
requires no options and the shipped adapters stay internal. The
options are the supported extension point and the sanctioned test
seam; the CLI package's unexported dependency fields are retired in
favour of them.

### Requests, validation, and frontend projection

Request types are plain structs with tags describing their shape:

```go
type ItemsRequest struct {
    Feeds []string `flag:"feed"  usage:"feed url or alias to query (repeatable)"`
    Since string   `flag:"since" usage:"lower bound: RFC3339 or relative such as 24h"`
    Limit int      `flag:"limit" usage:"maximum items to return; 0 returns all"`
}
```

Frontends derive their input surface from those tags by reflection:
CLI flags and help text, HTTP query parameters and request bodies, MCP
tool input schemas. This keeps one source of truth for the shape of
every use case, and extends the existing practice of reflecting output
schemas from the result structs rather than hand-authoring them.

Reflection describes **shape** only. Semantics stay ordinary Go: each
request type carries a `Validate() error` method holding enum checks,
cross-field rules, relative-time parsing, and defaulting, and it
returns a usage-category error. Encoding semantics into tags requires
inventing a validation mini-language, which grows without bound and is
explicitly rejected here.

The reflection layer therefore reduces to a mapping from Go types to
frontend input kinds. A type absent from that mapping cannot fail at
compile time, so a table test walks every request type in the library
and asserts every field maps to a known kind. That test is mandatory:
it is what converts the one unsafe property of this approach back into
a build failure.

### Command projections are deferred

A framework-agnostic command registry, from which each frontend's
command surface is generated as a projection, is the natural endpoint
of this design and is anticipated by the CLI structure ADR. It is
deliberately **not** built now. With one frontend it has no consumer,
and its requirements cannot be known until a second projection is
real.

Until then, the registry is the `App` method set, and each frontend
wires its own commands explicitly. When it is built, one rule governs
it: a registry entry holds only what every frontend shares (name,
summary, request type, result type, handler). Anything specific to one
frontend lives in that frontend's own table. Without that rule the
entry becomes the union of every frontend's framework, which is the
failure mode this deferral exists to avoid.

### Contract testing

The existing CLI golden tests assert the stdout, stderr, and exit
triple at the framework-free boundary, and are unaffected by this
change. As frontends are added, the scenario table is parameterized by
frontend, so one set of scenarios is exercised through every
projection and asserted to produce the same envelope. That suite is
what guarantees the frontends cannot drift from one another.

## Consequences

- feedwatch becomes embeddable. A TUI, a server, an MCP server, and a
  third-party program all consume the same use cases and the same
  result types, so the output contract has exactly one definition.
- Failure encodings across frontends become projections of one
  taxonomy. The category that yields exit 64 in the CLI yields HTTP
  400 in the server, and the two cannot diverge, because neither
  classifies anything itself.
- Runtime self-description generalizes for free: the schema command
  and a server's API document are two renderings of the same
  reflection over the same result types.
- The public surface is a compatibility commitment. Four packages,
  one of which is pure data and one of which is a single interface, is
  a surface small enough to hold stable; the adapters behind it stay
  free to change.
- Alternative store backends are supported without feedwatch shipping
  them, and the doubles used internally become useful to embedders as
  conformance aids if they are later published.
- Migration is dominated by moves rather than rewrites: envelope types
  move to the library, action bodies shrink to request assembly and
  rendering, and collaborator resolution becomes the options
  constructor. The stream contract does not move, so the CLI test
  suite carries over.
- The cost paid up front is a reflection layer for request shapes and
  the table test that guards it. The cost deliberately deferred is the
  command registry, which is a bounded addition on top of a clean
  library and a poor guess before one exists.
