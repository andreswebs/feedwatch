package command

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/output"
	"github.com/andreswebs/feedwatch/internal/terr"
)

// Schema is the machine-readable description of one command: its
// positional arguments and flags (introspected from the live command tree) plus
// its exit codes and output JSON Schema (from the per-command registry, which
// holds only what introspection cannot derive). It carries the envelope head
// because schema --command CMD renders it as a top-level result; the same head
// therefore also appears on each Schema nested in a SchemaResult, which
// is self-describing and harmless.
type Schema struct {
	output.Head
	Command   string            `json:"command"`
	Args      []ArgSchema       `json:"args"`
	Flags     []FlagSchema      `json:"flags"`
	ExitCodes map[string]string `json:"exit_codes"`
	Output    json.RawMessage   `json:"output_schema"`
}

// MarshalJSON coalesces args and flags so each always serializes as [] rather
// than null.
func (c Schema) MarshalJSON() ([]byte, error) {
	type alias Schema
	a := alias(c)
	if a.Args == nil {
		a.Args = []ArgSchema{}
	}
	if a.Flags == nil {
		a.Flags = []FlagSchema{}
	}
	return json.Marshal(a)
}

// ArgSchema describes a single positional argument. Variadic is true for a
// glob argument that consumes the remaining positionals.
type ArgSchema struct {
	Name     string `json:"name"`
	Variadic bool   `json:"variadic,omitempty"`
}

// FlagSchema describes a single flag: its primary name (with leading dashes),
// any aliases, its value type, and its compiled-in default when non-zero.
type FlagSchema struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Type    string   `json:"type"`
	Default any      `json:"default,omitempty"`
}

// SchemaError describes one entry of the tool-level error inventory: a stable
// machine code, the process exit code it maps to, and its remediation hint. It
// is a projection of terr.All() (docs/adr/0005-output-contract.md and ADR
// 0002), never hand-maintained, so the documented error surface cannot drift
// from the real one.
type SchemaError struct {
	Code     string `json:"code"`
	ExitCode int    `json:"exit_code"`
	Hint     string `json:"hint,omitempty"`
}

// SchemaResult is the bare-schema envelope. Its reference core (guaranteed on
// every fleet tool, ADR 0005) is tool, version, commands, the tool-level
// exit_codes union, and the errors inventory; global_flags is a feedwatch
// enrichment, and each command's per-command exit_codes map and derived
// output_schema are carried inside Commands as additive detail.
type SchemaResult struct {
	output.Head
	Tool        string        `json:"tool"`
	Version     string        `json:"version"`
	Commands    []Schema      `json:"commands"`
	ExitCodes   []int         `json:"exit_codes"`
	Errors      []SchemaError `json:"errors"`
	GlobalFlags []FlagSchema  `json:"global_flags"`
}

// MarshalJSON coalesces every collection so each always serializes as [] rather
// than null.
func (r SchemaResult) MarshalJSON() ([]byte, error) {
	type alias SchemaResult
	a := alias(r)
	if a.Commands == nil {
		a.Commands = []Schema{}
	}
	if a.ExitCodes == nil {
		a.ExitCodes = []int{}
	}
	if a.Errors == nil {
		a.Errors = []SchemaError{}
	}
	if a.GlobalFlags == nil {
		a.GlobalFlags = []FlagSchema{}
	}
	return json.Marshal(a)
}

// schemaCommand registers the schema subcommand: emit the machine-readable
// interface contract for every command, or narrow to one named command.
func (d Deps) schemaCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "schema",
		Usage:     "emit the machine-readable interface contract",
		ArgsUsage: "[COMMAND]",
		Arguments: []cliv3.Argument{&cliv3.StringArg{Name: "command"}},
		Action:    d.schemaAction,
	}
}

// schemaAction renders the schema for the named command, or for every command
// when no name is given. An unknown command name is a usage error (exit 64).
func (d Deps) schemaAction(ctx context.Context, cmd *cliv3.Command) error {
	r := rendererFrom(ctx)
	root := cmd.Root()

	if name := cmd.StringArg("command"); name != "" {
		sub := commandByName(root, name)
		if sub == nil {
			return unknownCommandErr(name)
		}
		return r.Result(commandSchema(sub))
	}

	commands := commandSchemas(root)
	result := SchemaResult{
		Head:        output.OKHead(),
		Tool:        root.Name,
		Version:     d.Version,
		Commands:    commands,
		ExitCodes:   exitCodeUnion(commands),
		Errors:      errorInventory(),
		GlobalFlags: flagSchemas(root.Flags),
	}
	return r.Result(result)
}

// errorInventory projects the registered coded sentinels into the tool-level
// error inventory. It is a projection of terr.All(), not a literal, so a newly
// registered code appears with no edit here (ADR 0002/0005).
func errorInventory() []SchemaError {
	all := terr.All()
	out := make([]SchemaError, 0, len(all))
	for _, e := range all {
		out = append(out, SchemaError{Code: e.Code(), ExitCode: e.ExitCode(), Hint: e.Hint()})
	}
	return out
}

// exitCodeUnion computes the tool-level exit_codes: the sorted, de-duplicated
// union of every command's declared exit-code table. It reads the same maps the
// commands report, so the union is a projection and cannot be hand-maintained
// out of sync.
func exitCodeUnion(commands []Schema) []int {
	seen := map[int]bool{}
	for _, c := range commands {
		for code := range c.ExitCodes {
			n, err := strconv.Atoi(code)
			if err != nil {
				continue
			}
			seen[n] = true
		}
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// commandByName finds a documented subcommand of root by name or alias, so
// schema narrows only to commands an agent can actually invoke.
func commandByName(root *cliv3.Command, name string) *cliv3.Command {
	for _, c := range root.Commands {
		if skipCommand(c) {
			continue
		}
		if c.HasName(name) {
			return c
		}
	}
	return nil
}

// commandSchemas introspects every documented subcommand in declaration order.
func commandSchemas(root *cliv3.Command) []Schema {
	out := make([]Schema, 0, len(root.Commands))
	for _, c := range root.Commands {
		if skipCommand(c) {
			continue
		}
		out = append(out, commandSchema(c))
	}
	return out
}

// skipCommand reports whether a command is excluded from the schema: hidden
// commands (such as the completion helper) and the framework's auto-added help
// command, which is a conventional aid rather than part of the contract.
func skipCommand(c *cliv3.Command) bool {
	return c.Hidden || c.Name == "help"
}

// commandSchema builds one command's schema from its live arguments and flags,
// augmented with its registered exit codes and output JSON Schema.
func commandSchema(c *cliv3.Command) Schema {
	meta := registryFor(c.Name)
	return Schema{
		Head:      output.OKHead(),
		Command:   c.Name,
		Args:      argSchemas(c.Arguments),
		Flags:     flagSchemas(c.Flags),
		ExitCodes: meta.exitCodes,
		Output:    meta.output,
	}
}

// argSchemas introspects a command's positional arguments via a type switch over
// the concrete argument types, since the Argument interface exposes no name.
func argSchemas(args []cliv3.Argument) []ArgSchema {
	out := make([]ArgSchema, 0, len(args))
	for _, a := range args {
		switch v := a.(type) {
		case *cliv3.StringArg:
			out = append(out, ArgSchema{Name: v.Name})
		case *cliv3.StringArgs:
			out = append(out, ArgSchema{Name: v.Name, Variadic: true})
		}
	}
	return out
}

// flagSchemas introspects a flag slice, reporting each flag's name, aliases,
// type, and non-zero default. The conventional --help and --version flags are
// omitted: the design treats them separately from the machine-readable contract.
func flagSchemas(flags []cliv3.Flag) []FlagSchema {
	out := make([]FlagSchema, 0, len(flags))
	for _, f := range flags {
		switch f.Names()[0] {
		case "help", "version":
			continue
		}
		out = append(out, flagSchema(f))
	}
	return out
}

// flagSchema maps one flag to its schema. A type switch over the concrete flag
// types reports a precise type (distinguishing stringSlice from string, which a
// bare TypeName conflates) and a JSON-friendly default; unknown types fall back
// to the framework's TypeName.
func flagSchema(f cliv3.Flag) FlagSchema {
	names := f.Names()
	fs := FlagSchema{Name: "--" + names[0]}
	for _, a := range names[1:] {
		fs.Aliases = append(fs.Aliases, "--"+a)
	}

	switch v := f.(type) {
	case *cliv3.StringFlag:
		fs.Type = "string"
		if v.Value != "" {
			fs.Default = v.Value
		}
	case *cliv3.BoolFlag:
		fs.Type = "bool"
		if v.Value {
			fs.Default = v.Value
		}
	case *cliv3.IntFlag:
		fs.Type = "int"
		if v.Value != 0 {
			fs.Default = v.Value
		}
	case *cliv3.DurationFlag:
		fs.Type = "duration"
		if v.Value != 0 {
			fs.Default = v.Value.String()
		}
	case *cliv3.StringSliceFlag:
		fs.Type = "stringSlice"
		if len(v.Value) > 0 {
			fs.Default = v.Value
		}
	default:
		if dg, ok := f.(cliv3.DocGenerationFlag); ok {
			fs.Type = dg.TypeName()
		}
	}
	return fs
}
