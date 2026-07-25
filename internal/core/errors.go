package core

import (
	"errors"
	"fmt"
)

// Category classifies an error so callers can branch on its kind without
// matching message strings. It is the enumeration referenced by the exit-code
// contract and the structured error objects on stderr.
type Category string

const (
	// CatNetwork covers connection-level failures (reset, refused, DNS).
	CatNetwork Category = "network"
	// CatHTTP covers non-success HTTP responses; the status is carried too.
	CatHTTP Category = "http"
	// CatParse covers feed bodies that cannot be parsed.
	CatParse Category = "parse"
	// CatTimeout covers connect or overall deadline expiry.
	CatTimeout Category = "timeout"
	// CatUsage covers bad arguments or flags (whole-invocation failure).
	CatUsage Category = "usage"
	// CatConfig covers invalid configuration (whole-invocation failure).
	CatConfig Category = "config"
	// CatStore covers an unreachable or unusable store (whole-invocation).
	CatStore Category = "store"
	// CatInternal covers unexpected failures, including recovered panics.
	CatInternal Category = "internal"
)

// FeedError is the structured error carried across feedwatch's layers. Callers
// recover it with errors.As and classify by Category, never by string. A
// FeedError may be feed-scoped (FeedURL set) or whole-invocation (FeedURL
// empty); the boundary uses Category and the sentinels to pick an exit code.
type FeedError struct {
	FeedURL  string   // empty for non-feed-scoped errors
	Category Category // error kind, never matched by string
	Status   int      // HTTP status when Category == CatHTTP, else 0
	Message  string   // human message; falls back to Err.Error()
	Err      error    // wrapped cause
}

// Error renders a lowercase-leading, punctuation-free message in the form
// "<category> <feed_url> (status): <message>", omitting the parts that are
// unset. The message prefers the explicit Message and falls back to the
// wrapped cause.
func (e *FeedError) Error() string {
	head := string(e.Category)
	if e.FeedURL != "" {
		head += " " + e.FeedURL
	}
	if e.Category == CatHTTP && e.Status != 0 {
		head += fmt.Sprintf(" (%d)", e.Status)
	}

	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		return head
	}
	return head + ": " + msg
}

// Unwrap exposes the wrapped cause so errors.Is and errors.As traverse the
// chain.
func (e *FeedError) Unwrap() error { return e.Err }

// Detail returns the bare human detail of the error: the explicit Message,
// falling back to the wrapped cause. It omits the category, feed URL, and
// status head that Error() prepends, for callers that carry those as
// structured fields already.
func (e *FeedError) Detail() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return ""
}

// NetworkErr builds a feed-scoped network-category error wrapping cause.
func NetworkErr(url string, err error) *FeedError {
	return &FeedError{FeedURL: url, Category: CatNetwork, Err: err}
}

// HTTPErr builds a feed-scoped HTTP-category error carrying the status.
func HTTPErr(url string, status int, err error) *FeedError {
	return &FeedError{FeedURL: url, Category: CatHTTP, Status: status, Err: err}
}

// ParseErr builds a feed-scoped parse-category error wrapping cause.
func ParseErr(url string, err error) *FeedError {
	return &FeedError{FeedURL: url, Category: CatParse, Err: err}
}

// TimeoutErr builds a feed-scoped timeout-category error wrapping cause.
func TimeoutErr(url string, err error) *FeedError {
	return &FeedError{FeedURL: url, Category: CatTimeout, Err: err}
}

// Sentinels for static, whole-invocation failures. They are matched at the
// error boundary with errors.Is and map to the sysexits.h failure classes
// per docs/adr/0001-exit-code-taxonomy.md.
var (
	// ErrUsage marks a usage error (bad arguments or flags).
	ErrUsage = errors.New("usage error")
	// ErrConfig marks an invalid configuration.
	ErrConfig = errors.New("configuration error")
	// ErrStoreUnavailable marks an unreachable or unusable store.
	ErrStoreUnavailable = errors.New("store unavailable")
	// ErrSchemaTooNew marks a stored schema newer than the binary supports.
	ErrSchemaTooNew = errors.New("schema version newer than supported")
)

// ExitCodeFor maps a whole-invocation error to a process exit code, following
// the family-wide taxonomy in docs/adr/0001-exit-code-taxonomy.md. A nil error
// or a purely feed-scoped *FeedError maps to 0: feed outcomes drive exit 2 and
// 3 from the poll aggregate, not from a returned error. Whole-invocation
// failures use the BSD sysexits.h range: usage 64 (EX_USAGE), data/schema-too-new
// 65 (EX_DATAERR), store-unavailable 69 (EX_UNAVAILABLE), config 78 (EX_CONFIG),
// and internal or unclassified failures 70 (EX_SOFTWARE). No whole-invocation
// failure returns 1; exit 1 and the 2-63 range are reserved for result classes.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}

	switch {
	case errors.Is(err, ErrUsage):
		return 64
	case errors.Is(err, ErrSchemaTooNew):
		return 65
	case errors.Is(err, ErrStoreUnavailable):
		return 69
	case errors.Is(err, ErrConfig):
		return 78
	}

	var fe *FeedError
	if errors.As(err, &fe) {
		switch fe.Category {
		case CatUsage:
			return 64
		case CatConfig:
			return 78
		case CatStore:
			return 69
		case CatInternal:
			return 70
		default:
			return 0
		}
	}

	return 70
}
