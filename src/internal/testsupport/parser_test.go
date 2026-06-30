package testsupport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// Compile-time conformance: the double satisfies the consumer interface.
var _ parse.Parser = (*testsupport.FakeParser)(nil)

func TestFakeParserReturnsCannedFeedForRegisteredBaseURL(t *testing.T) {
	const base = "https://blog.example/feed.xml"
	p := testsupport.NewFakeParser()
	want := parse.ParsedFeed{
		TTL:   45 * time.Minute,
		Items: []core.Item{{Title: "First", Link: "https://blog.example/first"}},
	}
	p.Register(base, want)

	got, err := p.Parse(context.Background(), []byte("ignored"), base)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.TTL != want.TTL || len(got.Items) != 1 || got.Items[0].Title != "First" {
		t.Errorf("parsed = %+v, want %+v", got, want)
	}
}

func TestFakeParserErrorsForUnregisteredBaseURL(t *testing.T) {
	p := testsupport.NewFakeParser()

	_, err := p.Parse(context.Background(), []byte("x"), "https://unknown.example/feed")
	if err == nil {
		t.Fatal("Parse: expected error for unregistered base URL, got nil")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatParse {
		t.Errorf("category = %q, want %q", fe.Category, core.CatParse)
	}
}
