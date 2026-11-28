package notify

import (
	"context"
	"time"
)

// Check is one check of admin_health.
type Check struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

// WatchHealth returns the periodic task that tells the instance admins when a check changes between ok and
// failing. The first run only learns the state, so that a restart does not report what was already wrong.
// The event carries the name of the check and its state, never the error text.
func (s *Service) WatchHealth(checks []Check) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if !s.Enabled() {
			return nil
		}
		for _, c := range checks {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			_, err := c.Run(cctx)
			cancel()
			ok := err == nil
			s.mu.Lock()
			if s.health == nil {
				s.health = map[string]bool{}
			}
			prev, seen := s.health[c.Name]
			s.health[c.Name] = ok
			s.mu.Unlock()
			if !seen || prev == ok {
				continue
			}
			state := "failing"
			if ok {
				state = "ok"
			}
			if err := s.Emit(ctx, s.pool, Event{Kind: HealthChanged, Detail: c.Name + " " + state}); err != nil {
				return err
			}
		}
		return nil
	}
}
