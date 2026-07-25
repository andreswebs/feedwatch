package testsupport_test

import (
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/testsupport"
)

func TestFixedClockReturnsConstantInstant(t *testing.T) {
	want := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	clk := testsupport.FixedClock(want)

	first := clk()
	time.Sleep(time.Millisecond)
	second := clk()

	if !first.Equal(want) {
		t.Errorf("first call = %v, want %v", first, want)
	}
	if !second.Equal(first) {
		t.Errorf("second call = %v, want %v (clock must not advance)", second, first)
	}
}
