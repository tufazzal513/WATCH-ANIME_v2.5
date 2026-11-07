package server

import (
	"context"
	"time"
)

const (
	maintenanceEvery = time.Hour
	keepErrorLogs    = 5000
	keepResetFor     = 48 * time.Hour
)

// MaintainOnce trims data that would otherwise grow forever: old client error reports, expired password-reset
// attempts and stale presence entries. Later parts (change-log pruning, backups) add their own jobs.
func (s *Server) MaintainOnce(ctx context.Context) {
	if s.deps.Repo != nil {
		if n, err := s.deps.Repo.PruneErrors(ctx, keepErrorLogs); err != nil {
			s.deps.Log.Warn("maintenance: prune error logs failed", "err", err)
		} else if n > 0 {
			s.deps.Log.Info("maintenance: old error logs removed", "count", n)
		}
		if _, err := s.deps.Repo.PruneResetAttempts(ctx, keepResetFor); err != nil {
			s.deps.Log.Warn("maintenance: prune reset attempts failed", "err", err)
		}
		if _, err := s.deps.Repo.PruneAdminSessions(ctx); err != nil {
			s.deps.Log.Warn("maintenance: prune admin sessions failed", "err", err)
		}
		if n, err := s.deps.Repo.PruneChanges(ctx, 100000, 30*24*time.Hour); err != nil {
			s.deps.Log.Warn("maintenance: prune replication changes failed", "err", err)
		} else if n > 0 {
			s.deps.Log.Info("maintenance: replication changes pruned", "count", n)
		}
	}
	if s.deps.Presence != nil {
		s.deps.Presence.Prune()
	}
}

func (s *Server) maintenanceLoop(ctx context.Context) {
	t := time.NewTicker(maintenanceEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.MaintainOnce(ctx)
		}
	}
}
