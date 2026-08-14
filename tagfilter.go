package feedwatch

import "github.com/andreswebs/feedwatch/core"

// tagFilter validates a request's tag selection and resolves it into a store
// filter. It is shared by every use case that accepts --tag/--match so the rules
// are stated once and cannot drift between commands. An empty tags slice yields
// a filter that matches every feed, and an empty match resolves to MatchAll.
func tagFilter(tags []string, match string) (core.ListFilter, error) {
	if err := core.ValidateTags(tags); err != nil {
		return core.ListFilter{}, err
	}
	m, err := core.ParseTagMatch(match)
	if err != nil {
		return core.ListFilter{}, err
	}
	return core.ListFilter{Tags: tags, Match: m}, nil
}
