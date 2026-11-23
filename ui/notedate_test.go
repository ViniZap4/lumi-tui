package ui

import (
	"testing"
	"time"
)

func TestNoteDateZeroIsBlank(t *testing.T) {
	if got := noteDate(time.Time{}); got != "" {
		t.Fatalf("zero time rendered as %q", got)
	}
	if got := noteDate(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)); got != "Nov 23, 2026" {
		t.Fatalf("got %q", got)
	}
}
