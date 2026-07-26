package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
)

// runResult captures everything an agent (or test) observes from one invocation:
// the stdout and stderr text and the exit code Run returned.
type runResult struct {
	out, err string
	code     int
}

// drive runs args through the real Run boundary with temp-file stdout/stderr,
// returning what the invocation produced. args exclude the program name (like
// os.Args[1:]); Run prepends it. It is the single test entry point every
// per-command helper funnels through, mirroring how main wires the command minus
// the real os.Exit.
func drive(t *testing.T, d Deps, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d.Out, d.Err = outF, errF

	code := Run(args, d)

	return runResult{
		out:  readFile(t, outF),
		err:  readFile(t, errF),
		code: code,
	}
}

// runCLI drives the root with only the version and clock seams, for tests that
// exercise global behavior (version, usage errors, completion, migrate). The
// program name is passed as args[0] to match os.Args and stripped before Run.
func runCLI(t *testing.T, version string, args ...string) runResult {
	t.Helper()
	return drive(t, Deps{Clock: core.SystemClock, Version: version}, args[1:]...)
}

func tempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func readFile(t *testing.T, f *os.File) string {
	t.Helper()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read %s: %v", f.Name(), err)
	}
	return string(b)
}

// errEnvelope mirrors the ADR 0005 stderr error envelope so tests can assert on
// code, message, hint, and the per-instance details without depending on the
// output package's unexported types.
type errEnvelope struct {
	SchemaVersion int  `json:"schema_version"`
	OK            bool `json:"ok"`
	Error         struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Details struct {
			FeedURL string `json:"feed_url"`
			Status  int    `json:"status"`
		} `json:"details"`
	} `json:"error"`
}

func TestVersionJSON(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "--version")

	if res.code != 0 {
		t.Errorf("version path should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var v struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Go      string `json:"go"`
	}
	if err := json.Unmarshal([]byte(res.out), &v); err != nil {
		t.Fatalf("stdout is not a JSON version object: %v\ngot: %q", err, res.out)
	}
	if v.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", v.Version)
	}
	if v.Go == "" {
		t.Errorf("go field is empty, want a runtime version")
	}
}

// TestVersionTextFormat covers the human side of walking-skeleton behavior 1:
// under --format text, --version prints a plain line (not JSON) to stdout with
// no ANSI color on a non-terminal, and exit 0.
func TestVersionTextFormat(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "--format", "text", "--version")

	if res.code != 0 {
		t.Errorf("version path should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if !strings.HasPrefix(res.out, "feedwatch 1.2.3") {
		t.Errorf("text version = %q, want a human line starting with %q", res.out, "feedwatch 1.2.3")
	}
	if strings.Contains(res.out, "{") {
		t.Errorf("text version looks like JSON: %q", res.out)
	}
	if strings.Contains(res.out, "\x1b[") {
		t.Errorf("text version to a non-terminal contains ANSI escape: %q", res.out)
	}
}

// runWithStub drives the root with an injected stub subcommand whose Action is
// provided by the test, exercising the Before hook, context wiring, and exit
// boundary that real subcommands rely on. It reaches into the interior seam
// (runCustom) to graft the stub, then runs the same boundary Run uses (finish).
func runWithStub(t *testing.T, action cliv3.ActionFunc, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{Clock: core.SystemClock, Version: "1.2.3", Out: outF, Err: errF}

	customize := func(root *cliv3.Command) {
		root.Commands = append(root.Commands, &cliv3.Command{Name: "stub", Action: action})
	}
	r, err := runCustom(t.Context(), args, d, customize)
	code := finish(r, err, nil)

	return runResult{
		out:  readFile(t, outF),
		err:  readFile(t, errF),
		code: code,
	}
}

func TestActionExitCodePartial(t *testing.T) {
	action := func(ctx context.Context, _ *cliv3.Command) error {
		if err := rendererFrom(ctx).Result(map[string]int{"polled": 2}); err != nil {
			return err
		}
		return exitError{code: 3}
	}
	res := runWithStub(t, action, "stub")

	if res.code != 3 {
		t.Errorf("exit code = %d, want 3", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty (outcome is reported on stdout)", res.err)
	}
	var env map[string]int
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not the JSON envelope: %v\ngot: %q", err, res.out)
	}
	if env["polled"] != 2 {
		t.Errorf("polled = %d, want 2", env["polled"])
	}
}

func TestActionConfigErrorExits78(t *testing.T) {
	action := func(_ context.Context, _ *cliv3.Command) error {
		return &core.FeedError{Category: core.CatConfig, Message: "store path is not writable"}
	}
	res := runWithStub(t, action, "stub")

	if res.code != 78 {
		t.Errorf("exit code = %d, want 78 (config)", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty for a hard failure", res.out)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrConfig.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrConfig.Code())
	}
	if env.Error.Message != "store path is not writable" {
		t.Errorf("message = %q, want the action's message", env.Error.Message)
	}
}

func TestLogLevelControlsStderr(t *testing.T) {
	logAction := func(ctx context.Context, _ *cliv3.Command) error {
		log := loggerFrom(ctx)
		log.Info("info-line")
		log.Debug("debug-line")
		return nil
	}

	t.Run("quiet suppresses info", func(t *testing.T) {
		res := runWithStub(t, logAction, "--quiet", "stub")
		if strings.Contains(res.err, "info-line") {
			t.Errorf("quiet emitted info log: %q", res.err)
		}
	})

	t.Run("debug level emits debug", func(t *testing.T) {
		res := runWithStub(t, logAction, "--log-level", "debug", "stub")
		if !strings.Contains(res.err, "debug-line") {
			t.Errorf("debug level suppressed debug log: %q", res.err)
		}
	})
}

func TestTextErrorNoColorOnNonTTY(t *testing.T) {
	action := func(_ context.Context, _ *cliv3.Command) error {
		return &core.FeedError{Category: core.CatConfig, Message: "bad config"}
	}
	res := runWithStub(t, action, "--format", "text", "stub")

	if strings.Contains(res.err, "\x1b[") {
		t.Errorf("text error to a non-terminal contains ANSI escape: %q", res.err)
	}
	if !strings.Contains(res.err, "bad config") {
		t.Errorf("text error missing message: %q", res.err)
	}
}

func TestConfigPrecedence(t *testing.T) {
	var got int
	capture := func(ctx context.Context, _ *cliv3.Command) error {
		got = configFrom(ctx).Concurrency
		return nil
	}

	t.Run("default when neither set", func(t *testing.T) {
		got = 0
		runWithStub(t, capture, "stub")
		if got != 8 {
			t.Errorf("concurrency = %d, want default 8", got)
		}
	})

	t.Run("env overrides default", func(t *testing.T) {
		got = 0
		t.Setenv("FEEDWATCH_CONCURRENCY", "4")
		runWithStub(t, capture, "stub")
		if got != 4 {
			t.Errorf("concurrency = %d, want env 4", got)
		}
	})

	t.Run("flag overrides env", func(t *testing.T) {
		got = 0
		t.Setenv("FEEDWATCH_CONCURRENCY", "4")
		runWithStub(t, capture, "--concurrency", "2", "stub")
		if got != 2 {
			t.Errorf("concurrency = %d, want flag 2", got)
		}
	})
}

func TestInvalidFormatIsUsageError(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "--format", "yaml")

	if res.code != 64 {
		t.Errorf("exit code = %d, want 64 (usage)", res.code)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrUsage.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
	}
}

// TestUnknownFlagUsageWording pins the framework coupling of the sanctioned
// usage classifier (onUsageError). The classifier trusts the framework to route
// only usage errors through OnUsageError and copies the framework's message
// verbatim; this test fails if urfave changes that wording for an unknown flag,
// per ADR 0002's requirement that the carve-out be covered by such a test.
func TestUnknownFlagUsageWording(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "--bogus-flag")

	if res.code != 64 {
		t.Errorf("exit code = %d, want 64 (usage)", res.code)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrUsage.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
	}
	// The framework's wording for an undefined flag; a change here means the
	// classifier's assumption must be re-checked.
	if !strings.Contains(env.Error.Message, "flag provided but not defined") {
		t.Errorf("message = %q, want the framework's unknown-flag wording", env.Error.Message)
	}
}

func TestInvalidConcurrencyIsConfigError(t *testing.T) {
	res := runWithStub(t, func(_ context.Context, _ *cliv3.Command) error { return nil }, "--concurrency", "0", "stub")

	if res.code != 78 {
		t.Errorf("exit code = %d, want 78 (config)", res.code)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrConfig.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrConfig.Code())
	}
}

func TestNoVersionSubcommand(t *testing.T) {
	d := Deps{Version: "1.2.3", Out: tempFile(t), Err: tempFile(t)}
	root := newRoot(d, new(error))
	if root.Command("version") != nil {
		t.Errorf("a version subcommand exists; --version must be the only version path")
	}
}

func TestShellCompletionEnabled(t *testing.T) {
	d := Deps{Version: "1.2.3", Out: tempFile(t), Err: tempFile(t)}
	if !newRoot(d, new(error)).EnableShellCompletion {
		t.Errorf("shell completion is not enabled")
	}
}

func TestCompletionUnknownShellIsUsageError(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "completion", "powershell")

	if res.code != 64 {
		t.Errorf("exit code = %d, want 64 (usage)", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty", res.out)
	}

	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrUsage.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
	}
}

func TestCompletionKnownShellEmitsScript(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "completion", "pwsh")

	if res.code != 0 {
		t.Errorf("exit code = %d, want 0", res.code)
	}
	if res.out == "" {
		t.Errorf("stdout is empty, want a completion script")
	}
}

func TestResolveStorePath(t *testing.T) {
	t.Run("explicit value passes through", func(t *testing.T) {
		const dsn = "postgres://user@host/feedwatch"
		got, isDefault := resolveStorePath(dsn)
		if got != dsn {
			t.Errorf("resolveStorePath(%q) = %q, want unchanged", dsn, got)
		}
		if isDefault {
			t.Errorf("resolveStorePath(%q) reported default, want explicit", dsn)
		}
	})

	t.Run("XDG_STATE_HOME default", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/xdg/state")
		want := filepath.Join("/xdg/state", "feedwatch", "feedwatch.db")
		got, isDefault := resolveStorePath("")
		if got != want {
			t.Errorf("resolveStorePath(\"\") = %q, want %q", got, want)
		}
		if !isDefault {
			t.Errorf("resolveStorePath(\"\") did not report default, want default")
		}
	})
}

// TestDefaultStoreDirAutoCreated covers fee-yigg: on a fresh machine with no
// FEEDWATCH_DB set, a command resolves the default XDG store path and creates
// the tool-owned feedwatch/ parent directory itself, so migrate succeeds with
// no manual setup.
func TestDefaultStoreDirAutoCreated(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("FEEDWATCH_DB", "")
	t.Setenv("XDG_STATE_HOME", xdg)

	res := runCLI(t, "1.2.3", "feedwatch", "migrate", "--status")

	if res.code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %q)", res.code, res.err)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	dir := filepath.Join(xdg, "feedwatch")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("default store dir %q not created: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("%q is not a directory", dir)
	}
}

func TestUnknownCommand(t *testing.T) {
	res := runCLI(t, "1.2.3", "feedwatch", "bogus")

	if res.code != 64 {
		t.Errorf("exit code = %d, want 64 (usage)", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty", res.out)
	}

	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrUsage.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
	}
}
