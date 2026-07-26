package terr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/andreswebs/feedwatch/internal/terr"
)

// Behavior 1 (tracer): New returns an E whose Code, ExitCode, Hint, and Error
// report what was passed.
func TestNewReportsFields(t *testing.T) {
	e := terr.New("tracer_code", 64, "do the thing", "something went wrong")
	if e.Code() != "tracer_code" {
		t.Errorf("Code() = %q, want %q", e.Code(), "tracer_code")
	}
	if e.ExitCode() != 64 {
		t.Errorf("ExitCode() = %d, want 64", e.ExitCode())
	}
	if e.Hint() != "do the thing" {
		t.Errorf("Hint() = %q, want %q", e.Hint(), "do the thing")
	}
	if e.Error() != "something went wrong" {
		t.Errorf("Error() = %q, want %q", e.Error(), "something went wrong")
	}
}

// Behavior 2: New with an already-registered code panics.
func TestNewPanicsOnDuplicateCode(t *testing.T) {
	_ = terr.New("dup_code", 70, "", "first")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New with a duplicate code did not panic")
		}
	}()
	_ = terr.New("dup_code", 70, "", "second")
}

// Behavior 3: Wrap and WithDetails return copies. The receiver's cause and
// details stay nil, and errors.Is(copy, sentinel) still holds.
func TestWrapAndWithDetailsAreCopies(t *testing.T) {
	sentinel := terr.New("copy_code", 78, "", "base")

	cause := errors.New("boom")
	wrapped := sentinel.Wrap(cause)
	if sentinel.Unwrap() != nil {
		t.Error("Wrap mutated the receiver's cause")
	}
	if !errors.Is(wrapped.Unwrap(), cause) {
		t.Error("Wrap copy does not carry the cause")
	}
	if !errors.Is(wrapped, sentinel) {
		t.Error("errors.Is(wrapped, sentinel) is false")
	}

	detailed := sentinel.WithDetails(map[string]int{"n": 1})
	if sentinel.ErrorDetails() != nil {
		t.Error("WithDetails mutated the receiver's details")
	}
	if detailed.ErrorDetails() == nil {
		t.Error("WithDetails copy does not carry the details")
	}
	if !errors.Is(detailed, sentinel) {
		t.Error("errors.Is(detailed, sentinel) is false")
	}
}

// Behavior 4: Error renders "msg: cause" with a cause, bare msg otherwise.
func TestErrorRendersCause(t *testing.T) {
	sentinel := terr.New("render_code", 70, "", "top")
	if got := sentinel.Error(); got != "top" {
		t.Errorf("Error() = %q, want %q", got, "top")
	}
	wrapped := sentinel.Wrap(errors.New("inner"))
	if got := wrapped.Error(); got != "top: inner" {
		t.Errorf("Error() = %q, want %q", got, "top: inner")
	}
}

// Behavior 5: All returns registrations in order and is a copy.
func TestAllIsOrderedCopy(t *testing.T) {
	a := terr.New("all_a", 70, "", "a")
	b := terr.New("all_b", 70, "", "b")

	got := terr.All()
	ia, ib := -1, -1
	for i, e := range got {
		switch e.Code() {
		case "all_a":
			ia = i
		case "all_b":
			ib = i
		}
	}
	if ia == -1 || ib == -1 {
		t.Fatalf("All() missing registered sentinels: %v", got)
	}
	if ia >= ib {
		t.Errorf("All() out of registration order: all_a at %d, all_b at %d", ia, ib)
	}
	if got[ia] != a || got[ib] != b {
		t.Error("All() did not return the registered sentinel pointers")
	}

	got[0] = nil
	if again := terr.All(); again[0] == nil {
		t.Error("All() returned a slice aliasing the registry")
	}
}

// Behavior 6: errors.As recovers a registered sentinel through one and two
// levels of %w wrapping.
func TestErrorsAsThroughWrapping(t *testing.T) {
	sentinel := terr.New("as_code", 69, "", "base")
	one := fmt.Errorf("one: %w", sentinel)
	two := fmt.Errorf("two: %w", one)

	for _, err := range []error{sentinel, one, two} {
		var coded terr.Coded
		if !errors.As(err, &coded) {
			t.Errorf("errors.As did not recover Coded from %v", err)
			continue
		}
		if coded.Code() != "as_code" {
			t.Errorf("recovered Code() = %q, want %q", coded.Code(), "as_code")
		}
	}
}
