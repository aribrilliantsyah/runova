package dbs

import (
	"context"
	"time"
)

const backupHour = 2

// NextRun returns the next 02:00 in now's location: today's when now is
// before it, otherwise tomorrow's.
func NextRun(now time.Time) time.Time {
	y, m, d := now.Date()
	next := time.Date(y, m, d, backupHour, 0, 0, 0, now.Location())
	if !now.Before(next) {
		next = time.Date(y, m, d+1, backupHour, 0, 0, 0, now.Location())
	}
	return next
}

// RunSchedule backs up all databases every night at 02:00 local time until ctx ends.
func (s *Service) RunSchedule(ctx context.Context) {
	for {
		now := s.now()
		timer := time.NewTimer(NextRun(now).Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		s.BackupAll(ctx)
	}
}
