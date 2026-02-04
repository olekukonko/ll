package tests

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/olekukonko/ll"
	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lx"
)

// BenchmarkLogger_Disabled tests the "Fast Path".
// Since we switched to atomic level checks, this should be near-instant (sub-5ns).
func BenchmarkLogger_Disabled(b *testing.B) {
	// Level is Error, but we call Info
	logger := ll.New("bench", ll.WithLevel(lx.LevelError))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("this should be skipped instantly")
	}
}

// BenchmarkLogger_SimpleText tests the TextHandler buffer pooling.
func BenchmarkLogger_SimpleText(b *testing.B) {
	logger := ll.New("bench", ll.WithHandler(lh.NewTextHandler(io.Discard))).Enable()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("static message")
	}
}

// BenchmarkLogger_WithFields tests the field processing allocation.
func BenchmarkLogger_WithFields(b *testing.B) {
	logger := ll.New("bench", ll.WithHandler(lh.NewTextHandler(io.Discard))).Enable()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// key/value pairs
		logger.Fields("id", i, "status", "active", "latency", 45).Info("processing request")
	}
}

// BenchmarkLogger_ContextAndFields tests the optimized slice merging logic.
// Previously this caused double allocations; now it should be efficient.
func BenchmarkLogger_ContextAndFields(b *testing.B) {
	// Setup context once
	logger := ll.New("bench", ll.WithHandler(lh.NewTextHandler(io.Discard))).
		Enable().
		AddContext("app", "benchmark", "env", "prod")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Merge context with new fields
		logger.Fields("req_id", i).Info("context merge test")
	}
}

// BenchmarkLogger_JSON tests the JSONHandler buffer pooling and encoder reuse.
func BenchmarkLogger_JSON(b *testing.B) {
	logger := ll.New("bench", ll.WithHandler(lh.NewJSONHandler(io.Discard))).Enable()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Fields("id", i, "data", "payload").Info("json output")
	}
}

// BenchmarkDedup_Serial tests the basic overhead of the Dedup handler in a single thread.
func BenchmarkDedup_Serial(b *testing.B) {
	// Dedup with 100ms TTL
	handler := lh.NewDedup(lh.NewTextHandler(io.Discard), 100*time.Millisecond)
	logger := ll.New("dedup", ll.WithHandler(handler)).Enable()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Same message, should trigger hash calculation + map lookup + expiry check
		logger.Info("repetitive error message")
	}
}

// BenchmarkDedup_Parallel tests the Sharded Map implementation.
// This is critical for high-throughput apps like 'agbero'.
// High contention on a single Mutex would kill performance here.
func BenchmarkDedup_Parallel(b *testing.B) {
	handler := lh.NewDedup(lh.NewTextHandler(io.Discard), 100*time.Millisecond)
	logger := ll.New("dedup", ll.WithHandler(handler)).Enable()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Simulate high concurrency hitting the dedup logic
			logger.Info("repetitive concurrent message")
		}
	})
}

// BenchmarkDedup_HighCardinality tests behavior when keys are unique (worst case for Dedup).
func BenchmarkDedup_HighCardinality(b *testing.B) {
	handler := lh.NewDedup(lh.NewTextHandler(io.Discard), 100*time.Millisecond)
	logger := ll.New("dedup", ll.WithHandler(handler)).Enable()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Unique messages force map writes
		logger.Info(fmt.Sprintf("unique message %d", i))
	}
}
