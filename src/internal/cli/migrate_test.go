package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "feedwatch.db")
}

// TestMigrateStatusFreshDB covers walking-skeleton behavior 2: status on a
// brand-new database ensures the schema (applies pending migrations) and then
// reports the sqlite backend, a schema version of at least 1, and nothing
// pending, all as JSON on stdout with exit 0.
func TestMigrateStatusFreshDB(t *testing.T) {
	res := runRoot(t, "1.2.3", "feedwatch", "--db", tempDB(t), "migrate", "--status")

	if res.exited {
		t.Errorf("status should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var st MigrateStatus
	if err := json.Unmarshal([]byte(res.out), &st); err != nil {
		t.Fatalf("stdout is not a MigrateStatus object: %v\ngot: %q", err, res.out)
	}
	if st.Backend != "sqlite" {
		t.Errorf("backend = %q, want sqlite", st.Backend)
	}
	if st.SchemaVersion < 1 {
		t.Errorf("schema_version = %d, want >= 1 after ensuring schema", st.SchemaVersion)
	}
	if st.Pending != 0 {
		t.Errorf("pending = %d, want 0 after ensuring schema", st.Pending)
	}
}

// TestMigrateStatusIdempotent covers walking-skeleton behavior 3: two
// consecutive status runs against the same db report the same schema version
// with nothing pending and never error.
func TestMigrateStatusIdempotent(t *testing.T) {
	db := tempDB(t)

	first := runRoot(t, "1.2.3", "feedwatch", "--db", db, "migrate", "--status")
	second := runRoot(t, "1.2.3", "feedwatch", "--db", db, "migrate", "--status")

	for _, res := range []runResult{first, second} {
		if res.exited {
			t.Errorf("status should exit 0, got code %d (stderr: %q)", res.code, res.err)
		}
	}

	var a, b MigrateStatus
	if err := json.Unmarshal([]byte(first.out), &a); err != nil {
		t.Fatalf("first status is not a MigrateStatus object: %v\ngot: %q", err, first.out)
	}
	if err := json.Unmarshal([]byte(second.out), &b); err != nil {
		t.Fatalf("second status is not a MigrateStatus object: %v\ngot: %q", err, second.out)
	}
	if b.SchemaVersion != a.SchemaVersion {
		t.Errorf("schema_version drifted between runs: %d then %d", a.SchemaVersion, b.SchemaVersion)
	}
	if b.Pending != 0 {
		t.Errorf("second run pending = %d, want 0", b.Pending)
	}
}

// TestMigrateUnwritableDBExits1 covers walking-skeleton behavior 4: a --db whose
// parent directory does not exist cannot be opened, so the store-unavailable
// failure maps to exit 1 with a JSON error object on stderr and no stdout.
func TestMigrateUnwritableDBExits1(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "missing-dir", "feedwatch.db")
	res := runRoot(t, "1.2.3", "feedwatch", "--db", bad, "migrate", "--status")

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
	if env.Error.Category != string(core.CatStore) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatStore)
	}
}

// TestMigrateAppliesThenStatusClean covers behavior 2: a bare migrate applies
// the pending migrations, then a following status shows nothing pending and a
// matching schema version.
func TestMigrateAppliesThenStatusClean(t *testing.T) {
	db := tempDB(t)

	res := runRoot(t, "1.2.3", "feedwatch", "--db", db, "migrate")
	if res.exited {
		t.Errorf("migrate should exit 0, got code %d (stderr: %q)", res.code, res.err)
	}
	var applied MigrateApplied
	if err := json.Unmarshal([]byte(res.out), &applied); err != nil {
		t.Fatalf("stdout is not a MigrateApplied object: %v\ngot: %q", err, res.out)
	}
	if applied.Applied < 1 {
		t.Errorf("applied = %d, want >= 1", applied.Applied)
	}
	if applied.SchemaVersion < 1 {
		t.Errorf("schema_version = %d, want >= 1 after applying", applied.SchemaVersion)
	}

	res = runRoot(t, "1.2.3", "feedwatch", "--db", db, "migrate", "--status")
	var st MigrateStatus
	if err := json.Unmarshal([]byte(res.out), &st); err != nil {
		t.Fatalf("status stdout is not a MigrateStatus object: %v\ngot: %q", err, res.out)
	}
	if st.Pending != 0 {
		t.Errorf("pending = %d, want 0 after applying", st.Pending)
	}
	if st.SchemaVersion != applied.SchemaVersion {
		t.Errorf("status schema_version = %d, want %d", st.SchemaVersion, applied.SchemaVersion)
	}
}

// TestBackendName covers behavior 3: the reported backend is decided by the
// resolved --db URL scheme.
func TestBackendName(t *testing.T) {
	cases := map[string]string{
		"/var/lib/feedwatch/feedwatch.db":  "sqlite",
		"feedwatch.db":                     "sqlite",
		"postgres://user@host/feedwatch":   "postgres",
		"postgresql://user@host/feedwatch": "postgres",
	}
	for dsn, want := range cases {
		if got := backendName(dsn); got != want {
			t.Errorf("backendName(%q) = %q, want %q", dsn, got, want)
		}
	}
}
