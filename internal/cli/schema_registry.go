package cli

import (
	"encoding/json"

	"github.com/andreswebs/feedwatch/internal/jsonschema"
)

// cmdMeta holds the parts of a command's contract that introspection of the
// urfave tree cannot derive: the exit-code table and the output JSON Schema.
// It lives beside the command definitions so schema augments, rather than
// guesses, what the framework does not track.
type cmdMeta struct {
	exitCodes map[string]string
	output    json.RawMessage
}

// registryFor returns the registered metadata for a command, falling back to
// the conventional store-command contract (success or a usage/config error)
// and a permissive object schema for any command without an explicit entry,
// such as one injected by a test.
func registryFor(name string) cmdMeta {
	if m, ok := schemaRegistry[name]; ok {
		return cmdMeta{exitCodes: m.exitCodes, output: m.output}
	}
	return cmdMeta{exitCodes: defaultExitCodes(), output: json.RawMessage(`{"type":"object"}`)}
}

// failureExitCodes is the shared whole-invocation failure table of ADR 0001
// (docs/adr/0001-exit-code-taxonomy.md): the BSD sysexits.h classes feedwatch
// can actually produce. Store I/O is routed through the store-unavailable class
// (69), so there is no separate EX_IOERR (74) class to declare.
func failureExitCodes() map[string]string {
	return map[string]string{
		"64": "usage error: the CLI surface was misused (EX_USAGE)",
		"65": "data error: stored schema is newer than this binary supports (EX_DATAERR)",
		"69": "store unavailable: the store could not be opened or reached (EX_UNAVAILABLE)",
		"70": "internal error, a bug (EX_SOFTWARE)",
		"78": "configuration error: invalid configuration (EX_CONFIG)",
	}
}

// defaultExitCodes is the exit-code table shared by every command that either
// fully succeeds (0) or fails as a whole-invocation error in the sysexits.h
// range (ADR 0001).
func defaultExitCodes() map[string]string {
	codes := failureExitCodes()
	codes["0"] = "success"
	return codes
}

// pollExitCodes is the poll table. Codes 2 and 3 are result sub-codes (the
// command completed): 2 when every targeted feed failed, 3 when some did. They
// are not failures. The whole-invocation failure classes of ADR 0001 also apply.
func pollExitCodes() map[string]string {
	codes := failureExitCodes()
	codes["0"] = "all targeted feeds succeeded"
	codes["2"] = "result: all targeted feeds failed (command completed)"
	codes["3"] = "result: some feeds succeeded and some failed (command completed)"
	return codes
}

// checkExitCodes mirrors pollExitCodes for the check command. Codes 2 and 3 are
// result sub-codes (the command completed), not failures.
func checkExitCodes() map[string]string {
	codes := failureExitCodes()
	codes["0"] = "all checked feeds passed (or nothing to check)"
	codes["2"] = "result: all checked feeds failed (command completed)"
	codes["3"] = "result: some feeds passed and some failed (command completed)"
	return codes
}

// schemaRegistry maps each command to its exit codes and output JSON Schema.
// The flag and argument halves of a command's contract are introspected from
// the live tree; only these two fields are hand-maintained here.
// schemaRegistry maps each command to its exit codes and output JSON Schema.
// Each output schema is derived at init from the command's Go result struct, so
// the result type is the single source of truth and the schema cannot drift
// from what the command returns. Two outputs are not plain objects: migrate has
// two shapes (oneOf) and export/schema are non-object scalars.
var schemaRegistry = map[string]cmdMeta{
	"migrate":  {exitCodes: defaultExitCodes(), output: jsonschema.OneOf(jsonschema.Reflect(MigrateApplied{}), jsonschema.Reflect(MigrateStatus{}))},
	"poll":     {exitCodes: pollExitCodes(), output: jsonschema.Reflect(PollResult{})},
	"check":    {exitCodes: checkExitCodes(), output: jsonschema.Reflect(CheckResult{})},
	"add":      {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(AddResult{})},
	"list":     {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(ListResult{})},
	"rm":       {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(RmResult{})},
	"enable":   {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(EnableResult{})},
	"disable":  {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(DisableResult{})},
	"items":    {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(ItemsResult{})},
	"prune":    {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(PruneResult{})},
	"discover": {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(DiscoverResult{})},
	"import":   {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(ImportResult{})},
	"export":   {exitCodes: defaultExitCodes(), output: jsonschema.Scalar("string", "OPML 2.0 XML document written to the output file or stdout; not a JSON envelope")},
	"schema":   {exitCodes: defaultExitCodes(), output: jsonschema.Scalar("object", "a CommandSchema when narrowed to one command, otherwise {commands,global_flags}")},
}
