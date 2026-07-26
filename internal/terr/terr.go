// Package terr defines the coded-error contract of docs/adr/0002-error-handling.md:
// every failure that can reach a consumer carries a stable machine code, a
// process exit code from docs/adr/0001-exit-code-taxonomy.md, and an optional
// remediation hint. The boundary discovers all three through the Coded
// interface via errors.As, and the registered code set is enumerable as data so
// the schema command cannot drift from the real error surface.
package terr

import (
	"fmt"
	"sync"
)

// Coded is an error that carries a stable machine code, a process exit code,
// and a user-facing remediation hint. The exit boundary discovers it with
// errors.As and uses ExitCode to choose the process exit code.
type Coded interface {
	error
	Code() string
	ExitCode() int
	Hint() string
}

// Detailed is an optional interface for errors that carry render-ready
// structured details, surfaced in the error envelope's "details" field.
type Detailed interface {
	ErrorDetails() any
}

// E is a coded error. Registered sentinels are immutable package-level values:
// Wrap and WithDetails return copies, so a sentinel is safe to share across
// goroutines by construction.
type E struct {
	code    string
	msg     string
	hint    string
	exit    int
	cause   error
	details any
}

var (
	mu       sync.Mutex
	registry = map[string]*E{}
	order    []*E
)

// New registers and returns an immutable sentinel with the given code, exit
// code, hint, and message. It panics on a duplicate code: duplicate
// registration is an init-time programmer error, so crashing at startup is the
// correct outcome (ADR 0002).
func New(code string, exit int, hint, msg string) *E {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[code]; dup {
		panic(fmt.Sprintf("terr: duplicate error code %q", code))
	}
	e := &E{code: code, exit: exit, hint: hint, msg: msg}
	registry[code] = e
	order = append(order, e)
	return e
}

// Newf builds an unregistered, one-off coded error whose message is formatted
// from format and args. It does not enter the registry, so it never collides
// with a sentinel code and never appears in All.
func Newf(code string, exit int, hint, format string, args ...any) *E {
	return &E{code: code, exit: exit, hint: hint, msg: fmt.Sprintf(format, args...)}
}

// All returns the registered sentinels in registration order. The result is a
// copy: mutating it does not affect the registry.
func All() []*E {
	mu.Lock()
	defer mu.Unlock()
	out := make([]*E, len(order))
	copy(out, order)
	return out
}

// Error renders "msg: cause" when a cause is attached, and the bare msg
// otherwise.
func (e *E) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

// Code returns the stable snake_case machine code.
func (e *E) Code() string { return e.code }

// ExitCode returns the process exit code from ADR 0001.
func (e *E) ExitCode() int { return e.exit }

// Hint returns the consumer-facing remediation hint, which may be empty.
func (e *E) Hint() string { return e.hint }

// Unwrap exposes the wrapped cause so errors.Is and errors.As traverse the
// chain.
func (e *E) Unwrap() error { return e.cause }

// Is reports whether target is an *E with the same code, so a Wrapped or
// WithDetails copy still satisfies errors.Is against its sentinel.
func (e *E) Is(target error) bool {
	t, ok := target.(*E)
	return ok && t.code == e.code
}

// Wrap returns a copy carrying cause; the receiver is unchanged, keeping
// sentinels immutable.
func (e *E) Wrap(cause error) *E {
	c := *e
	c.cause = cause
	return &c
}

// WithDetails returns a copy carrying render-ready details; the receiver is
// unchanged, keeping sentinels immutable.
func (e *E) WithDetails(details any) *E {
	c := *e
	c.details = details
	return &c
}

// ErrorDetails returns the render-ready details attached by WithDetails, or nil.
func (e *E) ErrorDetails() any { return e.details }
