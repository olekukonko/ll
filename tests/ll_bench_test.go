package tests

import (
	"io"
	"testing"
	"time"

	"github.com/olekukonko/ll"
	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lx"
)

// discardWriter is a high-performance io.Writer that discards all data
type discardWriter struct{}

func (d discardWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

// Helper to create a test logger
func newTestLogger(level lx.LevelType) *ll.Logger {
	return ll.New("app",
		ll.WithHandler(lh.NewTextHandler(io.Discard)),
		ll.WithLevel(level),
	).Enable()
}

// BenchmarkDisabledLogger tests the cost of logging when logger is disabled
func BenchmarkDisabledLogger(b *testing.B) {
	logger := ll.New("app").Disable()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("test message")
		}
	})
}

// BenchmarkLevelFiltered tests filtering at different levels
func BenchmarkLevelFiltered(b *testing.B) {
	// Level set to ERROR, trying to log INFO (should be filtered)
	logger := newTestLogger(lx.LevelError)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("filtered message")
		}
	})
}

// BenchmarkSimpleInfo tests basic Info logging
func BenchmarkSimpleInfo(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("simple message")
		}
	})
}

// BenchmarkInfoWithFields tests logging with fields
func BenchmarkInfoWithFields(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Fields(
				"user", "alice",
				"action", "login",
				"duration_ms", 42,
			).Info("user action")
		}
	})
}

// BenchmarkInfof tests formatted logging
func BenchmarkInfof(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Infof("user %s performed %s in %dms", "alice", "login", 42)
		}
	})
}

// BenchmarkNamespaceCreation tests namespace hierarchy creation
func BenchmarkNamespaceCreation(b *testing.B) {
	root := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		child := root.Namespace("child").Namespace("grandchild")
		child.Info("test")
	}
}

// BenchmarkConditionalLogging tests If() conditional chains
func BenchmarkConditionalLogging(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	condition := true
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.If(condition).Info("conditional message")
		}
	})
}

// BenchmarkConditionalSkipped tests when condition is false
func BenchmarkConditionalSkipped(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	condition := false
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.If(condition).Info("should not log")
		}
	})
}

// BenchmarkWithContext tests context field overhead
func BenchmarkWithContext(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug).AddContext("service", "api", "version", "1.0.0")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("message with context")
		}
	})
}

// BenchmarkDebugOverhead tests Debug level when set to Info (filtered)
func BenchmarkDebugOverhead(b *testing.B) {
	logger := newTestLogger(lx.LevelInfo)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Debug("debug message filtered out")
		}
	})
}

// BenchmarkPrint tests Print (LevelNone, no formatting)
func BenchmarkPrint(b *testing.B) {
	logger := ll.New("app",
		ll.WithHandler(lh.NewTextHandler(io.Discard)),
	).Enable()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Print("raw message")
		}
	})
}

// BenchmarkSince tests timing overhead
func BenchmarkSince(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sb := logger.Since()
		time.Sleep(time.Microsecond) // Simulate tiny work
		sb.Info("operation completed")
	}
}

// BenchmarkStackCapture tests stack trace capture cost
func BenchmarkStackCapture(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Stack("error with stack")
	}
}

// BenchmarkMeasure tests the Measure helper
func BenchmarkMeasure(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	work := func() {
		time.Sleep(time.Microsecond)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Measure(work)
	}
}

// BenchmarkMultiHandler tests handler chaining overhead
func BenchmarkMultiHandler(b *testing.B) {
	multi := lh.NewMultiHandler(
		lh.NewTextHandler(io.Discard),
		lh.NewJSONHandler(io.Discard),
	)
	logger := ll.New("app",
		ll.WithHandler(multi),
		ll.WithLevel(lx.LevelDebug),
	).Enable()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("multi handler message")
		}
	})
}

// BenchmarkColorizedHandler tests colorized output overhead
func BenchmarkColorizedHandler(b *testing.B) {
	logger := ll.New("app",
		ll.WithHandler(lh.NewColorizedHandler(io.Discard, lh.WithColorNone())),
		ll.WithLevel(lx.LevelDebug),
	).Enable()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("colored message")
		}
	})
}

// BenchmarkJSONHandler tests JSON serialization cost
func BenchmarkJSONHandler(b *testing.B) {
	logger := ll.New("app",
		ll.WithHandler(lh.NewJSONHandler(io.Discard)),
		ll.WithLevel(lx.LevelDebug),
	).Enable()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Fields("key", "value", "num", 42).Info("json message")
		}
	})
}

// BenchmarkNamespaceEnableCheck tests namespace enablement lookup
func BenchmarkNamespaceEnableCheck(b *testing.B) {
	root := newTestLogger(lx.LevelDebug)
	child := root.Namespace("child").Namespace("grandchild")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = child.NamespaceEnabled("test")
		}
	})
}

// BenchmarkClone tests logger cloning cost
func BenchmarkClone(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug).AddContext("key", "value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = logger.Clone()
	}
}

// BenchmarkMiddleware tests middleware chain overhead
func BenchmarkMiddleware(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	// Add a simple passthrough middleware
	logger.Use(ll.Middle(func(e *lx.Entry) error {
		return nil
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("message through middleware")
		}
	})
}

// BenchmarkFieldBuilderChain tests field builder chaining
func BenchmarkFieldBuilderChain(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Chain Fields and Merge (removed Field call that was causing error)
			logger.Fields("a", 1).Merge("b", 2).Info("chained")
		}
	})
}

// BenchmarkGlobalLogger tests package-level functions
func BenchmarkGlobalLogger(b *testing.B) {
	ll.Handler(lh.NewTextHandler(io.Discard))
	ll.Level(lx.LevelDebug)
	ll.Enable()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ll.Info("global logger message")
		}
	})
}

// BenchmarkSuspendCheck tests suspended logger fast-path
func BenchmarkSuspendCheck(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug).Suspend()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("should be suspended")
		}
	})
}

// BenchmarkErrLogging tests error logging - Err() doesn't return value, so test differently
func BenchmarkErrLogging(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Err() logs immediately and doesn't return anything for chaining
			// Just test the overhead of calling it with nil
			logger.Err(nil)
		}
	})
}

// BenchmarkDbg tests debug output (source capture)
func BenchmarkDbg(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Dbg("debug value", i)
	}
}

// BenchmarkMark tests mark output
func BenchmarkMark(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Mark()
	}
}

// BenchmarkLine tests vertical spacing
func BenchmarkLine(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Line(1)
	}
}

// BenchmarkDump tests hex dump output
func BenchmarkDump(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)
	data := []byte{0x48, 0x65, 0x6c, 0x6c, 0x6f} // "Hello"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Dump(data)
	}
}

// BenchmarkOutput tests JSON output method
func BenchmarkOutput(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)
	data := map[string]interface{}{"key": "value", "num": 42}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Output(data)
	}
}

// BenchmarkInspect tests inspect output
func BenchmarkInspect(b *testing.B) {
	logger := newTestLogger(lx.LevelDebug)
	type TestStruct struct {
		Name  string
		Value int
	}
	obj := TestStruct{Name: "test", Value: 42}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Inspect(obj)
	}
}

// BenchmarkFatalExits tests fatal configuration
func BenchmarkFatalExits(b *testing.B) {
	logger := ll.New("app",
		ll.WithHandler(lh.NewTextHandler(io.Discard)),
		ll.WithLevel(lx.LevelDebug),
		ll.WithFatalExits(false),
	).Enable()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Fatal("fatal error")
	}
}

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
		logger.Infof("unique message %d", i)
	}
}
