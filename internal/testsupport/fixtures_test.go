package testsupport_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

func TestFixtureReturnsCorpusBytes(t *testing.T) {
	for _, name := range []string{
		"valid_rss.xml", "valid_atom.xml", "valid_jsonfeed.json",
		"malformed.xml", "bad_dates.xml", "iso8859-1.xml", "utf16-bom.xml",
	} {
		if got := testsupport.Fixture(t, name); len(got) == 0 {
			t.Errorf("Fixture(%q) is empty", name)
		}
	}
}

func TestFixtureValidFeedsParse(t *testing.T) {
	p := parse.New()
	for _, name := range []string{"valid_rss.xml", "valid_atom.xml", "valid_jsonfeed.json"} {
		feed, err := p.Parse(context.Background(), testsupport.Fixture(t, name), "https://example/")
		if err != nil {
			t.Errorf("Parse(%q): %v", name, err)
			continue
		}
		if len(feed.Items) == 0 {
			t.Errorf("Parse(%q): no items", name)
		}
	}
}

func TestFixtureMalformedStillYieldsItems(t *testing.T) {
	p := parse.New()
	feed, err := p.Parse(context.Background(), testsupport.Fixture(t, "malformed.xml"), "https://malformed.example/")
	if err != nil {
		t.Fatalf("Parse(malformed.xml): %v", err)
	}
	if len(feed.Items) == 0 {
		t.Error("Parse(malformed.xml): expected lenient parse to yield at least one item")
	}
}

func TestFixtureEncodedHaveExpectedByteSignatures(t *testing.T) {
	if utf16 := testsupport.Fixture(t, "utf16-bom.xml"); !bytes.HasPrefix(utf16, []byte{0xff, 0xfe}) {
		t.Error("utf16-bom.xml: missing UTF-16LE byte-order mark")
	}
	// ISO-8859-1 encodes é as the single byte 0xE9, invalid as standalone UTF-8.
	if latin1 := testsupport.Fixture(t, "iso8859-1.xml"); !bytes.Contains(latin1, []byte{0xe9}) {
		t.Error("iso8859-1.xml: expected a raw 0xE9 latin1 byte")
	}
}
