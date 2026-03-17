package lh

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/olekukonko/ll/lx"
)

// TestDedup_ShardDistribution tests that entries are distributed across shards
func TestDedup_ShardDistribution(t *testing.T) {
	handler := &countingHandler{}
	d := NewDedup(handler, time.Second)

	// Send entries with different keys
	for i := 0; i < 1000; i++ {
		entry := &lx.Entry{
			Level:   lx.LevelInfo,
			Message: string(rune('a' + (i % 26))),
		}
		d.Handle(entry)
	}

	// Check that shards were used
	usedShards := 0
	for i := 0; i < len(d.shards); i++ {
		d.shards[i].mu.Lock()
		if len(d.shards[i].seen) > 0 {
			usedShards++
		}
		d.shards[i].mu.Unlock()
	}

	if usedShards < 2 {
		t.Fatalf("expected distribution across multiple shards, got %d", usedShards)
	}

	d.Close()
}

// TestDedup_NilKeyFn tests panic protection
func TestDedup_NilKeyFn(t *testing.T) {
	handler := &countingHandler{}
	d := NewDedup(handler, time.Second)
	d.keyFn = nil // Simulate bug

	// Should not panic, should pass through
	err := d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})
	if err != nil {
		t.Fatalf("expected nil error with nil keyFn, got %v", err)
	}

	d.Close()
}

// TestDedup_TTLExpiration tests that entries expire correctly
func TestDedup_TTLExpiration(t *testing.T) {
	handler := &countingHandler{}
	ttl := 50 * time.Millisecond
	d := NewDedup(handler, ttl, WithDedupCleanupInterval(10*time.Millisecond))

	// First entry
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "duplicate"})

	// Same entry immediately - should be deduped
	handler.count.Store(0)
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "duplicate"})
	if handler.count.Load() != 0 {
		t.Fatal("entry should have been deduped")
	}

	// Wait for TTL
	time.Sleep(ttl + 20*time.Millisecond)

	// Same entry after TTL - should be allowed
	handler.count.Store(0)
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "duplicate"})
	if handler.count.Load() != 1 {
		t.Fatal("entry should have been allowed after TTL")
	}

	d.Close()
}

type countingHandler struct {
	count atomic.Int32
}

func (c *countingHandler) Handle(e *lx.Entry) error {
	c.count.Add(1)
	return nil
}

// BenchmarkDedup_ShardContention tests concurrent performance
func BenchmarkDedup_ShardContention(b *testing.B) {
	handler := &countingHandler{}
	d := NewDedup(handler, time.Second)

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			entry := &lx.Entry{
				Level:   lx.LevelInfo,
				Message: string(rune('a' + (i % 26))),
			}
			d.Handle(entry)
			i++
		}
	})

	d.Close()
}

func TestDedup_SuppressesWithFields(t *testing.T) {
	handler := &countingHandler{}
	d := NewDedup(handler, time.Second)

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "login failed",
		Fields: lx.Fields{
			{Key: "user", Value: "alice"},
			{Key: "ip", Value: "1.2.3.4"},
		},
	}

	d.Handle(entry)
	handler.count.Store(0)

	// Identical entry — must be suppressed
	d.Handle(entry)
	if handler.count.Load() != 0 {
		t.Fatal("entry with fields should have been deduped")
	}

	d.Close()
}
