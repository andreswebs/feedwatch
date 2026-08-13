package parse_test

import (
	"context"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/parse"
)

// fakeParser is a hand-written double proving the Parser interface is
// satisfiable. Behavior tests live with the gofeed implementation.
type fakeParser struct{}

func (*fakeParser) Parse(context.Context, []byte, string) (core.ParsedFeed, error) {
	return core.ParsedFeed{}, nil
}

// Compile-time conformance: the fake satisfies parse.Parser.
var _ parse.Parser = (*fakeParser)(nil)
