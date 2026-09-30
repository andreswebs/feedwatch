package command

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// oneStringRequest is the tracer: the smallest request shape the projector has to
// handle.
type oneStringRequest struct {
	Since string `flag:"since" usage:"lower time bound"`
}

// everyKindRequest carries one field per entry of the supported type mapping, so
// the table test below is the executable form of that table.
type everyKindRequest struct {
	Text     string        `flag:"text" usage:"a string"`
	Yes      bool          `flag:"yes" usage:"a bool"`
	Count    int           `flag:"count" usage:"an int"`
	Optional *int          `flag:"optional" usage:"an optional int"`
	Wait     time.Duration `flag:"wait" usage:"a duration"`
	Many     []string      `flag:"many" usage:"a string slice"`
}

// taggedRequest exercises the shape tags that are not the field type: exclusion,
// aliases, and compiled-in defaults.
type taggedRequest struct {
	Hidden  []byte `flag:"-"`
	Force   bool   `flag:"force" alias:"all" usage:"force it"`
	Order   string `flag:"order" default:"published desc" usage:"sort order"`
	Limit   int    `flag:"limit" default:"25" usage:"how many"`
	Wait    string `flag:"wait" usage:"no default"`
	Aliased string `flag:"aliased" alias:"a,b" usage:"two aliases"`
}

// argRequest exercises positional projection: a single-value argument and a
// variadic one.
type argRequest struct {
	Ref   string   `arg:"ref"`
	Feeds []string `arg:"feed" variadic:"true"`
}

// emptyRequest is a use case that accepts nothing; the projector must handle it
// without special-casing.
type emptyRequest struct{}

// unmappedRequest has a field type absent from the mapping table, the one
// property of the reflection approach that cannot fail at compile time.
type unmappedRequest struct {
	Ratio float64 `flag:"ratio" usage:"unsupported"`
}

// untaggedRequest has a field carrying neither a flag nor an arg tag, which is
// the "a new field silently has no CLI surface" failure the projector exists to
// prevent.
type untaggedRequest struct {
	Forgotten string
}

// TestFlagsForTracer is the tracer cycle: one string field yields one string flag
// carrying that name and usage.
func TestFlagsForTracer(t *testing.T) {
	flags := flagsFor(oneStringRequest{})

	if len(flags) != 1 {
		t.Fatalf("flagsFor yielded %d flags, want 1: %v", len(flags), flags)
	}
	f, ok := flags[0].(*cliv3.StringFlag)
	if !ok {
		t.Fatalf("flag type = %T, want *cli.StringFlag", flags[0])
	}
	if f.Name != "since" {
		t.Errorf("name = %q, want since", f.Name)
	}
	if f.Usage != "lower time bound" {
		t.Errorf("usage = %q, want %q", f.Usage, "lower time bound")
	}
}

// TestFlagsForEveryKind covers the whole type mapping: each supported Go type
// projects to its documented flag type, in struct field order.
func TestFlagsForEveryKind(t *testing.T) {
	flags := flagsFor(everyKindRequest{})

	want := []struct {
		name string
		flag cliv3.Flag
	}{
		{"text", &cliv3.StringFlag{}},
		{"yes", &cliv3.BoolFlag{}},
		{"count", &cliv3.IntFlag{}},
		{"optional", &cliv3.IntFlag{}},
		{"wait", &cliv3.DurationFlag{}},
		{"many", &cliv3.StringSliceFlag{}},
	}
	if len(flags) != len(want) {
		t.Fatalf("flagsFor yielded %d flags, want %d", len(flags), len(want))
	}
	for i, w := range want {
		if got := flags[i].Names()[0]; got != w.name {
			t.Errorf("flag %d name = %q, want %q", i, got, w.name)
		}
		if fmt.Sprintf("%T", flags[i]) != fmt.Sprintf("%T", w.flag) {
			t.Errorf("flag %q type = %T, want %T", w.name, flags[i], w.flag)
		}
	}
}

// TestFlagsForShapeTags covers exclusion, aliases, and defaults: the three tags
// that describe shape without describing a type.
func TestFlagsForShapeTags(t *testing.T) {
	flags := flagsFor(taggedRequest{})

	for _, f := range flags {
		if f.Names()[0] == "hidden" {
			t.Fatalf(`flag:"-" field still produced a flag: %v`, f.Names())
		}
	}

	force, ok := findCLIFlag(flags, "force").(*cliv3.BoolFlag)
	if !ok {
		t.Fatalf("force flag missing or not a bool flag")
	}
	if len(force.Aliases) != 1 || force.Aliases[0] != "all" {
		t.Errorf("force aliases = %v, want [all]", force.Aliases)
	}

	aliased, ok := findCLIFlag(flags, "aliased").(*cliv3.StringFlag)
	if !ok {
		t.Fatalf("aliased flag missing or not a string flag")
	}
	if strings.Join(aliased.Aliases, ",") != "a,b" {
		t.Errorf("aliased aliases = %v, want [a b]", aliased.Aliases)
	}

	order, ok := findCLIFlag(flags, "order").(*cliv3.StringFlag)
	if !ok {
		t.Fatalf("order flag missing or not a string flag")
	}
	if order.Value != "published desc" {
		t.Errorf("order default = %q, want %q", order.Value, "published desc")
	}

	limit, ok := findCLIFlag(flags, "limit").(*cliv3.IntFlag)
	if !ok {
		t.Fatalf("limit flag missing or not an int flag")
	}
	if limit.Value != 25 {
		t.Errorf("limit default = %d, want 25", limit.Value)
	}

	wait, ok := findCLIFlag(flags, "wait").(*cliv3.StringFlag)
	if !ok {
		t.Fatalf("wait flag missing or not a string flag")
	}
	if wait.Value != "" {
		t.Errorf("wait default = %q, want the zero value", wait.Value)
	}
}

// TestArgsFor covers positional projection: a plain arg tag becomes a single
// string argument and variadic:"true" becomes an unbounded one.
func TestArgsFor(t *testing.T) {
	args := argsFor(argRequest{})

	if len(args) != 2 {
		t.Fatalf("argsFor yielded %d arguments, want 2: %v", len(args), args)
	}
	ref, ok := args[0].(*cliv3.StringArg)
	if !ok {
		t.Fatalf("argument 0 type = %T, want *cli.StringArg", args[0])
	}
	if ref.Name != "ref" {
		t.Errorf("argument 0 name = %q, want ref", ref.Name)
	}

	feeds, ok := args[1].(*cliv3.StringArgs)
	if !ok {
		t.Fatalf("argument 1 type = %T, want *cli.StringArgs", args[1])
	}
	if feeds.Name != "feed" {
		t.Errorf("argument 1 name = %q, want feed", feeds.Name)
	}
	if feeds.Max != -1 {
		t.Errorf("argument 1 max = %d, want -1 (unbounded)", feeds.Max)
	}
}

// TestProjectorsOnEmptyRequest covers the empty use case: both projectors return
// empty slices rather than nil-panicking or needing a special case.
func TestProjectorsOnEmptyRequest(t *testing.T) {
	if flags := flagsFor(emptyRequest{}); len(flags) != 0 {
		t.Errorf("flagsFor(empty) = %v, want no flags", flags)
	}
	if args := argsFor(emptyRequest{}); len(args) != 0 {
		t.Errorf("argsFor(empty) = %v, want no arguments", args)
	}
}

// TestFlagsForUnmappedTypePanics is the guard that converts the one unsafe
// property of reflection back into an unmissable failure: an unsupported field
// type panics at command-tree construction, naming struct, field, and type.
func TestFlagsForUnmappedTypePanics(t *testing.T) {
	msg := recoverMessage(t, func() { flagsFor(unmappedRequest{}) })

	for _, want := range []string{"unmappedRequest", "Ratio", "float64"} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic message %q does not name %q", msg, want)
		}
	}
}

// TestFlagsForUntaggedFieldPanics pins the other half of the guard: a field with
// no shape tag is a mistake, not an implicit opt-out, so it fails loudly instead
// of quietly having no CLI surface.
func TestFlagsForUntaggedFieldPanics(t *testing.T) {
	msg := recoverMessage(t, func() { flagsFor(untaggedRequest{}) })

	for _, want := range []string{"untaggedRequest", "Forgotten"} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic message %q does not name %q", msg, want)
		}
	}
}

// TestBindRoundTrip drives a real command built from a request type and asserts
// the bound struct equals what the argv described, for every supported type.
func TestBindRoundTrip(t *testing.T) {
	got := bindThrough(t, everyKindRequest{}, "--text", "hello", "--yes",
		"--count", "7", "--optional", "3", "--wait", "90s", "--many", "a", "--many", "b")

	three := 3
	want := everyKindRequest{
		Text:     "hello",
		Yes:      true,
		Count:    7,
		Optional: &three,
		Wait:     90 * time.Second,
		Many:     []string{"a", "b"},
	}
	if got.Text != want.Text || got.Yes != want.Yes || got.Count != want.Count ||
		got.Wait != want.Wait || strings.Join(got.Many, ",") != strings.Join(want.Many, ",") {
		t.Errorf("bound = %+v, want %+v", got, want)
	}
	if got.Optional == nil || *got.Optional != three {
		t.Errorf("bound Optional = %v, want a pointer to 3", got.Optional)
	}
}

// TestBindPointerSetVsUnset is the one case where the Go type and the flag type
// deliberately disagree: an absent flag leaves the pointer nil, while an explicit
// zero is a pointer to zero. This is what keeps --keep-days 0 distinguishable
// from no --keep-days at all.
func TestBindPointerSetVsUnset(t *testing.T) {
	absent := bindThrough(t, everyKindRequest{})
	if absent.Optional != nil {
		t.Errorf("absent flag bound Optional = %v, want nil", *absent.Optional)
	}

	zero := bindThrough(t, everyKindRequest{}, "--optional", "0")
	if zero.Optional == nil {
		t.Fatalf("--optional 0 bound Optional = nil, want a pointer to 0")
	}
	if *zero.Optional != 0 {
		t.Errorf("--optional 0 bound Optional = %d, want 0", *zero.Optional)
	}
}

// TestBindArguments covers positional binding, including the variadic tail.
func TestBindArguments(t *testing.T) {
	var got argRequest
	runBind(t, argRequest{}, &got, "godev", "a", "b")

	if got.Ref != "godev" {
		t.Errorf("Ref = %q, want godev", got.Ref)
	}
	if strings.Join(got.Feeds, ",") != "a,b" {
		t.Errorf("Feeds = %v, want [a b]", got.Feeds)
	}
}

// TestBindDefaults covers the defaulting path: an unset flag binds its
// compiled-in default, so the request the library validates is the same one the
// help text advertises.
func TestBindDefaults(t *testing.T) {
	var got taggedRequest
	runBind(t, taggedRequest{}, &got)

	if got.Order != "published desc" {
		t.Errorf("Order = %q, want the compiled-in default", got.Order)
	}
	if got.Limit != 25 {
		t.Errorf("Limit = %d, want 25", got.Limit)
	}
	if got.Hidden != nil {
		t.Errorf(`flag:"-" field was written by bind: %v`, got.Hidden)
	}
}

// requestSurfaceCase is one row of the ADR 0007 coverage table: a request type
// and the number of flags and arguments its shape must project to.
type requestSurfaceCase struct {
	name  string
	req   any
	flags int
	args  int
}

// requestSurfaceCases is the coverage table itself, shared by the mapping test
// that exercises it and the coverage test that proves it is complete. Extend it
// when a use case is added; TestRequestSurfaceCoverage fails until you do.
func requestSurfaceCases() []requestSurfaceCase {
	return []requestSurfaceCase{
		{"add", feedwatch.AddRequest{}, 3, 1},
		{"check", feedwatch.CheckRequest{}, 2, 1},
		{"disable", feedwatch.DisableRequest{}, 0, 1},
		{"discover", feedwatch.DiscoverRequest{}, 0, 1},
		{"enable", feedwatch.EnableRequest{}, 0, 1},
		{"export", feedwatch.ExportRequest{}, 2, 0},
		{"import", feedwatch.ImportRequest{}, 0, 0},
		{"items", feedwatch.ItemsRequest{}, 11, 0},
		{"list", feedwatch.ListRequest{}, 2, 0},
		{"poll", feedwatch.PollRequest{}, 4, 1},
		{"prune", feedwatch.PruneRequest{}, 4, 0},
		{"rm", feedwatch.RemoveRequest{}, 2, 1},
		{"tag", feedwatch.TagRequest{}, 4, 1},
		{"tags", feedwatch.TagsRequest{}, 0, 0},
	}
}

// TestRequestSurfaceCoverage is the half of ADR 0007's mandatory guard that the
// table alone cannot supply: the ADR asks for a test that walks *every* request
// type in the library, and a hand-maintained list silently omits the next one.
// Declaring a request type is what admits an unmapped field type, so the failure
// has to land when the type is added rather than when a frontend first wires it
// and panics. The library's own source is the only enumeration of its exported
// types, so the guard parses it.
func TestRequestSurfaceCoverage(t *testing.T) {
	declared := declaredRequestTypes(t)
	if len(declared) == 0 {
		t.Fatal("no request types found in the library; the guard would pass vacuously")
	}

	tabled := make(map[string]bool, len(requestSurfaceCases()))
	for _, tc := range requestSurfaceCases() {
		tabled[reflect.TypeOf(tc.req).Name()] = true
	}

	for _, name := range declared {
		if !tabled[name] {
			t.Errorf("feedwatch.%s is not in the request surface table; add a row to requestSurfaceCases", name)
		}
	}
}

// declaredRequestTypes returns the names of every exported type in the library's
// root package whose name ends in "Request", read from the package source.
func declaredRequestTypes(t *testing.T) []string {
	t.Helper()

	// The test runs in the CLI package directory, two levels below the module
	// root, which is where the library's root package lives.
	const libraryDir = "../.."

	entries, err := filepath.Glob(filepath.Join(libraryDir, "*.go"))
	if err != nil {
		t.Fatalf("glob the library sources: %v", err)
	}

	var names []string
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		if file.Name.Name != "feedwatch" {
			continue
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() || !strings.HasSuffix(ts.Name.Name, "Request") {
					continue
				}
				names = append(names, ts.Name.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// TestRequestSurfaceMapping is the mandatory coverage table of ADR 0007: every
// request type the library declares projects without panicking, and yields
// exactly the documented number of flags and arguments.
func TestRequestSurfaceMapping(t *testing.T) {
	for _, tc := range requestSurfaceCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(flagsFor(tc.req)); got != tc.flags {
				t.Errorf("flagsFor(%T) yielded %d flags, want %d", tc.req, got, tc.flags)
			}
			if got := len(argsFor(tc.req)); got != tc.args {
				t.Errorf("argsFor(%T) yielded %d arguments, want %d", tc.req, got, tc.args)
			}
		})
	}
}

// findCLIFlag returns the flag whose primary name matches, or nil.
func findCLIFlag(flags []cliv3.Flag, name string) cliv3.Flag {
	for _, f := range flags {
		if f.Names()[0] == name {
			return f
		}
	}
	return nil
}

// recoverMessage runs fn, which is expected to panic, and returns the panic
// value rendered as text.
func recoverMessage(t *testing.T, fn func()) string {
	t.Helper()

	var msg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				msg = fmt.Sprint(r)
			}
		}()
		fn()
	}()

	if msg == "" {
		t.Fatalf("expected a panic, got none")
	}
	return msg
}

// runBind builds a command from proto's projected surface, runs it with args, and
// binds the parsed values into dst, which must be a pointer to the same type.
func runBind(t *testing.T, proto, dst any, args ...string) {
	t.Helper()

	cmd := &cliv3.Command{
		Name:      "probe",
		Flags:     flagsFor(proto),
		Arguments: argsFor(proto),
		Action: func(_ context.Context, cmd *cliv3.Command) error {
			return bind(cmd, dst)
		},
	}
	if err := cmd.Run(context.Background(), append([]string{"probe"}, args...)); err != nil {
		t.Fatalf("running the projected command: %v", err)
	}
}

// bindThrough is runBind specialized to everyKindRequest, returning the bound
// value so the type assertions stay out of the test bodies.
func bindThrough(t *testing.T, proto everyKindRequest, args ...string) everyKindRequest {
	t.Helper()
	var got everyKindRequest
	runBind(t, proto, &got, args...)
	return got
}
