package lm

import (
	"fmt"
	"github.com/olekukonko/ll/lx"
	"sync"
	"time"
)

type RateLimiter struct {
	limits map[lx.LevelType]*rateLimit
	mu     sync.Mutex
}

type rateLimit struct {
	count    int           // Current number of logs in the interval
	maxCount int           // Maximum allowed logs per interval
	interval time.Duration // Time window for rate limiting
	last     time.Time     // Time of the last log
	mu       sync.Mutex    // Protects concurrent access
}

func NewRateLimiter(level lx.LevelType, count int, interval time.Duration) *RateLimiter {
	r := &RateLimiter{
		limits: make(map[lx.LevelType]*rateLimit),
	}
	r.Set(level, count, interval)
	return r
}

func (rl *RateLimiter) Set(level lx.LevelType, count int, interval time.Duration) *RateLimiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.limits[level] = &rateLimit{
		count:    0,
		maxCount: count,
		interval: interval,
		last:     time.Now(),
	}
	return rl
}

func (rl *RateLimiter) Handle(e *lx.Entry) error {
	rl.mu.Lock()
	limit, exists := rl.limits[e.Level]
	rl.mu.Unlock()
	if !exists {
		return nil
	}

	limit.mu.Lock()
	defer limit.mu.Unlock()
	now := time.Now()
	if now.Sub(limit.last) >= limit.interval {
		limit.last = now
		limit.count = 0
	}
	limit.count++ // Increment before checking
	if limit.count > limit.maxCount {
		return fmt.Errorf("rate limit exceeded")
	}
	return nil
}
