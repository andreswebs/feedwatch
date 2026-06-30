# Ticket Workflow

Work is driven by tickets, managed with the `tk` CLI. Files live in `.tickets/`.

```sh
tk ls                    # List all tickets
tk ready                 # List tickets with all deps resolved (ready to start)
tk blocked               # List tickets blocked by unresolved deps
tk show <id>             # Show full ticket details
tk dep tree <id>         # Show dependency tree
tk start <id>            # Mark as in_progress
tk close <id>            # Mark as closed
tk add-note <id> "..."   # Append a note
```

## Ticket Markdown

Ticket bodies are Markdown and must lint clean. When creating or editing a
ticket:

- Wrap every code element in backticks: fenced blocks (` ``` `) for signatures,
  structs, SQL, DSNs, flag lists, and multi-line snippets; inline code for
  identifiers, especially any token containing `*`, `_`, `<...>`, or `[...]`
  (for example `` `*Store` ``, `` `<shell>` ``). Unfenced code is both a lint
  failure and gets silently corrupted by `--fix` (spaces stripped around `*`,
  characters escaped).
- After writing, fix then validate with the configured linter:

  ```sh
  markdownlint-cli2 --fix '.tickets/*.md'
  markdownlint-cli2 '.tickets/*.md'
  ```

  The second command must report `0 error(s)` before the ticket is considered
  ready.
