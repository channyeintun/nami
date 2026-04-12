package agent

import (
	"strings"
	"sync"
)

// StopController coordinates cooperative stop requests across query callers.
// The first request is consumed by the query loop; a second request while one is
// still pending can be treated by the caller as a hard-cancel fallback.
type StopController struct {
	mu      sync.Mutex
	pending bool
	reason  string
}

func NewStopController() *StopController {
	return &StopController{}
}

func (c *StopController) Request(reason string) (alreadyPending bool) {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	alreadyPending = c.pending
	c.pending = true
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled"
	}
	c.reason = reason
	return alreadyPending
}

func (c *StopController) Consume() (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pending {
		return "", false
	}
	reason := c.reason
	c.pending = false
	c.reason = ""
	if strings.TrimSpace(reason) == "" {
		reason = "cancelled"
	}
	return reason, true
}

func (c *StopController) Pending() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending
}
