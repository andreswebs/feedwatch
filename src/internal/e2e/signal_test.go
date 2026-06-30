package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestPollInterruptPersistsAndExits covers the graceful-shutdown contract: when
// a poll is interrupted while one feed is still in flight, the feed that already
// completed is persisted and queryable, the process exits 128+signum (130 for
// SIGINT, 143 for SIGTERM), and no internal-category error leaks to stderr. It
// drives the real binary so it exercises the signal handling that lives in main.
func TestPollInterruptPersistsAndExits(t *testing.T) {
	cases := []struct {
		name string
		sig  syscall.Signal
		want int
	}{
		{"SIGINT", syscall.SIGINT, 130},
		{"SIGTERM", syscall.SIGTERM, 143},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pollInterruptScenario(t, tc.sig, tc.want)
		})
	}
}

func pollInterruptScenario(t *testing.T, sig syscall.Signal, wantExit int) {
	t.Helper()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed))
	}))
	defer fast.Close()

	// The slow feed blocks until its in-flight request is cancelled by the
	// interrupt (or a generous ceiling elapses as a backstop), so the poll is
	// guaranteed to still be fetching when the signal arrives.
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer slow.Close()

	db := filepath.Join(t.TempDir(), "feedwatch.db")
	fastURL := fast.URL + "/feed.xml"
	slowURL := slow.URL + "/feed.xml"

	opml := fmt.Sprintf(`<?xml version="1.0"?><opml version="2.0"><body>`+
		`<outline type="rss" text="fast" xmlUrl="%s"/>`+
		`<outline type="rss" text="slow" xmlUrl="%s"/>`+
		`</body></opml>`, fastURL, slowURL)
	opmlPath := filepath.Join(t.TempDir(), "subs.opml")
	if err := os.WriteFile(opmlPath, []byte(opml), 0o600); err != nil {
		t.Fatalf("write opml: %v", err)
	}

	//nolint:gosec // G204: the suite runs the binary it just built with test-controlled args.
	if out, err := exec.Command(binPath, "--quiet", "--db", db, "import", opmlPath).CombinedOutput(); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}

	//nolint:gosec // G204: same test-controlled binary and args.
	poll := exec.Command(binPath, "--quiet", "--db", db, "poll", "--all")
	var pout, perr bytes.Buffer
	poll.Stdout = &pout
	poll.Stderr = &perr
	if err := poll.Start(); err != nil {
		t.Fatalf("start poll: %v", err)
	}

	time.Sleep(2 * time.Second) // let the fast feed complete before interrupting
	if err := poll.Process.Signal(sig); err != nil {
		t.Fatalf("signal poll: %v", err)
	}

	if code := exitCodeOf(poll.Wait()); code != wantExit {
		t.Fatalf("poll exit = %d, want %d\nstdout: %s\nstderr: %s", code, wantExit, pout.String(), perr.String())
	}

	if strings.Contains(perr.String(), `"category":"internal"`) {
		t.Fatalf("internal-category error leaked on interrupt:\nstderr: %s", perr.String())
	}

	//nolint:gosec // G204: same test-controlled binary and args.
	iout, err := exec.Command(binPath, "--quiet", "--db", db, "items", "--feed", fastURL).Output()
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(iout, &env); err != nil {
		t.Fatalf("decode items: %v\n%s", err, iout)
	}
	if len(env.Items) == 0 {
		t.Fatalf("completed feed's items were not persisted across the interrupt:\n%s", iout)
	}
}
