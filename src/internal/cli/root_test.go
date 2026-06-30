package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
)

// runResult captures everything an agent (or test) observes from one invocation:
// the stdout and stderr text and the exit code our boundary selected.
type runResult struct {
	out, err string
	code     int
	exited   bool
}

// runRoot drives NewRootCommand(d).Run with temp-file stdout/stderr and a
// captured OsExiter, returning what the invocation produced. It mirrors how
// main wires the command, minus the real os.Exit.
func runRoot(t *testing.T, version string, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)

	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   core.SystemClock,
		Version: version,
		Out:     outF,
		Err:     errF,
	}

	var res runResult
	oldExiter := cliv3.OsExiter
	cliv3.OsExiter = func(code int) {
		res.code = code
		res.exited = true
	}
	t.Cleanup(func() { cliv3.OsExiter = oldExiter })

	if err := NewRootCommand(d).Run(t.Context(), args); err != nil {
		// main relays a residual error to HandleExitCoder; our ExitErrHandler
		// already set the code via OsExiter, so nothing more to do here.
		_ = err
	}

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
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

// errorPayload mirrors the stderr error shape so tests can assert on category
// and message without depending on the output package's unexported type.
type errEnvelope struct {
	Error struct {
		Category string `json:"category"`
		FeedURL  string `json:"feed_url"`
		Status   int    `json:"status"`
		Message  string `json:"message"`
	} `json:"error"`
}

func TestVersionJSON(t *testing.T) {
	res := runRoot(t, "1.2.3", "feedwatch", "--version")

	if res.exited {
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
	res := runRoot(t, "1.2.3", "feedwatch", "--format", "text", "--version")

	if res.exited {
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
// boundary that real subcommands will rely on.
func runWithStub(t *testing.T, action cliv3.ActionFunc, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   core.SystemClock,
		Version: "1.2.3",
		Out:     outF,
		Err:     errF,
	}

	root := NewRootCommand(d)
	root.Commands = append(root.Commands, &cliv3.Command{Name: "stub", Action: action})

	var res runResult
	oldExiter := cliv3.OsExiter
	cliv3.OsExiter = func(code int) {
		res.code = code
		res.exited = true
	}
	t.Cleanup(func() { cliv3.OsExiter = oldExiter })

	_ = root.Run(t.Context(), append([]string{"feedwatch"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
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

func TestActionConfigErrorExits1(t *testing.T) {
	action := func(_ context.Context, _ *cliv3.Command) error {
		return &core.FeedError{Category: core.CatConfig, Message: "store path is not writable"}
	}
	res := runWithStub(t, action, "stub")

	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty for a hard failure", res.out)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatConfig) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatConfig)
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
	res := runRoot(t, "1.2.3", "feedwatch", "--format", "yaml")

	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatUsage) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatUsage)
	}
}

func TestInvalidConcurrencyIsConfigError(t *testing.T) {
	res := runWithStub(t, func(_ context.Context, _ *cliv3.Command) error { return nil }, "--concurrency", "0", "stub")

	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatConfig) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatConfig)
	}
}

func TestNoVersionSubcommand(t *testing.T) {
	d := Deps{Cfg: config.Defaults(), Version: "1.2.3", Out: tempFile(t), Err: tempFile(t)}
	root := NewRootCommand(d)
	if root.Command("version") != nil {
		t.Errorf("a version subcommand exists; --version must be the only version path")
	}
}

func TestShellCompletionEnabled(t *testing.T) {
	d := Deps{Cfg: config.Defaults(), Version: "1.2.3", Out: tempFile(t), Err: tempFile(t)}
	if !NewRootCommand(d).EnableShellCompletion {
		t.Errorf("shell completion is not enabled")
	}
}

func TestCompletionUnknownShellIsUsageError(t *testing.T) {
	res := runRoot(t, "1.2.3", "feedwatch", "completion", "powershell")

	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty", res.out)
	}

	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatUsage) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatUsage)
	}
}

func TestCompletionKnownShellEmitsScript(t *testing.T) {
	res := runRoot(t, "1.2.3", "feedwatch", "completion", "pwsh")

	if res.exited && res.code != 0 {
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

	res := runRoot(t, "1.2.3", "feedwatch", "migrate", "--status")

	if res.exited && res.code != 0 {
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
	res := runRoot(t, "1.2.3", "feedwatch", "bogus")

	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty", res.out)
	}

	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatUsage) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatUsage)
	}
}
