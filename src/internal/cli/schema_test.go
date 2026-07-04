package cli

import (
	"encoding/json"
	"sort"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
)

// decodeCommandSchema unmarshals stdout into a single CommandSchema, failing the
// test if it is not valid JSON of that shape.
func decodeCommandSchema(t *testing.T, out string) CommandSchema {
	t.Helper()
	var cs CommandSchema
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		t.Fatalf("stdout is not a CommandSchema: %v\ngot: %q", err, out)
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
	res := runRoot(t, "1.2.3", "feedwatch", "schema", "poll")

	if res.exited {
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
	res := runRoot(t, "1.2.3", "feedwatch", "schema")

	if res.exited {
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

// TestSchemaFlagTypes covers behavior 3: each concrete flag type is reported
// correctly, including the slice and duration types that a bare TypeName would
// conflate or mislabel.
func TestSchemaFlagTypes(t *testing.T) {
	items := decodeCommandSchema(t, runRoot(t, "1.2.3", "feedwatch", "schema", "items").out)

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
	add := decodeCommandSchema(t, runRoot(t, "1.2.3", "feedwatch", "schema", "add").out)
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
	add := decodeCommandSchema(t, runRoot(t, "1.2.3", "feedwatch", "schema", "add").out)
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
// a usage error with exit 1 and empty stdout.
func TestSchemaUnknownCommand(t *testing.T) {
	res := runRoot(t, "1.2.3", "feedwatch", "schema", "bogus")

	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty for a usage error", res.out)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatUsage) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatUsage)
	}
}

// TestSchemaDriftGuard covers behavior 5: the introspected flag set tracks the
// real command tree. A flag added to a command must appear in its schema with
// no registry change, proving schema cannot silently drift from the flags.
func TestSchemaDriftGuard(t *testing.T) {
	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   core.SystemClock,
		Version: "1.2.3",
		Out:     outF,
		Err:     errF,
	}

	root := NewRootCommand(d)
	root.Commands = append(root.Commands, &cliv3.Command{
		Name: "probe",
		Flags: []cliv3.Flag{
			&cliv3.IntFlag{Name: "depth", Value: 5},
		},
	})

	oldExiter := cliv3.OsExiter
	cliv3.OsExiter = func(int) {}
	t.Cleanup(func() { cliv3.OsExiter = oldExiter })

	if err := root.Run(t.Context(), []string{"feedwatch", "schema", "probe"}); err != nil {
		t.Fatalf("schema probe: %v", err)
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

	// Plain-object commands: top-level properties and required.
	objects := map[string]struct {
		props    []string
		required []string
	}{
		"add":      {[]string{"url", "alias", "interval", "created"}, []string{"url", "created"}},
		"list":     {[]string{"feeds"}, []string{"feeds"}},
		"rm":       {[]string{"removed"}, []string{"removed"}},
		"enable":   {[]string{"feed"}, []string{"feed"}},
		"disable":  {[]string{"feed"}, []string{"feed"}},
		"prune":    {[]string{"pruned"}, []string{"pruned"}},
		"items":    {[]string{"items", "omitted_no_date"}, []string{"items"}},
		"poll":     {[]string{"polled", "succeeded", "failed", "skipped", "fetched", "new_items", "deduped", "items", "failures", "renamed"}, []string{"polled", "succeeded", "failed", "skipped", "fetched", "new_items", "deduped", "items", "failures", "renamed"}},
		"discover": {[]string{"candidates"}, []string{"candidates"}},
		"import":   {[]string{"added", "skipped", "failed"}, []string{"added", "skipped", "failed"}},
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
		[]string{"applied", "schema_version"}, []string{"applied", "schema_version"})
	assertContract(t, "migrate.status", parseSchema(t, mig.OneOf[1]),
		[]string{"schema_version", "pending", "backend"}, []string{"schema_version", "pending", "backend"})

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
