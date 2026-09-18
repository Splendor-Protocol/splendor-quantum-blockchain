package network

import (
	"testing"
	"time"
)

// The symbol phase of a coded fetch runs under one budget: a two-minute
// floor, two seconds per committed symbol above that, and a ten-minute cap
// that a hostile CodebookSize cannot exceed.
func TestObjectFetchDeadline(t *testing.T) {
	cases := map[uint32]time.Duration{
		0:       2 * time.Minute,
		60:      2 * time.Minute,
		61:      122 * time.Second,
		200:     400 * time.Second,
		300:     10 * time.Minute,
		1280:    10 * time.Minute,
		1 << 31: 10 * time.Minute,
	}
	for n, want := range cases {
		if got := objectFetchDeadline(n); got != want {
			t.Fatalf("objectFetchDeadline(%d) = %s, want %s", n, got, want)
		}
	}
}
