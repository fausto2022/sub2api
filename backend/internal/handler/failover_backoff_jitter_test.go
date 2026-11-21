package handler

import (
	"testing"
	"time"
)

func TestFailoverBackoffJitterBounds(t *testing.T) {
	fs := NewFailoverState(3, false)
	for _, d := range []time.Duration{sameAccountRetryDelay, singleAccountBackoffDelay, 8 * time.Second} {
		if got := fs.retryDelay(d); got != d {
			t.Fatalf("legacy delay = %v, want %v", got, d)
		}
	}
	fs.jitterEnabled = true
	for _, d := range []time.Duration{sameAccountRetryDelay, singleAccountBackoffDelay, 8 * time.Second} {
		first := fs.retryDelay(d)
		var varied bool
		for i := 0; i < 256; i++ {
			got := fs.retryDelay(d)
			if got < d-d/5 || got > d+d/5 {
				t.Fatalf("delay %v outside ±20%% of %v", got, d)
			}
			varied = varied || first != got
		}
		if !varied {
			t.Fatalf("no variation for delay %v", d)
		}
	}
}
