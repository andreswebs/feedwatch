package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

const discoverRSS = `<?xml version="1.0"?>
<rss version="2.0"><channel><title>Probed Feed</title>
<item><title>i</title><guid>g1</guid></item></channel></rss>`

// runDiscover drives the discover command through the root with the real fetcher
// and parser (pointed at an httptest server via the injected interfaces) and an
// injected store, capturing stdout, stderr, and the exit code.
func runDiscover(t *testing.T, st store.Store, args ...string) runResult {
	t.Helper()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("fetch.New: %v", err)
	}

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Version: "1.2.3",
		Store:   st,
		Fetch:   f,
		Parse:   parse.New(),
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "discover"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

type discoverEnvelope struct {
	Candidates []struct {
		Title  string `json:"title"`
		URL    string `json:"url"`
		Type   string `json:"type"`
		Source string `json:"source"`
	} `json:"candidates"`
}

// TestDiscoverCommandReportsCandidatesAndWritesNothing covers behavior 5: the
// discover command lists candidates as JSON and performs no store writes.
func TestDiscoverCommandReportsCandidatesAndWritesNothing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head></html>`))
	})
	mux.HandleFunc("/feed", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(discoverRSS))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	res := runDiscover(t, st, srv.URL)

	if res.exited {
		t.Errorf("discover should exit 0, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var env discoverEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a discover envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(env.Candidates), env.Candidates)
	}
	if env.Candidates[0].Source != "probe" {
		t.Errorf("source = %q, want probe", env.Candidates[0].Source)
	}

	feeds, err := st.ListFeeds(context.Background(), core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("discover wrote %d feed(s) to the store, want 0", len(feeds))
	}
}

// TestDiscoverEmptyIsEmptyArray covers the empty case: a page with no feeds
// yields {"candidates":[]}, never null, so an agent can iterate without a nil
// check.
func TestDiscoverEmptyIsEmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head></html>`))
	}))
	defer srv.Close()

	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	res := runDiscover(t, st, srv.URL)

	if res.exited {
		t.Errorf("discover should exit 0, got code %d", res.code)
	}
	if got := res.out; !json.Valid([]byte(got)) {
		t.Fatalf("stdout is not valid JSON: %q", got)
	}
	var env discoverEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Candidates == nil {
		t.Errorf("candidates is null, want an empty array")
	}
}

// TestDiscoverBadURLIsUsageError covers URL validation: a bare host (no scheme)
// is a usage error, exit 1, with empty stdout.
func TestDiscoverBadURLIsUsageError(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	res := runDiscover(t, st, "example.com")

	if !res.exited || res.code != 1 {
		t.Fatalf("bad URL should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a usage error", res.out)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not an error envelope: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatUsage) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatUsage)
	}
}
