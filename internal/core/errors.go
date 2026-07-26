package core

import (
	"fmt"

	"github.com/andreswebs/feedwatch/internal/terr"
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

// Registered coded sentinels for static, whole-invocation failures. Each is a
// terr.E carrying its ADR 0001 exit code (documented next to the code, per ADR
// 0002) and a consumer-facing hint; they are matched at the error boundary with
// errors.Is and enumerated as data by the schema command via terr.All.
var (
	// ErrUsage marks a usage error (bad arguments or flags): exit 64 (EX_USAGE),
	// the sysexits.h class for a misused command surface.
	ErrUsage = terr.New("usage_error", 64,
		"check the command arguments and flags; run the command with --help",
		"usage error")
	// ErrSchemaTooNew marks a stored schema newer than the binary supports: exit
	// 65 (EX_DATAERR), because the persisted data is unreadable by this version.
	ErrSchemaTooNew = terr.New("schema_too_new", 65,
		"upgrade feedwatch to a version that understands this store's schema",
		"schema version newer than supported")
	// ErrStoreUnavailable marks an unreachable or unusable store: exit 69
	// (EX_UNAVAILABLE), the sysexits.h class for a service that cannot be reached.
	ErrStoreUnavailable = terr.New("store_unavailable", 69,
		"check the store path or connection string and its permissions",
		"store unavailable")
	// ErrConfig marks an invalid configuration: exit 78 (EX_CONFIG), the
	// sysexits.h class for a configuration error.
	ErrConfig = terr.New("config_error", 78,
		"check the configuration flags and environment variables",
		"configuration error")
	// ErrInternal marks an unexpected or unclassified failure, including
	// recovered panics: exit 70 (EX_SOFTWARE), the sysexits.h internal-error
	// class. It is the fallback the error boundary reports for anything that does
	// not classify as one of the classes above.
	ErrInternal = terr.New("internal_error", 70,
		"this is a bug; rerun with --log-level debug and report it",
		"internal error")
)

// Registered class sentinels for feed-scoped failures. Per-feed failures are
// result data (they travel in the stdout failures arrays and drive the poll and
// check aggregate codes 2 and 3), so these never reach the boundary as a
// returned error on a healthy path. They still carry a non-zero exit code (70,
// the internal-error class) so that if one does reach the boundary, it is a bug
// path that exits 70 loudly rather than silently exiting 0.
var (
	// ErrHTTP is the class sentinel for a non-success HTTP response.
	ErrHTTP = terr.New("http_error", 70,
		"check the feed URL and the reported HTTP status",
		"http error")
	// ErrNetwork is the class sentinel for a connection-level failure.
	ErrNetwork = terr.New("feed_unreachable", 70,
		"check that the feed host is reachable and its DNS resolves",
		"feed unreachable")
	// ErrParse is the class sentinel for an unparseable feed body.
	ErrParse = terr.New("parse_error", 70,
		"the feed body could not be parsed as RSS, Atom, or JSON Feed",
		"parse error")
	// ErrTimeout is the class sentinel for a connect or overall deadline expiry.
	ErrTimeout = terr.New("timeout_error", 70,
		"the feed did not respond within the timeout; increase --timeout or retry",
		"timeout error")
)

// class returns the per-Category class sentinel that supplies FeedError's
// class-level Code, ExitCode, and Hint. Whole-invocation categories delegate to
// the sentinels above so one code set covers both the feed-scoped and the
// whole-invocation paths.
func (e *FeedError) class() *terr.E {
	switch e.Category {
	case CatUsage:
		return ErrUsage
	case CatConfig:
		return ErrConfig
	case CatStore:
		return ErrStoreUnavailable
	case CatHTTP:
		return ErrHTTP
	case CatNetwork:
		return ErrNetwork
	case CatParse:
		return ErrParse
	case CatTimeout:
		return ErrTimeout
	default:
		return ErrInternal
	}
}

// Code returns the class-level machine code delegated to the category sentinel.
func (e *FeedError) Code() string { return e.class().Code() }

// ExitCode returns the class-level exit code delegated to the category sentinel.
func (e *FeedError) ExitCode() int { return e.class().ExitCode() }

// Hint returns the class-level remediation hint delegated to the category sentinel.
func (e *FeedError) Hint() string { return e.class().Hint() }

// Is delegates class-level identity to the category sentinel, so
// errors.Is(fe, classSentinel) matches while the instance keeps its own detail.
// It reports false for any other target, leaving Unwrap to reach the cause.
func (e *FeedError) Is(target error) bool {
	return e.class().Is(target)
}

// feedErrorDetails is the render-ready detail structure surfaced under the error
// envelope's "details" field. Status is omitted when zero and feed_url when
// empty, matching the stderr error payload's omission behavior.
type feedErrorDetails struct {
	FeedURL string `json:"feed_url,omitempty"`
	Status  int    `json:"status,omitempty"`
}

// ErrorDetails returns the render-ready {feed_url, status} structure for the
// error envelope, satisfying terr.Detailed. A whole-invocation FeedError with
// no feed scope (empty URL and zero status) has nothing to surface, so it
// returns nil and the envelope omits the details field entirely rather than
// rendering an empty object.
func (e *FeedError) ErrorDetails() any {
	if e.FeedURL == "" && e.Status == 0 {
		return nil
	}
	return feedErrorDetails{FeedURL: e.FeedURL, Status: e.Status}
}
