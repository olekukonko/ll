package lm

import (
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/olekukonko/ll/lx"
)

// shardCount determines the number of shards for the rate limiter.
// Should be a power of 2 for efficient modulo operations.
const shardCount = 32

// RateLimiter is a sharded middleware that limits the rate of log entries per level.
type RateLimiter struct {
	shards [shardCount]*rateLimitShard
}

// rateLimitShard holds rate limiting state for a subset of log levels.
type rateLimitShard struct {
	limits sync.Map // map[lx.LevelType]*rateLimit
}

// rateLimit holds rate limiting state for a specific log level.
type rateLimit struct {
	count    int32      // Current count
	maxCount int32      // Maximum allowed
	interval int64      // Interval in nanoseconds
	last     int64      // Last update timestamp in nanoseconds
	mu       sync.Mutex // Protects count/last during reset
}

// NewRateLimiter creates a new sharded RateLimiter for a specific log level.
func NewRateLimiter(level lx.LevelType, count int, interval time.Duration) *RateLimiter {
	r := &RateLimiter{}
	for i := range r.shards {
		r.shards[i] = &rateLimitShard{}
	}
	r.Set(level, count, interval)
	return r
}

// Set configures a rate limit for a specific log level.
func (rl *RateLimiter) Set(level lx.LevelType, count int, interval time.Duration) *RateLimiter {
	shard := rl.getShard(level)

	limit := &rateLimit{
		count:    0,
		maxCount: int32(count),
		interval: interval.Nanoseconds(),
		last:     time.Now().UnixNano(),
	}

	shard.limits.Store(level, limit)
	return rl
}

// getShard returns the appropriate shard for a log level using FNV hash.
func (rl *RateLimiter) getShard(level lx.LevelType) *rateLimitShard {
	h := fnv.New32a()
	h.Write([]byte{byte(level)})
	idx := h.Sum32() & (shardCount - 1)
	return rl.shards[idx]
}

// Handle processes a log entry and enforces rate limiting.
func (rl *RateLimiter) Handle(e *lx.Entry) error {
	shard := rl.getShard(e.Level)

	// Fast path: check if limit exists without locking
	val, exists := shard.limits.Load(e.Level)
	if !exists {
		return nil
	}

	limit := val.(*rateLimit)
	now := time.Now().UnixNano()

	// Check if interval passed
	if now-limit.last >= limit.interval {
		limit.mu.Lock()
		// Double-check after acquiring lock
		current := time.Now().UnixNano()
		if current-limit.last >= limit.interval {
			limit.last = current
			limit.count = 1
			limit.mu.Unlock()
			return nil
		}
		limit.mu.Unlock()
	}

	// Increment count and check limit
	limit.mu.Lock()
	defer limit.mu.Unlock()

	// Re-check interval in case another goroutine reset it
	current := time.Now().UnixNano()
	if current-limit.last >= limit.interval {
		limit.last = current
		limit.count = 1
		return nil
	}

	limit.count++
	if limit.count > limit.maxCount {
		return fmt.Errorf("rate limit exceeded for level %v", e.Level)
	}
	return nil
}

// Delete removes a rate limit for a specific level.
func (rl *RateLimiter) Delete(level lx.LevelType) {
	shard := rl.getShard(level)
	shard.limits.Delete(level)
}

// Get retrieves the current rate limit settings for a level.
func (rl *RateLimiter) Get(level lx.LevelType) (int, time.Duration, bool) {
	shard := rl.getShard(level)
	val, exists := shard.limits.Load(level)
	if !exists {
		return 0, 0, false
	}
	limit := val.(*rateLimit)
	return int(limit.maxCount), time.Duration(limit.interval), true
}
