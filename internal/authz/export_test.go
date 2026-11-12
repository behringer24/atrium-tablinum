package authz

import "time"

// SetClock replaces the clock the cache expiry uses, so that a test does not depend on real time.
func (c *Checker) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}
