package server

import (
	"testing"
	"time"
)

func TestNotificationRetryDelayIsBoundedAndExponential(t *testing.T) {
	cases := []struct {
		previous time.Duration
		want     time.Duration
	}{
		{0, time.Second},
		{time.Second, 2 * time.Second},
		{2 * time.Second, 4 * time.Second},
		{16 * time.Second, 32 * time.Second},
		{32 * time.Second, time.Minute},
		{time.Minute, time.Minute},
	}
	for _, tc := range cases {
		if got := notificationRetryDelay(tc.previous); got != tc.want {
			t.Errorf("notificationRetryDelay(%s) = %s, want %s", tc.previous, got, tc.want)
		}
	}
}
