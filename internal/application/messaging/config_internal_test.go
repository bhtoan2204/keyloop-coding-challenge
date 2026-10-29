package messaging

import (
	"testing"
	"time"
)

func TestRetryDelayBackoff(t *testing.T) {
	c := Config{
		RetryMinDelay: 500 * time.Millisecond,
		RetryMaxDelay: 30 * time.Second,
	}
	cases := []struct {
		failures int
		poison   bool
		want     time.Duration
	}{
		{1, false, 500 * time.Millisecond},
		{2, false, time.Second},
		{4, false, 4 * time.Second},
		{7, false, 30 * time.Second},
		{60, false, 30 * time.Second},
		{1, true, 30 * time.Second},
	}
	for _, tc := range cases {
		if got := c.retryDelay(tc.failures, tc.poison); got != tc.want {
			t.Errorf("retryDelay(%d, %v) = %v, want %v", tc.failures, tc.poison, got, tc.want)
		}
	}
}
