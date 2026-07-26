package command

import (
	"encoding/json"
	"sort"
	"strconv"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/terr"
)

// decodeCommandSchema unmarshals stdout into a single Schema, failing the
// test if it is not valid JSON of that shape.
func decodeCommandSchema(t *testing.T, out string) Schema {
	t.Helper()
	var cs Schema
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		t.Fatalf("stdout is not a Schema: %v\ngot: %q", err, out)
	}
	return cs
}

// findFlag returns the named flag schema (matching with or without the leading
// dashes) and whether it was found.
func findFlag(flags []FlagSchema, name string) (FlagSchema, bool) {
	for _, f := range flags {
		if f.Name == name || f.Name == "--"+name {
			return f, true
		}
	}
	return FlagSchema{}, false
}

// TestSchemaPoll is the tracer: schema poll emits poll's flags, its exit codes
// (0/2/3), and a non-empty output schema.
func TestSchemaPoll(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "schema", "poll")

	if res.code != 0 {
		t.Errorf("schema poll should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	cs := decodeCommandSchema(t, res.out)

	if cs.Command != "poll" {
		t.Errorf("command = %q, want poll", cs.Command)
	}

	force, ok := findFlag(cs.Flags, "force")
	if !ok {
		t.Fatalf("poll schema is missing the --force flag: %+v", cs.Flags)
	}
	if force.Type != "bool" {
		t.Errorf("--force type = %q, want bool", force.Type)
	}

	for _, code := range []string{"0", "2", "3"} {
		if _, ok := cs.ExitCodes[code]; !ok {
			t.Errorf("poll exit codes missing %q: %v", code, cs.ExitCodes)
		}
	}

	if len(cs.Output) == 0 {
		t.Fatalf("poll output schema is empty")
	}
	var js map[string]any
	if err := json.Unmarshal(cs.Output, &js); err != nil {
		t.Errorf("poll output schema is not valid JSON: %v", err)
	}
}

// TestSchemaListsAllCommands covers behavior 2: bare schema lists every
// registered command.
func TestSchemaListsAllCommands(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "schema")

	if res.code != 0 {
		t.Errorf("schema should exit 0, got code %d", res.code)
	}

	var sr SchemaResult
	if err := json.Unmarshal([]byte(res.out), &sr); err != nil {
		t.Fatalf("stdout is not a SchemaResult: %v\ngot: %q", err, res.out)
	}

	got := make(map[string]bool, len(sr.Commands))
	for _, c := range sr.Commands {
		got[c.Command] = true
	}

	want := []string{
		"migrate", "poll", "add", "list", "rm", "enable", "disable",
		"items", "prune", "discover", "import", "export", "schema",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("schema output missing command %q (have %v)", name, got)
		}
	}

	if len(sr.GlobalFlags) == 0 {
		t.Errorf("schema output has no global flags")
	}
	if _, ok := findFlag(sr.GlobalFlags, "db"); !ok {
		t.Errorf("global flags missing --db: %+v", sr.GlobalFlags)
	}
}

// TestSchemaErrorInventory is the tracer for the ADR 0005 error inventory: bare
// schema reports an `errors` array carrying {code, exit_code, hint}, and it is
// exactly a projection of terr.All() compared element-wise against the registry
// rather than a literal, so a newly registered code appears with no test edit.
func TestSchemaErrorInventory(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "schema")
	if res.code != 0 {
		t.Fatalf("schema should exit 0, got %d; stderr=%q", res.code, res.err)
	}

	var sr SchemaResult
	if err := json.Unmarshal([]byte(res.out), &sr); err != nil {
		t.Fatalf("stdout is not a SchemaResult: %v\ngot: %q", err, res.out)
	}

	want := terr.All()
	if len(sr.Errors) != len(want) {
		t.Fatalf("errors has %d entries, want %d (terr.All)\ngot: %+v", len(sr.Errors), len(want), sr.Errors)
	}
	for i, e := range want {
		got := sr.Errors[i]
		if got.Code != e.Code() || got.ExitCode != e.ExitCode() || got.Hint != e.Hint() {
			t.Errorf("errors[%d] = %+v, want {code:%q exit:%d hint:%q}", i, got, e.Code(), e.ExitCode(), e.Hint())
		}
	}
}

// TestSchemaNewSentinelAppears covers behavior 3: registering a new sentinel
// makes it appear in the schema error inventory with no other change, proving
// the inventory is a live projection of terr.All() rather than a literal.
func TestSchemaNewSentinelAppears(t *testing.T) {
	sentinel := terr.New("schema_test_only_code", 70, "a hint", "a message")

	res := runCLI(t, "1.2.3", "feedwatch", "schema")
	var sr SchemaResult
	if err := json.Unmarshal([]byte(res.out), &sr); err != nil {
		t.Fatalf("stdout is not a SchemaResult: %v", err)
	}

	found := false
	for _, e := range sr.Errors {
		if e.Code == sentinel.Code() {
			found = true
			if e.ExitCode != sentinel.ExitCode() || e.Hint != sentinel.Hint() {
				t.Errorf("new sentinel projected as %+v, want exit %d hint %q", e, sentinel.ExitCode(), sentinel.Hint())
			}
		}
	}
	if !found {
		t.Errorf("newly registered code %q did not appear in the schema error inventory", sentinel.Code())
	}
}

// TestSchemaExitCodesUnion covers the tool-level exit_codes: the sorted union of
// every command's declared exit-code table, computed from the tables rather than
// a literal.
func TestSchemaExitCodesUnion(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "schema")
	var sr SchemaResult
	if err := json.Unmarshal([]byte(res.out), &sr); err != nil {
		t.Fatalf("stdout is not a SchemaResult: %v", err)
	}

	want := map[int]bool{}
	for _, c := range sr.Commands {
		for code := range c.ExitCodes {
			n, err := strconv.Atoi(code)
			if err != nil {
				t.Fatalf("command %q has non-int exit code key %q", c.Command, code)
			}
			want[n] = true
		}
	}

	if len(sr.ExitCodes) != len(want) {
		t.Errorf("exit_codes = %v (%d), want the %d-code union of the command tables", sr.ExitCodes, len(sr.ExitCodes), len(want))
	}
	for i, code := range sr.ExitCodes {
		if !want[code] {
			t.Errorf("exit_codes has %d, not declared in any command table", code)
		}
		if i > 0 && sr.ExitCodes[i-1] >= code {
			t.Errorf("exit_codes not strictly sorted ascending: %v", sr.ExitCodes)
		}
	}
}

// TestSchemaToolAndVersion covers the reference-core tool and version fields:
// bare schema reports the tool name and the running binary version.
func TestSchemaToolAndVersion(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "schema")
	var sr SchemaResult
	if err := json.Unmarshal([]byte(res.out), &sr); err != nil {
		t.Fatalf("stdout is not a SchemaResult: %v", err)
	}
	if sr.Tool != "feedwatch" {
		t.Errorf("tool = %q, want feedwatch", sr.Tool)
	}
	if sr.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", sr.Version)
	}
}

// TestSchemaFlagTypes covers behavior 3: each concrete flag type is reported
// correctly, including the slice and duration types that a bare TypeName would
// conflate or mislabel.
func TestSchemaFlagTypes(t *testing.T) {
	items := decodeCommandSchema(t, runCLI(t, "1.2.3", "feedwatch", "schema", "items").out)

	cases := map[string]string{
		"feed":   "stringSlice",
		"since":  "string",
		"limit":  "int",
		"offset": "int",
	}
	for name, wantType := range cases {
		f, ok := findFlag(items.Flags, name)
		if !ok {
			t.Errorf("items schema missing flag %q", name)
			continue
		}
		if f.Type != wantType {
			t.Errorf("flag %q type = %q, want %q", name, f.Type, wantType)
		}
	}

	// --order carries a non-empty default that must be reported.
	order, ok := findFlag(items.Flags, "order")
	if !ok || order.Default != "published desc" {
		t.Errorf("--order = %+v, want default %q", order, "published desc")
	}

	// add's --interval is a duration flag.
	add := decodeCommandSchema(t, runCLI(t, "1.2.3", "feedwatch", "schema", "add").out)
	interval, ok := findFlag(add.Flags, "interval")
	if !ok {
		t.Fatalf("add schema missing --interval")
	}
	if interval.Type != "duration" {
		t.Errorf("--interval type = %q, want duration", interval.Type)
	}
}

// TestSchemaArgs covers argument introspection: add takes a single url argument.
func TestSchemaArgs(t *testing.T) {
	add := decodeCommandSchema(t, runCLI(t, "1.2.3", "feedwatch", "schema", "add").out)
	if len(add.Args) != 1 {
		t.Fatalf("add args = %+v, want one", add.Args)
	}
	if add.Args[0].Name != "url" {
		t.Errorf("add arg name = %q, want url", add.Args[0].Name)
	}
	if add.Args[0].Variadic {
		t.Errorf("add url arg reported variadic, want singular")
	}
}

// TestSchemaUnknownCommand covers behavior 4: narrowing to an unknown command is
// a usage error with exit 64 (EX_USAGE) and empty stdout.
func TestSchemaUnknownCommand(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "schema", "bogus")

	if res.code != 64 {
		t.Errorf("exit code = %d, want 64 (usage)", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty for a usage error", res.out)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrUsage.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
	}
}

// TestSchemaDriftGuard covers behavior 5: the introspected flag set tracks the
// real command tree. A flag added to a command must appear in its schema with
// no registry change, proving schema cannot silently drift from the flags.
func TestSchemaDriftGuard(t *testing.T) {
	outF, errF := tempFile(t), tempFile(t)
	d := Deps{Clock: core.SystemClock, Version: "1.2.3", Out: outF, Err: errF}

	customize := func(root *cliv3.Command) {
		root.Commands = append(root.Commands, &cliv3.Command{
			Name: "probe",
			Flags: []cliv3.Flag{
				&cliv3.IntFlag{Name: "depth", Value: 5},
			},
		})
	}

	r, err := runCustom(t.Context(), []string{"schema", "probe"}, d, customize)
	if code := finish(r, err, nil); code != 0 {
		t.Fatalf("schema probe exit = %d, stderr = %q", code, readFile(t, errF))
	}

	cs := decodeCommandSchema(t, readFile(t, outF))
	depth, ok := findFlag(cs.Flags, "depth")
	if !ok {
		t.Fatalf("added flag --depth did not appear in schema: %+v", cs.Flags)
	}
	if depth.Type != "int" {
		t.Errorf("--depth type = %q, want int", depth.Type)
	}
	if d, ok := depth.Default.(float64); !ok || d != 5 {
		t.Errorf("--depth default = %v, want 5", depth.Default)
	}
}

// parsedSchema is the decoded shape of an output schema, enough to assert on its
// property keys, required set, array element, and oneOf alternatives.
type parsedSchema struct {
	Type        string                     `json:"type"`
	Properties  map[string]json.RawMessage `json:"properties"`
	Required    []string                   `json:"required"`
	Items       json.RawMessage            `json:"items"`
	Description string                     `json:"description"`
	OneOf       []json.RawMessage          `json:"oneOf"`
}

func parseSchema(t *testing.T, raw json.RawMessage) parsedSchema {
	t.Helper()
	var p parsedSchema
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("output schema is not valid JSON: %v\ngot: %s", err, raw)
	}
	return p
}

func propKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func assertContract(t *testing.T, label string, p parsedSchema, wantProps, wantRequired []string) {
	t.Helper()
	if got, want := propKeys(p.Properties), sortedCopy(wantProps); !equalStrings(got, want) {
		t.Errorf("%s properties = %v, want %v", label, got, want)
	}
	if got, want := sortedCopy(p.Required), sortedCopy(wantRequired); !equalStrings(got, want) {
		t.Errorf("%s required = %v, want %v", label, got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestOutputSchemaContractPreserved is the output-half twin of
// TestSchemaDriftGuard: it pins each command's derived properties and required
// sets to the contract the hand-authored schemas expressed, proving the
// reflection migration changed no meaning, save the one documented tightening
// (import's failed element gains required xmlUrl/reason).
func TestOutputSchemaContractPreserved(t *testing.T) {
	feedViewProps := []string{"url", "alias", "interval", "status", "failures", "last_error"}
	feedViewReq := []string{"url", "status", "failures"}

	// head is the schema_version/ok pair that opens every envelope; it is
	// required and appears at the top level of each reflected result.
	head := []string{"schema_version", "ok"}
	withHead := func(fields ...string) []string { return append(append([]string(nil), head...), fields...) }

	// Plain-object commands: top-level properties and required, each opening
	// with the head.
	objects := map[string]struct {
		props    []string
		required []string
	}{
		"add":      {withHead("url", "alias", "interval", "created"), withHead("url", "created")},
		"list":     {withHead("feeds"), withHead("feeds")},
		"rm":       {withHead("removed"), withHead("removed")},
		"enable":   {withHead("feed"), withHead("feed")},
		"disable":  {withHead("feed"), withHead("feed")},
		"prune":    {withHead("pruned"), withHead("pruned")},
		"items":    {withHead("items", "omitted_no_date"), withHead("items")},
		"poll":     {withHead("polled", "succeeded", "failed", "skipped", "fetched", "new_items", "deduped", "items", "failures", "renamed"), withHead("polled", "succeeded", "failed", "skipped", "fetched", "new_items", "deduped", "items", "failures", "renamed")},
		"discover": {withHead("candidates"), withHead("candidates")},
		"import":   {withHead("added", "skipped", "failed"), withHead("added", "skipped", "failed")},
	}
	for name, want := range objects {
		p := parseSchema(t, registryFor(name).output)
		if p.Type != "object" {
			t.Errorf("%s type = %q, want object", name, p.Type)
		}
		assertContract(t, name, p, want.props, want.required)
	}

	// FeedView is reached by recursion through list/enable/disable.
	list := parseSchema(t, registryFor("list").output)
	feeds := parseSchema(t, list.Properties["feeds"])
	feedItem := parseSchema(t, feeds.Items)
	assertContract(t, "list.feeds[]", feedItem, feedViewProps, feedViewReq)

	enable := parseSchema(t, registryFor("enable").output)
	assertContract(t, "enable.feed", parseSchema(t, enable.Properties["feed"]), feedViewProps, feedViewReq)

	// discover candidates element.
	disc := parseSchema(t, registryFor("discover").output)
	cands := parseSchema(t, disc.Properties["candidates"])
	assertContract(t, "discover.candidates[]", parseSchema(t, cands.Items),
		[]string{"title", "url", "type", "source"}, []string{"url", "source"})

	// import failed element: the documented tightening to required xmlUrl/reason.
	imp := parseSchema(t, registryFor("import").output)
	failed := parseSchema(t, imp.Properties["failed"])
	assertContract(t, "import.failed[]", parseSchema(t, failed.Items),
		[]string{"xmlUrl", "reason"}, []string{"xmlUrl", "reason"})

	// poll and items document the item object shape: an array of objects with
	// at least published_at and title in the schema properties.
	for _, name := range []string{"poll", "items"} {
		p := parseSchema(t, registryFor(name).output)
		arr := parseSchema(t, p.Properties["items"])
		if arr.Type != "array" {
			t.Errorf("%s.items type = %q, want array", name, arr.Type)
		}
		elem := parseSchema(t, arr.Items)
		if elem.Type != "object" {
			t.Errorf("%s.items element type = %q, want object", name, elem.Type)
		}
		for _, field := range []string{"published_at", "title", "fetched_at", "link"} {
			if _, ok := elem.Properties[field]; !ok {
				t.Errorf("%s.items element missing property %q; got %v", name, field, propKeys(elem.Properties))
			}
		}
	}

	// migrate is a oneOf of the applied and status shapes.
	mig := parseSchema(t, registryFor("migrate").output)
	if len(mig.OneOf) != 2 {
		t.Fatalf("migrate oneOf has %d alternatives, want 2", len(mig.OneOf))
	}
	assertContract(t, "migrate.applied", parseSchema(t, mig.OneOf[0]),
		withHead("applied", "store_schema_version"), withHead("applied", "store_schema_version"))
	assertContract(t, "migrate.status", parseSchema(t, mig.OneOf[1]),
		withHead("store_schema_version", "pending", "backend"), withHead("store_schema_version", "pending", "backend"))

	// export and schema are non-object scalars carrying a description.
	exp := parseSchema(t, registryFor("export").output)
	if exp.Type != "string" || exp.Description == "" {
		t.Errorf("export schema = %s, want a described string scalar", registryFor("export").output)
	}
	sch := parseSchema(t, registryFor("schema").output)
	if sch.Type != "object" || sch.Description == "" {
		t.Errorf("schema schema = %s, want a described object scalar", registryFor("schema").output)
	}
}
