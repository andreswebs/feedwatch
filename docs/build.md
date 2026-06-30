# Build and Validation

All commands run from the project root via `make`. Go source lives under `src/`.

| Command          | Purpose                                                                         |
| ---------------- | ------------------------------------------------------------------------------- |
| `make build`     | Full build — runs fmt-check, vet, lint, test, clean, then compiles to `bin/app` |
| `make run`       | Run the server directly with `go run`                                           |
| `make test`      | Run all tests (`go test ./...`)                                                  |
| `make test-race` | Run tests with the race detector                                                |
| `make vet`       | Run `go vet ./...`                                                               |
| `make fmt`       | Format all Go source with `gofmt -w`                                             |
| `make fmt-check` | Fail if any files are not formatted                                             |
| `make lint`      | Run `golangci-lint` (depends on `vet`)                                           |
| `make clean`     | Remove build artifacts from `bin/`                                              |

## The quality gate

After any code change, run `make build` before considering the task complete. It
enforces, in order: `fmt-check`, `vet`, `lint` (golangci-lint), `test`, then
`clean` + compile to `bin/app`. If any step fails, fix the underlying issue
rather than working around it.

Do not silence lint errors with `_ =`; handle them properly (log, return, or
assert in tests). When a lint exception is genuinely warranted, use a targeted
`//nolint:<linter> // <reason>` with a justifying comment.

For a quicker feedback loop during development use `make test` or `make lint`
individually, but always finish with a full `make build`.

Note that `golangci-lint` here is v2 (formatters live under a separate
`formatters:` block, not `linters.enable`) and runs `gosec`, `errcheck`,
`errorlint`, `revive`, and `staticcheck` over test files too. See
[learnings.md](specs/001-initial-implementation/learnings.md) for the specific traps these have surfaced.
