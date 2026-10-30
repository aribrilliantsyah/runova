package apps

import "time"

// stopWindow is how long after a stop requested by Runova a die event is
// attributed to that request instead of counting as a crash.
const stopWindow = 10 * time.Second

// noteStop records that Runova is stopping the app's container on purpose.
func (s *Service) noteStop(appID int64) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	if s.stopped == nil {
		s.stopped = map[int64]time.Time{}
	}
	s.stopped[appID] = s.now()
}

// clearCrashes forgets the app's recent crashes, e.g. when the user starts it again.
func (s *Service) clearCrashes(appID int64) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	delete(s.crashes, appID)
}

// forget drops all monitor state of a deleted app.
func (s *Service) forget(appID int64) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	delete(s.crashes, appID)
	delete(s.stopped, appID)
}
