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
// error boundary with errors.Is and map to exit 1.
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

// ExitCodeFor maps a whole-invocation error to a process exit code. A nil error
// or a purely feed-scoped *FeedError maps to 0: feed outcomes drive exit 2 and
// 3 from the poll aggregate, not from a returned error. The whole-invocation
// sentinels and any FeedError whose Category is usage, config, store, or
// internal map to 1.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}

	switch {
	case errors.Is(err, ErrUsage),
		errors.Is(err, ErrConfig),
		errors.Is(err, ErrStoreUnavailable),
		errors.Is(err, ErrSchemaTooNew):
		return 1
	}

	var fe *FeedError
	if errors.As(err, &fe) {
		switch fe.Category {
		case CatUsage, CatConfig, CatStore, CatInternal:
			return 1
		default:
			return 0
		}
	}

	return 1
}
