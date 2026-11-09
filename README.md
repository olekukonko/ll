# ll - A Modern Structured Logging Library for Go

`ll` is a high-performance, production-ready logging library for Go, designed to provide **hierarchical namespaces**, **structured logging**, **middleware pipelines**, **conditional logging**, and support for multiple output formats, including text, JSON, colorized logs, syslog, VictoriaLogs, and compatibility with Go's `slog`. It's ideal for applications requiring fine-grained log control, extensibility, and scalability.

## Key Features

- **Logging Enabled by Default** - Zero configuration to start logging
- **Hierarchical Namespaces** - Organize logs with fine-grained control over subsystems (e.g., "app/db"), including per-namespace level thresholds
- **Structured Logging** - Add key-value metadata for machine-readable logs
- **Middleware Pipeline** - Customize log processing with rate limiting, sampling, and deduplication
- **Conditional & Error-Based Logging** - Optimize performance with fluent `If`, `IfErr`, `IfAny`, `IfOne` chains
- **Multiple Output Formats** - Text, JSON, colorized ANSI, syslog, VictoriaLogs, and `slog` integration
- **Advanced Debugging Utilities** - Source-aware `Dbg()`, hex/ASCII `Dump()`, private field `Inspect()`, and stack traces
- **Observability Hooks** - Dynamic-level `Log`/`Logf`, opt-in caller reporting, and `context.Context` field extraction
- **Production Ready** - Buffered batching, log rotation, duplicate suppression, rate limiting, and `Flush`/`Close` plumbing
- **Thread-Safe** - Built for high-concurrency with atomic operations, immutable config snapshots, and lock-free fast paths
- **Performance Optimized** - Zero allocations for skipped logs, no message copying, `sync.Pool` buffers

## Installation

Install `ll` using Go modules:

```bash
go get github.com/olekukonko/ll
```

Requires Go 1.21 or later.

## Quick Start

```go
package main

import "github.com/olekukonko/ll"

func main() {
    // Logger is ENABLED by default - no .Enable() needed!
    logger := ll.New("app")
    
    // Basic logging - works immediately
    logger.Info("Server starting")     // Output: [app] INFO: Server starting
    logger.Warn("Memory high")         // Output: [app] WARN: Memory high
    logger.Error("Connection failed")  // Output: [app] ERROR: Connection failed
    
    // Structured fields
    logger.Fields("user", "alice", "status", 200).Info("Login successful")
    // Output: [app] INFO: Login successful [user=alice status=200]
}
```

**That's it. No `.Enable()`, no handlers to configure—it just works.**

## Core Concepts

### 1. Enabled by Default, Configurable When Needed

Unlike many logging libraries that require explicit enabling, `ll` **logs immediately**. This eliminates boilerplate and reduces the chance of missing logs in production.

```go
// This works out of the box:
ll.Info("Service started")  // Output: [] INFO: Service started

// But you still have full control:
ll.Disable()  // Global shutdown
ll.Enable()   // Reactivate
```

### 2. Log Levels

`ll` keeps its historic numeric level ordering by default: an entry is emitted when
its level value does **not exceed** the configured one. Since `LevelDebug` has the
highest numeric value, `Level(LevelInfo)` suppresses Warn/Error/Fatal and
`Level(LevelError)` still lets Info through — surprising, but it is what previous
releases did, so it is preserved for compatibility.

If you want a plain "minimum severity" threshold, opt in:

```go
logger := ll.New("app", ll.WithOrderedLevels(true)).Level(lx.LevelError)
logger.Info("dropped")   // below threshold
logger.Error("kept")     // emitted
logger.Fatal("kept")     // Fatal is never filtered by a threshold

// or at runtime
logger.OrderedLevels(true)
```

Severity ranking is available as `lx.LevelType.Severity()`
(Debug=0, Info/None=1, Warn=2, Error=3, Fatal=4).

**Fatal does not exit by default.** `Fatal`/`Fatalf` log an entry; terminating the
process is opt-in:

```go
logger := ll.New("app", ll.WithFatalExits(true), ll.WithFatalStack(true))
```

### 3. Hierarchical Namespaces

Organize logs hierarchically with precise control over subsystems:

```go
// Create a logger hierarchy
root := ll.New("app")
db := root.Namespace("database")
cache := root.Namespace("cache").Style(lx.NestedPath)

// Control logging per namespace
root.NamespaceEnable("app/database")    // Enable database logs
root.NamespaceDisable("app/cache")      // Disable cache logs

db.Info("Connected")     // Output: [app/database] INFO: Connected
cache.Info("Hit")        // No output (disabled)
```

### 4. Structured Logging with Ordered Fields

Fields maintain insertion order and support fluent chaining:

```go
// Fluent key-value pairs
logger.
    Fields("request_id", "req-123").
    Fields("user", "alice").
    Fields("duration_ms", 42).
    Info("Request processed")

// Map-based fields
logger.Field(map[string]interface{}{
    "method": "POST",
    "path": "/api/users",
}).Debug("API call")

// Persistent context (included in ALL subsequent logs)
logger.AddContext("environment", "production", "version", "1.2.3")
logger.Info("Deployed")  // Output: ... [environment=production version=1.2.3]
```

### 5. Conditional & Error-Based Logging

Optimize performance with fluent conditional chains that **completely skip processing** when conditions are false:

```go
// Boolean conditions
logger.If(debugMode).Debug("Detailed diagnostics")  // No overhead when false
logger.If(featureEnabled).Info("Feature used")

// Error conditions
err := db.Query()
logger.IfErr(err).Error("Query failed")              // Logs only if err != nil

// Multiple conditions - ANY true
logger.IfErrAny(err1, err2, err3).Fatal("System failure")

// Multiple conditions - ALL true
logger.IfErrOne(validateErr, authErr).Error("Both checks failed")

// Chain conditions
logger.
    If(debugMode).
    IfErr(queryErr).
    Fields("query", sql).
    Debug("Query debug")
```

**Performance**: When conditions are false, the logger returns immediately with zero allocations.

### 6. Powerful Debugging Toolkit

`ll` includes advanced debugging utilities not found in standard logging libraries:

#### Dbg() - Source-Aware Variable Inspection
Captures both variable name AND value from your source code:

```go
x := 42
user := &User{Name: "Alice"}
ll.Dbg(x, user)  
// Output: [file.go:123] x = 42, *user = &{Name:Alice}
```

#### Dump() - Hex/ASCII Binary Inspection
Perfect for protocol debugging and binary data:

```go
ll.Handler(lh.NewColorizedHandler(os.Stdout))
ll.Dump([]byte("hello\nworld"))
// Output: Colorized hex/ASCII dump with offset markers
```

#### Inspect() - Private Field Reflection
Reveals unexported fields, embedded structs, and pointer internals:

```go
type secret struct {
    password string  // unexported!
}

s := secret{password: "hunter2"}
ll.Inspect(s)
// Output: [file.go:123] INSPECT: {
//   "(password)": "hunter2"  // Note the parentheses
// }
```

#### Stack() - Configurable Stack Traces
```go
ll.StackSize(8192)  // Larger buffer for deep stacks
ll.Stack("Critical failure")
// Output: ERROR: Critical failure [stack=goroutine 1 [running]...]
```

#### Mark() - Execution Flow Tracing
```go
func process() {
    ll.Mark()           // *MARK*: [file.go:123]
    ll.Mark("phase1")   // *phase1*: [file.go:124]
    // ... work ...
}
```

### 7. Production-Ready Handlers

```go
import (
    "github.com/olekukonko/ll"
    "github.com/olekukonko/ll/lh"
    "github.com/olekukonko/ll/l3rd/syslog"
    "github.com/olekukonko/ll/l3rd/victoria"
)

// JSON for structured logging
logger.Handler(lh.NewJSONHandler(os.Stdout))

// Colorized for development
logger.Handler(lh.NewColorizedHandler(os.Stdout, 
    lh.WithColorTheme("dark"),
    lh.WithColorIntensity(lh.IntensityVibrant),
))

// Buffered for high throughput (100 entries or 10 seconds)
buffered := lh.NewBuffered(
    lh.NewJSONHandler(os.Stdout),
    lh.WithBatchSize(100),
    lh.WithFlushInterval(10 * time.Second),
)
logger.Handler(buffered)
defer buffered.Close()  // Ensures flush on exit

// Syslog integration
syslogHandler, _ := syslog.New(
    syslog.WithTag("myapp"),
    syslog.WithFacility(stdsyslog.LOG_LOCAL0), // stdsyslog "log/syslog"
)
logger.Handler(syslogHandler)

// VictoriaLogs (cloud-native)
victoriaHandler, _ := victoria.New(
    victoria.WithURL("http://victoria-logs:9428"),
    victoria.WithAppName("payment-service"),
    victoria.WithEnvironment("production"),
    victoria.WithBatching(200, 5*time.Second), // opt-in async NDJSON batching
)
logger.Handler(victoriaHandler)
```

### 8. Middleware Pipeline

Transform, filter, or reject logs with a middleware pipeline:

```go
// Rate limiting - 10 logs per second maximum
rateLimiter := lm.NewRateLimiter(lx.LevelInfo, 10, time.Second)
logger.Use(rateLimiter)

// Sampling - 10% of debug logs
sampler := lm.NewSampling(lx.LevelDebug, 0.1)
logger.Use(sampler)

// Deduplication - suppress identical logs for 2 seconds
deduper := lh.NewDedup(logger.GetHandler(), 2*time.Second)
logger.Handler(deduper)

// Same, but report how many duplicates each burst swallowed. The count is only
// final when the window closes, so it is one extra line per burst, not per
// duplicate, and costs one allocation per window.
reporting := lh.NewDedup(logger.GetHandler(), 5*time.Second,
    lh.WithDedupSummary(""),                                  // field name "dup"
    lh.WithDedupIgnore("duration", "interval", "delay"),
)
logger.Handler(reporting)
// [app] INFO: upstream timeout [duration=12ms dup=42 dup_w=5s]
//
// Out of band, with no extra log lines: reporting.Suppressed(),
// reporting.Stats(), reporting.Report().

// Custom middleware
logger.Use(ll.Middle(func(e *lx.Entry) error {
    if strings.Contains(e.Message, "password") {
        return fmt.Errorf("sensitive information redacted")
    }
    return nil
}))
```

### 9. Global Convenience API

Use package-level functions for quick logging without creating loggers:

```go
import "github.com/olekukonko/ll"

func main() {
    ll.Info("Server starting")           // Global logger
    ll.Fields("port", 8080).Info("Listening")
    
    // Conditional logging at package level
    ll.If(simulation).Debug("Test mode")
    ll.IfErr(err).Error("Startup failed")
    
    // Debug utilities
    ll.Dbg(config)
    ll.Dump(requestBody)
    ll.Inspect(complexStruct)
}
```
### 10. Dynamic Levels, Callers, Context Propagation & Lifecycle

```go
// Dynamic level (for adapters or runtime-selected severity)
logger.Log(lx.LevelWarn, "disk", "almost full")
logger.Logf(lx.LevelError, "%d requests failed", n)
logger.Fields("user", "alice").Log(lx.LevelInfo, "login")

// Caller reporting (opt-in: it walks the stack)
logger := ll.New("app", ll.WithCaller(true))
logger.Info("started") // [main.go:12] [app] INFO: started
// JSON handler: {"ts":..,"lvl":"INFO","caller":"main.go:12",..}

// context.Context propagation (trace/request ids) - any field extractor works
logger := ll.New("app", ll.WithContextExtractor(func(ctx context.Context) []lx.Field {
    return []lx.Field{{Key: "trace_id", Value: traceIDFrom(ctx)}}
}))
logger.Ctx(r.Context()).Info("handled") // includes trace_id

// Per-namespace threshold (always severity based)
logger.NamespaceLevel("app/db", lx.LevelWarn) // quiet the db subtree

// Lifecycle: reach buffered/rotating handlers without keeping your own reference
defer logger.Close() // flushes + closes (walks MultiHandler too)
logger.Flush()       // flush only
```

**Handlers and entry ownership**: the logger recycles `*lx.Entry` (and its field
slice) as soon as `Handle` returns. A handler that needs to keep an entry must
copy it with `lh.CloneEntry` (as `lh.Buffered` and `lh.MemoryHandler` do).

### 11. VictoriaLogs Delivery

By default the VictoriaLogs handler sends each entry in its own HTTP request from
the calling goroutine. For high-volume services enable batching to collapse N
entries into one NDJSON request and move retries off the request path:

```go
vl, _ := victoria.New(
    victoria.WithURL("http://victoria-logs:9428"),
    victoria.WithBatching(500, 2*time.Second), // opt-in
    victoria.WithRetry(3),
    victoria.WithErrorHandler(func(err error) { /* async failures */ }),
)
defer vl.Close() // flushes the pending batch

logger := ll.New("payment").Handler(vl)
```

If you would rather keep the handler untouched, wrap it with the generic buffer:

```go
logger := ll.New("payment").Handler(
    lh.NewBuffered(vl, lh.WithBatchSize(500), lh.WithFlushInterval(2*time.Second)),
)
```



## Real-World Examples

### Web Server with Structured Logging

```go
package main

import (
    "github.com/olekukonko/ll"
    "github.com/olekukonko/ll/lh"
    "net/http"
    "time"
)

func main() {
    // Root logger - enabled by default
    log := ll.New("server")
    
    // JSON output for production
    log.Handler(lh.NewJSONHandler(os.Stdout))
    
    // Request logger with context
    http.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
        reqLog := log.Namespace("http").Fields(
            "method", r.Method,
            "path", r.URL.Path,
            "request_id", r.Header.Get("X-Request-ID"),
        )
        
        start := time.Now()
        reqLog.Info("request started")
        
        // ... handle request ...
        
        reqLog.Fields(
            "status", 200,
            "duration_ms", time.Since(start).Milliseconds(),
        ).Info("request completed")
    })
    
    log.Info("Server listening on :8080")
    http.ListenAndServe(":8080", nil)
}
```

### Microservice with VictoriaLogs

```go
package main

import (
    "github.com/olekukonko/ll"
    "github.com/olekukonko/ll/l3rd/victoria"
)

func main() {
    // Production setup
    vlHandler, _ := victoria.New(
        victoria.WithURL("http://logs.internal:9428"),
        victoria.WithAppName("payment-api"),
        victoria.WithEnvironment("production"),
        victoria.WithVersion("1.2.3"),
        victoria.WithBatching(500, 2*time.Second), // one HTTP request per 500 logs
        victoria.WithRetry(3),
    )
    defer vlHandler.Close()
    
    logger := ll.New("payment").
        Handler(vlHandler).
        AddContext("region", "us-east-1")
    
    logger.Info("Payment service initialized")
    
    // Conditional error handling
    if err := processPayment(); err != nil {
        logger.IfErr(err).
            Fields("payment_id", paymentID).
            Error("Payment processing failed")
    }
}
```

## Performance

Benchmarks on Apple M3 Pro (Go 1.27, 12 CPUs, parallel, writing to `io.Discard`).
`go test ./tests -bench . -benchmem`:

| Operation | Time/op | Allocations |
|-----------|---------|-------------|
| Skipped by level (ordered levels, opt-in) | **~2 ns** | **0** |
| Disabled logger | ~7 ns | **0** |
| Disabled logger + `Fields(...)` | ~10 ns | **0** |
| Simple text log | ~118 ns | **0** |
| With 2 context fields | ~118 ns | **0** |
| With 3 explicit fields | ~122 ns | **0** |
| `If(...)` conditional log | ~116 ns | **0** |
| Colorized output | ~120 ns | **0** |
| JSON output | ~177 ns | 2 (pooled objects) |
| Multi-handler (2 sinks) | ~151 ns | 1 |

**Key optimizations**:
- Zero allocations for skipped (disabled, filtered, false-conditional) logs
- The message is no longer copied when no `Prefix`/`Indent` is configured
- Configuration is published as an immutable snapshot: the hot path is lock-free
- Namespace lookups are skipped entirely until a namespace rule/level exists
- `sync.Pool` for buffers/entries, allocation-free namespace prefix walks

Note: on darwin `time.Now()` alone costs ~30-45 ns, so it dominates the enabled
path; the relative gains are larger on platforms with a cheaper clock.

## Why Choose `ll`?

| Feature | `ll` | `slog` | `zap` | `logrus` |
|---------|------|--------|-------|----------|
| **Enabled by default** | ✅ | ❌ | ❌ | ❌ |
| Hierarchical namespaces | ✅ | ❌ | ❌ | ❌ |
| Conditional logging | ✅ | ❌ | ❌ | ❌ |
| Error-based conditions | ✅ | ❌ | ❌ | ❌ |
| Source-aware Dbg() | ✅ | ❌ | ❌ | ❌ |
| Private field inspection | ✅ | ❌ | ❌ | ❌ |
| Hex/ASCII Dump() | ✅ | ❌ | ❌ | ❌ |
| Middleware pipeline | ✅ | ❌ | ✅ (limited) | ❌ |
| Deduplication | ✅ | ❌ | ❌ | ❌ |
| Rate limiting | ✅ | ❌ | ❌ | ❌ |
| VictoriaLogs support | ✅ | ❌ | ❌ | ❌ |
| Syslog support | ✅ | ❌ | ❌ | ✅ |
| Dynamic level (`Log`) | ✅ | ✅ | ❌ | ❌ |
| Caller reporting | ✅ | ✅ | ✅ | ✅ |
| `context.Context` fields | ✅ | ✅ | ❌ | ❌ |
| Per-namespace levels | ✅ | ❌ | ❌ | ❌ |
| Zero-allocs skipped logs | ✅ | ✅ | ✅ | ❌ |
| Thread-safe | ✅ | ✅ | ✅ | ✅ |

## Documentation

- [GoDoc](https://pkg.go.dev/github.com/olekukonko/ll) - Full API documentation
- [Examples](_example/) - Runable example code
- [Benchmarks](tests/bench_test.go) - Performance benchmarks

## Contributing

Contributions are welcome! Please open an issue or a pull request.

## License

MIT License - see [LICENSE](LICENSE) for details.