package backup

import (
	"context"
	"time"
)

// StartScheduler runs one backup per interval. The first run occurs after the
// configured interval, avoiding an unexpected large upload during deployment.
// Failures are logged and retried at the next interval, never every minute.
func (m *Manager) StartScheduler(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 12 * time.Hour
	}
	go func() {
		timer := time.NewTimer(every)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if _, err := m.Run(ctx); err != nil && err != ErrAlreadyRunning && m.opts.Logger != nil {
					m.opts.Logger.Printf("scheduled backup failed: %v", err)
				}
				timer.Reset(every)
			}
		}
	}()
}
