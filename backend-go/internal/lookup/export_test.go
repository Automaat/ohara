package lookup

import "time"

// SetClock makes s read the time from now, for tests that move a fake
// cache's clock.
func SetClock(s *Service, now func() time.Time) { s.now = now }

// SetTimeouts sets how long one provider fetch and a whole lookup may take.
func SetTimeouts(s *Service, timeout, budget time.Duration) {
	s.timeout = timeout
	s.budget = budget
}
