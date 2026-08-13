package feedwatch

import (
	"context"
	"encoding/json"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/poll"
)

// PollRequest selects the feeds to poll. An empty Feeds polls the feeds whose
// interval has elapsed; naming feeds polls exactly those, by URL or alias,
// regardless of schedule. Force polls every active feed, ignoring the schedule.
type PollRequest struct {
	Feeds []string `arg:"feed" variadic:"true"`
	Force bool     `flag:"force" alias:"all" usage:"poll every active feed, ignoring the schedule"`
}

// Validate reports whether the request is usable. Every field is optional, so it
// always succeeds; the method exists so every request type is validated
// uniformly by a frontend. An unknown feed reference is a store-resolution
// failure rather than a syntactic one, so it surfaces from Poll.
func (r PollRequest) Validate() error { return nil }

// PollFailure is one failed feed in the poll envelope: the feed URL, its error
// category, the HTTP status when the category is http (omitted otherwise), and
// the bare human detail of the failure (always present).
type PollFailure struct {
	FeedURL  string        `json:"feed_url"`
	Category core.Category `json:"category"`
	Status   int           `json:"status,omitempty"`
	Message  string        `json:"message"`
}

// PollResult is the poll result envelope: how many feeds were polled, succeeded,
// failed, and skipped, the count of items parsed from the wire (fetched), the
// count of newly-seen items (new_items), the count of wire items that were
// already known (deduped = fetched - new_items), the items themselves, one
// entry per failed feed, and one entry per feed renamed by a permanent redirect.
// failures and renamed are always present, empty ([]) when nothing failed or was
// renamed, so a partial failure or a feed identity change is observable from the
// result alone.
type PollResult struct {
	Head
	Polled    int               `json:"polled"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Skipped   int               `json:"skipped"`
	Fetched   int               `json:"fetched"`
	NewItems  int               `json:"new_items"`
	Deduped   int               `json:"deduped"`
	Items     []core.Item       `json:"items"`
	Failures  []PollFailure     `json:"failures"`
	Renamed   []core.FeedRename `json:"renamed"`
}

// MarshalJSON coalesces the owned collections so items, failures, and renamed
// always serialize as [] rather than null.
func (r PollResult) MarshalJSON() ([]byte, error) {
	type alias PollResult
	a := alias(r)
	if a.Items == nil {
		a.Items = []core.Item{}
	}
	if a.Failures == nil {
		a.Failures = []PollFailure{}
	}
	if a.Renamed == nil {
		a.Renamed = []core.FeedRename{}
	}
	return json.Marshal(a)
}

// ExitCode derives the process exit code from the outcome: 0 when nothing was
// polled or every polled feed succeeded, 2 when every polled feed failed, and 3
// when some succeeded and some failed. It agrees with the orchestrator's own
// derivation for every input, which a test pins.
func (r PollResult) ExitCode() int {
	if r.Polled == 0 || r.Failed == 0 {
		return 0
	}
	if r.Failed == r.Polled {
		return 2
	}
	return 3
}

// Poll fetches the targeted feeds, reports the items it had never seen before,
// and updates each feed's state: validators, schedule, and failure lifecycle. A
// feed that fails is result data, recorded in Failures and never cancelling its
// siblings; crossing the failure threshold auto-disables the feed and raises an
// advisory through the App's Warner.
//
// Poll can return a populated result alongside a non-nil error. That is the
// mid-persist case: some feeds' writes already committed before the store
// failed, so the result is a truthful partial envelope the caller should still
// render. res.Polled > 0 identifies it. When res.Polled == 0 the failure was
// early (an unreachable store, an unresolvable feed reference) and nothing was
// done, so the result must not be rendered.
func (a *App) Poll(ctx context.Context, req PollRequest) (PollResult, error) {
	if err := req.Validate(); err != nil {
		return PollResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return PollResult{}, err
	}
	fetcher, err := a.resolveFetcher()
	if err != nil {
		return PollResult{}, err
	}

	deps := poll.Deps{
		Store:            st,
		Fetcher:          fetcher,
		Parser:           a.resolveParser(),
		Clock:            a.clock,
		Concurrency:      a.cfg.Concurrency,
		PerHostDelay:     a.cfg.PerHostDelay,
		DefaultInterval:  a.cfg.DefaultInterval,
		FailureThreshold: a.cfg.FailureThreshold,
		MaxBackoff:       a.cfg.MaxBackoff,
		Warn:             a.warnf,
	}

	result, feedErrs, err := poll.Run(ctx, deps, req.Feeds, req.Force)
	if err != nil && result.Polled == 0 {
		// Nothing was persisted, so there is no truthful envelope to hand back.
		return PollResult{}, err
	}
	return shapePollResult(result, feedErrs), err
}

// shapePollResult builds the result envelope from a poll outcome and its
// per-feed errors, shared by the success and mid-persist-failure paths so they
// cannot drift.
func shapePollResult(result poll.Result, feedErrs []*core.FeedError) PollResult {
	failures := make([]PollFailure, 0, len(feedErrs))
	for _, fe := range feedErrs {
		failures = append(failures, PollFailure{
			FeedURL:  fe.FeedURL,
			Category: fe.Category,
			Status:   fe.Status,
			Message:  fe.Detail(),
		})
	}

	return PollResult{
		Head:      OKHead(),
		Polled:    result.Polled,
		Succeeded: result.Polled - result.Failed,
		Failed:    result.Failed,
		Skipped:   result.Skipped,
		Fetched:   result.Fetched,
		NewItems:  result.NewItems,
		Deduped:   result.Deduped,
		Items:     result.Items,
		Failures:  failures,
		Renamed:   result.Renamed,
	}
}
