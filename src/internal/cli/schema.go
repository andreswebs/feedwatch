package cli

import (
	"context"
	"encoding/json"

	cliv3 "github.com/urfave/cli/v3"
)

// CommandSchema is the machine-readable description of one command: its
// positional arguments and flags (introspected from the live command tree) plus
// its exit codes and output JSON Schema (from the per-command registry, which
// holds only what introspection cannot derive).
type CommandSchema struct {
	Command   string            `json:"command"`
	Args      []ArgSchema       `json:"args"`
	Flags     []FlagSchema      `json:"flags"`
	ExitCodes map[string]string `json:"exit_codes"`
	Output    json.RawMessage   `json:"output_schema"`
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

// SchemaResult is the bare-schema envelope: every command's schema plus the
// global flags inherited by all of them.
type SchemaResult struct {
	Commands    []CommandSchema `json:"commands"`
	GlobalFlags []FlagSchema    `json:"global_flags"`
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
// when no name is given. An unknown command name is a usage error (exit 1).
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

	result := SchemaResult{
		Commands:    commandSchemas(root),
		GlobalFlags: flagSchemas(root.Flags),
	}
	return r.Result(result)
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
func commandSchemas(root *cliv3.Command) []CommandSchema {
	out := make([]CommandSchema, 0, len(root.Commands))
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
func commandSchema(c *cliv3.Command) CommandSchema {
	meta := registryFor(c.Name)
	return CommandSchema{
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
