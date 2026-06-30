package testsupport

import (
	"embed"
	"testing"
)

//go:embed testdata
var fixtures embed.FS

// Fixture returns the bytes of the named fixture under testdata (for example
// "valid_rss.xml"). It fails the test if the fixture does not exist, so the
// corpus stays the single source of feed bodies for unit tests.
func Fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return b
}
