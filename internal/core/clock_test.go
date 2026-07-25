package core_test

import (
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

func TestSystemClockReturnsCurrentTime(t *testing.T) {
	before := time.Now()
	got := core.SystemClock()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Fatalf("SystemClock() = %v, want within [%v, %v]", got, before, after)
	}
}

func TestFixedClockReturnsConstantTime(t *testing.T) {
	want := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	var clk core.Clock = func() time.Time { return want }

	for range 3 {
		if got := clk(); !got.Equal(want) {
			t.Fatalf("fixed clock = %v, want %v", got, want)
		}
	}
}
