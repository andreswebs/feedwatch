// Package command owns the CLI surface per docs/adr/0003-cli-structure.md. The
// contract lives in run.go (Run, Deps, the exit boundary) and is framework-free;
// the replaceable urfave/cli interior (command tree, flags, Before hook, schema
// introspection) lives in root.go and the per-command files.
package command
