package lookup

import "time"

// SetClock makes s read the time from now, for tests that move a fake
// cache's clock.
func SetClock(s *Service, now func() time.Time) { s.now = now }
