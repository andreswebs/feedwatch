package e2e_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This exec-based suite is the conditional half of ADR 0006: it exists only for
// process-level behavior the in-process golden harness cannot reach. As of the
// ADR 0006 harness adoption that is signal handling and the 128+signum exit
// codes (signal_test.go); every stream-contract scenario moved in-process to the
// golden-triple suite in internal/command (golden_test.go,
// golden_scenarios_test.go) and its exec goldens were deleted rather than kept
// as a drifting second copy. What remains here is the machinery those signal
// tests share: a once-built real binary and the exit-code extractor.

// binPath is the freshly built feedwatch binary the signal tests drive. It is
// built once in TestMain so every scenario exercises the same artifact a user
// runs.
var binPath string

// rssFeed is a deterministic RSS 2.0 body served by the signal tests' fast feed.
const rssFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example Feed</title>
    <link>https://example.test/</link>
    <description>An example feed for end-to-end tests</description>
    <item>
      <title>First post</title>
      <link>https://example.test/first</link>
      <description>The first post.</description>
      <guid isPermaLink="false">urn:example:1</guid>
      <pubDate>Sat, 27 Jun 2026 10:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "feedwatch-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mkdir temp:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "feedwatch")

	// -buildvcs=false keeps Main.Version at "(devel)" so version.Current()
	// falls back to "dev"; with go.mod at the repo (and VCS) root a bare build
	// would otherwise stamp a nondeterministic pseudo-version from the git tag.
	//nolint:gosec // G204: building the module's own command with a fixed import path and a temp output.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "github.com/andreswebs/feedwatch/cmd/feedwatch")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build feedwatch:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// exitCodeOf extracts a process exit code from a *exec.ExitError, returning 0
// for a clean exit and -1 for a failure to launch the process at all.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
