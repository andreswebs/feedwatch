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

// defaultExitCodes is the exit-code table shared by every command that either
// fully succeeds or fails as a whole-invocation usage or configuration error.
func defaultExitCodes() map[string]string {
	return map[string]string{
		"0": "success",
		"1": "usage or configuration error",
	}
}

// pollExitCodes is the feed-outcome table: a poll exits 2 when every targeted
// feed failed and 3 when some did, distinct from a hard whole-invocation
// failure (exit 1).
func pollExitCodes() map[string]string {
	return map[string]string{
		"0": "all targeted feeds succeeded",
		"1": "usage or configuration error",
		"2": "all targeted feeds failed",
		"3": "partial: some feeds succeeded and some failed",
	}
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
