package ll

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/olekukonko/cat"
	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lx"
)

// stackBufPool pools buffers for stack trace capture to reduce allocations.
var (
	stackBufPool = sync.Pool{
		New: func() any { return make([]byte, 4096) },
	}

	entryPool = sync.Pool{
		New: func() any { return &lx.Entry{} },
	}

	fieldsSlicePool = sync.Pool{
		New: func() any {
			s := make(lx.Fields, 0, 8)
			return &s
		},
	}
)

// loggerConfig is an immutable snapshot of everything the emit path needs.
// Setters mutate Logger fields under l.mu and then publish a fresh snapshot, so
// the hot path can read the configuration with a single atomic load instead of
// taking the read lock on every log call.
type loggerConfig struct {
	handler    lx.Handler
	prefix     string
	indent     int
	context    lx.Fields
	style      lx.StyleType
	path       string
	separator  string
	middleware []Middleware
	stackSize  int
	caller     bool
}

// Logger manages logging configuration and behavior, encapsulating state such as enablement,
// log level, namespaces, context fields, output style, handler, middleware, and formatting.
// It is thread-safe, using a read-write mutex to protect concurrent access to its fields.
type Logger struct {
	mu              sync.RWMutex  // Guards concurrent access to fields
	enabled         atomic.Int32  // Determines if logging is enabled
	suspend         atomic.Bool   // uses suspend path for most actions eg. skipping namespace checks
	level           lx.LevelType  // Minimum log level (e.g., Debug, Info, Warn, Error)
	atomicLevel     int32         // Shadow copy of level for lock-free checks
	namespaces      *lx.Namespace // Manages namespace enable/disable states
	currentPath     string        // Current namespace path (e.g., "parent/child")
	context         lx.Fields     // Contextual fields included in all logs
	style           lx.StyleType  // Namespace formatting style (FlatPath or NestedPath)
	handler         lx.Handler    // Output handler for logs (e.g., text, JSON)
	middleware      []Middleware  // Middleware functions to process log entries
	prefix          string        // Prefix prepended to log messages
	indent          int           // Number of double spaces for message indentation
	stackBufferSize int           // Buffer size for capturing stack traces
	separator       string        // Separator for namespace paths (e.g., "/")
	entries         atomic.Int64  // Tracks total log entries sent to handler
	fatalExits      bool
	fatalStack      bool
	caller          bool                                 // capture caller location on every entry
	ordered         atomic.Bool                          // severity-ordered level filtering (opt-in)
	ctxFields       func(ctx context.Context) []lx.Field // derives fields from a context.Context
	labels          atomic.Pointer[[]string]
	cfg             atomic.Pointer[loggerConfig] // published read-only view for the hot path
}

// publish snapshots the mutable configuration into an immutable view that the
// logging hot path reads without locking. Callers must hold l.mu for writing (or
// own the logger exclusively, e.g. during construction).
func (l *Logger) publish() {
	l.cfg.Store(&loggerConfig{
		handler:    l.handler,
		prefix:     l.prefix,
		indent:     l.indent,
		context:    l.context,
		style:      l.style,
		path:       l.currentPath,
		separator:  l.separator,
		middleware: l.middleware,
		stackSize:  l.stackBufferSize,
		caller:     l.caller,
	})
}

// config returns the current configuration snapshot, creating one on demand for
// hand-built Logger values (e.g. &Logger{} in tests).
func (l *Logger) config() *loggerConfig {
	if c := l.cfg.Load(); c != nil {
		return c
	}
	l.mu.RLock()
	l.publish()
	l.mu.RUnlock()
	if c := l.cfg.Load(); c != nil {
		return c
	}
	return &loggerConfig{}
}

// New creates a new Logger with the given namespace and optional configurations.
// It initializes with defaults: disabled, Debug level, flat namespace style, text handler
// to os.Stdout, and an empty middleware chain. Options (e.g., WithHandler, WithLevel) can
// override defaults. The logger is thread-safe via mutex-protected methods.
// Example:
//
//	logger := New("app", WithHandler(lh.NewTextHandler(os.Stdout))).Enable()
//	logger.Info("Starting application") // Output: [app] INFO: Starting application
func New(namespace string, opts ...Option) *Logger {
	logger := &Logger{
		//enabled:         0,                            // Defaults to disabled (false)
		level:           lx.LevelDebug,                // Default minimum log level
		atomicLevel:     int32(lx.LevelDebug),         // Initialize atomic level
		namespaces:      defaultStore,                 // Shared namespace store
		currentPath:     namespace,                    // Initial namespace path
		context:         make(lx.Fields, 0, 10),       // Empty context for fields
		style:           lx.FlatPath,                  // Default namespace style ([parent/child])
		handler:         lh.NewTextHandler(os.Stdout), // Default text output to stdout
		middleware:      make([]Middleware, 0),        // Empty middleware chain
		stackBufferSize: 4096,                         // Default stack trace buffer size
		separator:       lx.Slash,                     // Default namespace separator ("/")
	}
	logger.enabled.Store(lx.Active)
	// Apply provided configuration options
	for _, opt := range opts {
		opt(logger)
	}
	logger.publish()
	return logger
}

// Apply applies one or more functional options to the default/global logger.
// Useful for late configuration (e.g., after migration, attach VictoriaLogs handler,
// set level, add middleware, etc.) without changing existing New() calls.
//
// Each option is responsible for its own synchronization, so Apply deliberately
// does not hold the logger mutex while running the options (options such as
// WithLevel call Logger.Level, which takes the write lock itself).
//
// Example:
//
//	// In main() or init(), after setting up handler
//	ll.Apply(
//	    ll.Handler(vlBatched),
//	    ll.Level(ll.LevelInfo),
//	    ll.Use(rateLimiterMiddleware),
//	)
//
// Returns the default logger for chaining (if needed).
func (l *Logger) Apply(opts ...Option) *Logger {
	for _, opt := range opts {
		if opt != nil {
			opt(l)
		}
	}
	l.mu.Lock()
	l.publish()
	l.mu.Unlock()
	return l
}

// AddContext adds one or more key-value pairs to the logger's persistent context.
// These fields will be included in **every** subsequent log message from this logger
// (and its child namespace loggers).
//
// It supports variadic key-value pairs (string key, any value).
// Non-string keys or uneven number of arguments will be safely ignored/logged.
//
// Returns the logger for chaining.
//
// Examples:
//
//	logger.AddContext("user", "alice", "env", "prod")
//	logger.AddContext("request_id", reqID, "trace_id", traceID)
//	logger.AddContext("service", "payment")                    // single pair
func (l *Logger) AddContext(pairs ...any) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.context == nil {
		l.context = make(lx.Fields, 0, len(pairs)/2)
	}
	for i := 0; i < len(pairs)-1; i += 2 {
		if key, ok := pairs[i].(string); ok {
			l.context = append(l.context, lx.Field{Key: key, Value: pairs[i+1]})
		}
	}
	l.publish()
	return l
}

// Benchmark logs the duration since a start time at Info level, including "start",
// "end", and "duration" fields. It is thread-safe via Fields and log methods.
// Example:
//
//	logger := New("app").Enable()
//	start := time.Now()
//	logger.Benchmark(start) // Output: [app] INFO: benchmark [start=... end=... duration=...]
func (l *Logger) Benchmark(start time.Time) time.Duration {
	duration := time.Since(start)
	l.Fields(
		"duration_ms", duration.Milliseconds(),
		"duration", duration.String(),
	).Infof("benchmark completed")
	return duration
}

// CanLog checks if a log at the given level would be emitted, considering enablement,
// log level, namespaces, sampling, and rate limits. It is thread-safe via shouldLog.
// Example:
//
//	logger := New("app").Enable().Level(lx.LevelWarn)
//	canLog := logger.CanLog(lx.LevelInfo) // false
func (l *Logger) CanLog(level lx.LevelType) bool {
	return l.shouldLog(level)
}

// Clear removes all middleware functions, resetting the middleware chain to empty.
// It is thread-safe using a write lock and returns the logger for chaining.
// Example:
//
//	logger := New("app").Enable().Use(someMiddleware)
//	logger.Clear()
//	logger.Info("Inactive middleware") // Output: [app] INFO: Inactive middleware
func (l *Logger) Clear() *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.middleware = nil
	l.publish()
	return l
}

// Clone creates a new logger with the same configuration and namespace as the parent.
// The clone shares the parent's context fields (copy-on-write: adding fields to either
// logger never affects the other) and its own atomic state for the identity/enabled
// flags. It is thread-safe using a read lock.
// Example:
//
//	logger := New("app").Enable().Context(map[string]interface{}{"k": "v"})
//	clone := logger.Clone()
//	clone.Info("Cloned") // Output: [app] INFO: Cloned [k=v]
func (l *Logger) Clone() *Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()
	child := &Logger{
		level:           l.level,                                   // Copy log level
		atomicLevel:     l.atomicLevel,                             // Copy atomic level
		namespaces:      l.namespaces,                              // Share namespace store
		currentPath:     l.currentPath,                             // Copy namespace path
		context:         l.context[:len(l.context):len(l.context)], // Share read-only, copy-on-append
		style:           l.style,                                   // Copy namespace style
		handler:         l.handler,                                 // Copy output handler
		middleware:      l.middleware,                              // Copy middleware chain
		prefix:          l.prefix,                                  // Copy message prefix
		indent:          l.indent,                                  // Copy indentation level
		stackBufferSize: l.stackBufferSize,                         // Copy stack trace buffer size
		separator:       l.separator,                               // Default separator ("/")
		fatalExits:      l.fatalExits,                              // Preserve Fatal() exit behaviour
		fatalStack:      l.fatalStack,                              // Preserve Fatal() stack behaviour
	}
	// Atomic fields must be copied by value assignment, never by struct copy (go vet).
	child.enabled.Store(l.enabled.Load())
	child.suspend.Store(l.suspend.Load())
	child.labels.Store(l.labels.Load())
	child.publish()
	return child
}

// Context creates a new logger with additional contextual fields, preserving existing
// fields and adding new ones. It returns a new logger to avoid mutating the parent and
// is thread-safe using a write lock.
// Example:
//
//	logger := New("app").Enable()
//	logger = logger.Context(map[string]interface{}{"user": "alice"})
//	logger.Info("Action") // Output: [app] INFO: Action [user=alice]
func (l *Logger) Context(fields map[string]interface{}) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Create a new logger with inherited configuration
	newLogger := &Logger{
		level:           l.level,
		atomicLevel:     l.atomicLevel,
		namespaces:      l.namespaces,
		currentPath:     l.currentPath,
		context:         make(lx.Fields, 0, len(l.context)+len(fields)),
		style:           l.style,
		handler:         l.handler,
		middleware:      l.middleware,
		prefix:          l.prefix,
		indent:          l.indent,
		stackBufferSize: l.stackBufferSize,
		separator:       l.separator,
		fatalExits:      l.fatalExits,
		fatalStack:      l.fatalStack,
	}
	// Atomic fields must be copied by value assignment, never by struct copy (go vet).
	newLogger.enabled.Store(l.enabled.Load())
	newLogger.suspend.Store(l.suspend.Load())
	newLogger.labels.Store(l.labels.Load())
	// Copy parent's context fields (in order)
	newLogger.context = append(newLogger.context, l.context...)
	// Add new fields from map
	for k, v := range fields {
		newLogger.context = append(newLogger.context, lx.Field{Key: k, Value: v})
	}
	newLogger.publish()
	return newLogger
}

// Debug logs a message at Debug level, formatting it and delegating to the internal
// log method. It is thread-safe.
// Example:
//
//	logger := New("app").Enable().Level(lx.LevelDebug)
//	logger.Debug("Debugging") // Output: [app] DEBUG: Debugging
func (l *Logger) Debug(args ...any) {
	if l.suspend.Load() {
		return
	}
	// Skip logging if Debug level is not enabled
	if !l.shouldLog(lx.LevelDebug) {
		return
	}
	l.logChecked(lx.LevelDebug, lx.ClassText, cat.Space(args...), nil, false)
}

// Debugf logs a formatted message at Debug level, delegating to Debug. It is thread-safe.
// Example:
//
//	logger := New("app").Enable().Level(lx.LevelDebug)
//	logger.Debugf("Debug %s", "message") // Output: [app] DEBUG: Debug message
func (l *Logger) Debugf(format string, args ...any) {
	// check if suspended
	if l.suspend.Load() {
		return
	}
	l.Debug(fmt.Sprintf(format, args...))
}

// Disable deactivates logging, suppressing all logs regardless of level or namespace.
// It is thread-safe using a write lock and returns the logger for chaining.
// Example:
//
//	logger := New("app").Enable().Disable()
//	logger.Info("Ignored") // Inactive output
func (l *Logger) Disable() *Logger {
	l.enabled.Store(lx.Inactive)
	return l
}

// Dump displays a hex and ASCII representation of a value's binary form, using gob
// encoding or direct conversion. It is useful for inspecting binary data structures.
// Example:
//
//	type Data struct { X int; Y string }
//	logger.Dump(Data{42, "test"}) // Outputs hex/ASCII dump
func (l *Logger) Dump(values ...interface{}) {
	if l.suspend.Load() {
		return
	}

	// Iterate over each value to dump
	for _, value := range values {
		// Log value description and type
		l.Infof("Dumping %v (%T)", value, value)
		var by []byte
		var err error
		// Convert value to byte slice based on type
		switch v := value.(type) {
		case []byte:
			by = v
		case string:
			by = []byte(v)
		case float32:
			// Convert float32 to 4-byte big-endian
			buf := make([]byte, 4)
			binary.BigEndian.PutUint32(buf, math.Float32bits(v))
			by = buf
		case float64:
			// Convert float64 to 8-byte big-endian
			buf := make([]byte, 8)
			binary.BigEndian.PutUint64(buf, math.Float64bits(v))
			by = buf
		case int, int8, int16, int32, int64:
			// Convert signed integer to 8-byte big-endian
			by = make([]byte, 8)
			binary.BigEndian.PutUint64(by, uint64(reflect.ValueOf(v).Int()))
		case uint, uint8, uint16, uint32, uint64:
			// Convert unsigned integer to 8-byte big-endian
			by = make([]byte, 8)
			binary.BigEndian.PutUint64(by, reflect.ValueOf(v).Uint())
		case io.Reader:
			// Read all bytes from io.Reader
			by, err = io.ReadAll(v)
		default:
			// Fallback to JSON marshaling for complex types
			by, err = json.Marshal(v)
		}
		// Log error if conversion fails
		if err != nil {
			l.Errorf("Dump error: %v", err)
			continue
		}
		// Generate hex/ASCII dump
		n := len(by)
		s := strings.Builder{}
		s.Grow((n/8 + 1) * 40)
		// Process 8-byte rows. A row is emitted only while there is data left, so
		// lengths that are an exact multiple of 8 do not produce phantom empty rows.
		for i := 0; i < n; i += 8 {
			end := i + 8
			if end > n {
				end = n
			}
			rowcount := end - i
			// Write position and hex prefix
			fmt.Fprintf(&s, "pos %02d  hex:  ", i)
			// Write hex values
			for j := i; j < end; j++ {
				fmt.Fprintf(&s, "%02x  ", by[j])
			}
			// Pad with spaces for alignment
			for j := rowcount; j < 8; j++ {
				s.WriteString("    ")
			}
			// Write ASCII representation
			s.WriteString("  '")
			s.WriteString(viewString(by[i:end]))
			s.WriteString("'\n")
		}
		// Nothing to dump (e.g. an empty byte slice): skip the entry entirely.
		if n == 0 {
			continue
		}
		// Log the hex/ASCII dump
		l.logChecked(lx.LevelNone, lx.ClassDump, s.String(), nil, false)
	}
}

// Output logs each value as pretty-printed JSON for REST debugging.
// Each value is logged on its own line with [file:line] and a blank line after the header.
// Ideal for inspecting outgoing/incoming REST payloads.
func (l *Logger) Output(values ...interface{}) {
	if l.suspend.Load() {
		return
	}

	l.output(2, values...)
}

// mark logs the caller's file and line number along with an optional custom name label for tracing execution flow.
func (l *Logger) output(skip int, values ...interface{}) {
	if l.suspend.Load() {
		return
	}

	if !l.shouldLog(lx.LevelInfo) {
		return
	}
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return
	}
	shortFile := file
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		shortFile = file[idx+1:]
	}
	header := fmt.Sprintf("[%s:%d] JSON:\n", shortFile, line)
	for _, v := range values {
		// Always pretty-print with indent
		b, err := json.MarshalIndent(v, "  ", "  ")
		if err != nil {
			b, _ = json.MarshalIndent(map[string]any{
				"value": fmt.Sprintf("%+v", v),
				"error": err.Error(),
			}, "  ", "  ")
		}
		l.logChecked(lx.LevelInfo, lx.ClassOutput, header+string(b), nil, false)
	}
}

// Inspect logs one or more values in a **developer-friendly, deeply introspective format** at Info level.
// It includes the caller file and line number, and reveals **all fields** â    including:
//
//   - Private (unexported) fields â    prefixed with `(field)`
//   - Embedded structs (inlined)
//   - Pointers and nil values â    shown as `*(field)` or `nil`
//   - Full struct nesting and type information
//
// This method uses `NewInspector` under the hood, which performs **full reflection-based traversal**.
// It is **not** meant for production logging or REST APIs â    use `Output` for that.
//
// Ideal for:
//   - Debugging complex internal state
//   - Inspecting structs with private fields
//   - Understanding struct embedding and pointer behavior
func (l *Logger) Inspect(values ...interface{}) {
	if l.suspend.Load() {
		return
	}
	o := NewInspector(l)
	o.Log(2, values...)
}

// Enable activates logging, allowing logs to be emitted if other conditions (e.g., level,
// namespace) are met. It is thread-safe using a write lock and returns the logger for chaining.
// Example:
//
//	logger := New("app").Enable()
//	logger.Info("Started") // Output: [app] INFO: Started
func (l *Logger) Enable() *Logger {
	l.enabled.Store(lx.Active)
	return l
}

// Enabled checks if the logger is enabled for logging. It is thread-safe using a read lock.
// Example:
//
//	logger := New("app").Enable()
//	if logger.Enabled() {
//	    logger.Info("Logging is enabled") // Output: [app] INFO: Logging is enabled
//	}
func (l *Logger) Enabled() bool {
	return l.enabled.Load() == lx.Active
}

// Err adds one or more errors to the logger's context and logs them at Error level.
// Non-nil errors are stored in the "error" context field (single error or slice) and
// logged as a concatenated string (e.g., "failed 1; failed 2"). It is thread-safe.
//
// The errors become part of the logger's persistent context, so they are included
// in every subsequent log entry from this logger.
// Example:
//
//	logger := New("app").Enable()
//	err1 := errors.New("failed 1")
//	err2 := errors.New("failed 2")
//	logger.Err(err1, err2)
//	logger.Info("Error occurred")
//	// Output: [app] ERROR: failed 1; failed 2
//	//         [app] INFO: Error occurred [error=[failed 1 failed 2]]
func (l *Logger) Err(errs ...error) {
	if l.suspend.Load() {
		return
	}

	// Skip logging if Error level is not enabled
	if !l.shouldLog(lx.LevelError) {
		return
	}

	// Collect non-nil errors and build log message
	var nonNilErrors []error
	var builder strings.Builder
	count := 0
	for i, err := range errs {
		if err != nil {
			if i > 0 && count > 0 {
				builder.WriteString("; ")
			}
			builder.WriteString(err.Error())
			nonNilErrors = append(nonNilErrors, err)
			count++
		}
	}
	if count == 0 {
		return
	}

	// Mutate the persistent context under the write lock, then release it before
	// logging: emit takes the read lock and sync.RWMutex is not reentrant.
	l.mu.Lock()
	if l.context == nil {
		l.context = make(lx.Fields, 0, 4)
	}
	if count == 1 {
		// Store single error directly
		l.context = append(l.context, lx.Field{Key: "error", Value: nonNilErrors[0]})
	} else {
		// Store slice of errors
		l.context = append(l.context, lx.Field{Key: "error", Value: nonNilErrors})
	}
	l.publish()
	l.mu.Unlock()

	// Log concatenated error messages
	l.logChecked(lx.LevelError, lx.ClassText, builder.String(), nil, false)
}

// Error logs a message at Error level, formatting it and delegating to the internal
// log method. It is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Error("Error occurred") // Output: [app] ERROR: Error occurred
func (l *Logger) Error(args ...any) {
	if l.suspend.Load() {
		return
	}
	// Skip logging if Error level is not enabled
	if !l.shouldLog(lx.LevelError) {
		return
	}
	l.logChecked(lx.LevelError, lx.ClassText, cat.Space(args...), nil, false)
}

// Errorf logs a formatted message at Error level, delegating to Error. It is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Errorf("Error %s", "occurred") // Output: [app] ERROR: Error occurred
func (l *Logger) Errorf(format string, args ...any) {
	// check if suspended
	if l.suspend.Load() {
		return
	}
	l.Error(fmt.Errorf(format, args...))
}

// Fatal logs a message at Error level with a stack trace and exits the program with
// exit code 1. It is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Fatal("Fatal error") // Output: [app] ERROR: Fatal error [stack=...], then exits
func (l *Logger) Fatal(args ...any) {
	if l.suspend.Load() {
		return
	}
	if !l.shouldLog(lx.LevelError) {
		os.Exit(1)
	}
	l.log(lx.LevelFatal, lx.ClassText, cat.Space(args...), nil, l.fatalStack)
	if l.fatalExits {
		os.Exit(1)
	}
}

// Fatalf logs a formatted message at Error level with a stack trace and exits the program.
// It delegates to Fatal and is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Fatalf("Fatal %s", "error") // Output: [app] ERROR: Fatal error [stack=...], then exits
func (l *Logger) Fatalf(format string, args ...any) {
	if l.suspend.Load() {
		return
	}
	l.Fatal(fmt.Sprintf(format, args...))
}

// FieldOne logs a message at Error level with a stack trace and exits the program with
// exit code 1. It is thread-safe.
func (l *Logger) FieldOne(key string, value any) *FieldBuilder {
	fb := fieldBuilderPool.Get().(*FieldBuilder)
	fb.logger = l
	fb.fields = fb.fields[:1]
	fb.fields[0] = lx.Field{Key: key, Value: value}
	return fb
}

// FieldSet avoids variadic allocation overhead by accepting a slice of strongly typed fields.
// Ideally, lx.Field is struct{Key string, Value any}
func (l *Logger) FieldSet(fields []lx.Field) *FieldBuilder {
	fb := getFieldBuilder(l, len(fields))
	if l.suspend.Load() {
		return fb
	}
	fb.fields = append(fb.fields, fields...)
	return fb
}

// Field starts a fluent chain for adding fields from a map, creating a FieldBuilder
// for type-safe field addition. It is thread-safe via the FieldBuilderâ   s logger.
// Example:
//
//	logger := New("app").Enable()
//	logger.Field(map[string]interface{}{"user": "alice"}).Info("Action") // Output: [app] INFO: Action [user=alice]
//
// Field starts a fluent chain for adding fields from a map
func (l *Logger) Field(fields map[string]interface{}) *FieldBuilder {
	fb := getFieldBuilder(l, len(fields))
	if l.suspend.Load() {
		return fb
	}
	for k, v := range fields {
		fb.fields = append(fb.fields, lx.Field{Key: k, Value: v})
	}
	return fb
}

// Fields starts a fluent chain for adding fields using variadic key-value pairs.
// It creates a FieldBuilder to attach fields, handling non-string keys or uneven pairs by
// adding an error field. Thread-safe via the FieldBuilder's logger.
// Example:
//
//	logger.Fields("user", "alice").Info("Action") // Output: [app] INFO: Action [user=alice]
func (l *Logger) Fields(pairs ...any) *FieldBuilder {
	fb := getFieldBuilder(l, len(pairs)/2)
	if l.suspend.Load() {
		return fb
	}
	// Process key-value pairs
	for i := 0; i < len(pairs)-1; i += 2 {
		if key, ok := pairs[i].(string); ok {
			fb.fields = append(fb.fields, lx.Field{Key: key, Value: pairs[i+1]})
		} else {
			// Log error for non-string keys
			fb.fields = append(fb.fields, lx.Field{
				Key:   "error",
				Value: fmt.Errorf("non-string key in Fields: %v", pairs[i]),
			})
		}
	}
	// Log error for uneven pairs
	if len(pairs)%2 != 0 {
		fb.fields = append(fb.fields, lx.Field{
			Key:   "error",
			Value: fmt.Errorf("uneven key-value pairs in Fields: [%v]", pairs[len(pairs)-1]),
		})
	}
	return fb
}

// GetContext returns the logger's context map of persistent key-value fields. It is
// thread-safe using a read lock.
// Example:
//
//	logger := New("app").AddContext("user", "alice")
//	ctx := logger.GetContext() // Returns map[string]interface{}{"user": "alice"}
func (l *Logger) GetContext() map[string]interface{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	// Convert slice to map for backward compatibility
	contextMap := make(map[string]interface{}, len(l.context))
	for _, pair := range l.context {
		contextMap[pair.Key] = pair.Value
	}
	return contextMap
}

// GetHandler returns the logger's current handler for customization or inspection.
// The returned handler should not be modified concurrently with logger operations.
// Example:
//
//	logger := New("app")
//	handler := logger.GetHandler() // Returns the current handler (e.g., TextHandler)
func (l *Logger) GetHandler() lx.Handler {
	return l.handler
}

// GetLevel returns the minimum log level for the logger. It is thread-safe using a read lock.
// Example:
//
//	logger := New("app").Level(lx.LevelWarn)
//	if logger.GetLevel() == lx.LevelWarn {
//	    logger.Warn("Warning level set") // Output: [app] WARN: Warning level set
//	}
func (l *Logger) GetLevel() lx.LevelType {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// FatalExits reports whether Fatal/Fatalf terminate the process.
// Exiting is opt-in via WithFatalExits(true); by default Fatal only logs
// (matching the library's "never kill the host application by surprise" stance).
func (l *Logger) FatalExits() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.fatalExits
}

// FatalStack reports whether Fatal entries capture a stack trace.
// It is configured with WithFatalStack(true).
func (l *Logger) FatalStack() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.fatalStack
}

// GetPath returns the logger's current namespace path. It is thread-safe using a read lock.
// Example:
//
//	logger := New("app").Namespace("sub")
//	path := logger.GetPath() // Returns "app/sub"
func (l *Logger) GetPath() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.currentPath
}

// GetSeparator returns the logger's namespace separator (e.g., "/"). It is thread-safe
// using a read lock.
// Example:
//
//	logger := New("app").Separator(".")
//	sep := logger.GetSeparator() // Returns "."
func (l *Logger) GetSeparator() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.separator
}

// GetStyle returns the logger's namespace formatting style (FlatPath or NestedPath).
// It is thread-safe using a read lock.
// Example:
//
//	logger := New("app").Style(lx.NestedPath)
//	if logger.GetStyle() == lx.NestedPath {
//	    logger.Info("Nested style") // Output: [app]: INFO: Nested style
//	}
func (l *Logger) GetStyle() lx.StyleType {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.style
}

// Handler sets the handler for processing log entries, configuring the output destination
// and format (e.g., text, JSON). It is thread-safe using a write lock and returns the
// logger for chaining.
// Example:
//
//	logger := New("app").Enable().Handler(lh.NewTextHandler(os.Stdout))
//	logger.Info("Log") // Output: [app] INFO: Log
func (l *Logger) Handler(handler lx.Handler) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handler = handler
	l.publish()
	return l
}

// Caller enables or disables caller reporting ("file.go:42") on every entry.
// Capturing the caller walks the stack, so it is off by default.
// Example:
//
//	logger := New("app", WithCaller(true))
//	logger.Info("started") // Output: [main.go:12] [app] INFO: started
func (l *Logger) Caller(enabled bool) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.caller = enabled
	l.publish()
	return l
}

// OrderedLevels enables severity-based level filtering (see WithOrderedLevels).
func (l *Logger) OrderedLevels(enabled bool) *Logger {
	l.ordered.Store(enabled)
	return l
}

// Log emits a message at an arbitrary level. It is the dynamic-level entry point
// used by adapters and by code that chooses a level at runtime.
// Example:
//
//	logger.Log(lx.LevelWarn, "disk", "almost full")
func (l *Logger) Log(level lx.LevelType, args ...any) {
	if l.suspend.Load() {
		return
	}
	if !l.shouldLog(level) {
		return
	}
	l.logChecked(level, lx.ClassText, cat.Space(args...), nil, false)
}

// Logf emits a formatted message at an arbitrary level.
// Example:
//
//	logger.Logf(lx.LevelError, "%d requests failed", n)
func (l *Logger) Logf(level lx.LevelType, format string, args ...any) {
	if l.suspend.Load() {
		return
	}
	if !l.shouldLog(level) {
		return
	}
	l.logChecked(level, lx.ClassText, fmt.Sprintf(format, args...), nil, false)
}

// Ctx derives a logger that carries the fields produced by the logger's context
// extractor (see WithContextExtractor) for the given context. The parent logger
// is never mutated; the returned logger inherits its configuration and context.
//
// Example:
//
//	logger = logger.Ctx(r.Context())
//	logger.Info("handled") // includes trace_id/request_id if configured
func (l *Logger) Ctx(ctx context.Context) *Logger {
	l.mu.RLock()
	extract := l.ctxFields
	l.mu.RUnlock()
	if extract == nil || ctx == nil {
		return l
	}
	ctxFields := extract(ctx)
	if len(ctxFields) == 0 {
		return l
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	child := &Logger{
		level:           l.level,
		atomicLevel:     l.atomicLevel,
		namespaces:      l.namespaces,
		currentPath:     l.currentPath,
		context:         make(lx.Fields, 0, len(l.context)+len(ctxFields)),
		style:           l.style,
		handler:         l.handler,
		middleware:      l.middleware,
		prefix:          l.prefix,
		indent:          l.indent,
		stackBufferSize: l.stackBufferSize,
		separator:       l.separator,
		fatalExits:      l.fatalExits,
		fatalStack:      l.fatalStack,
		caller:          l.caller,
		ctxFields:       extract,
	}
	child.enabled.Store(l.enabled.Load())
	child.suspend.Store(l.suspend.Load())
	child.labels.Store(l.labels.Load())
	child.ordered.Store(l.ordered.Load())
	child.context = append(child.context, l.context...)
	child.context = append(child.context, ctxFields...)
	child.publish()
	return child
}

// Flush flushes any buffered handler in the chain (e.g. lh.Buffered).
// It walks fan-out handlers such as lh.MultiHandler.
func (l *Logger) Flush() error {
	return flushHandler(l.config().handler)
}

// Close flushes and closes every handler in the chain that supports it
// (e.g. lh.Buffered, lh.Rotating, victoria). Handlers are released resources
// in a best-effort manner; the first error is returned.
func (l *Logger) Close() error {
	h := l.config().handler
	if err := flushHandler(h); err != nil {
		return err
	}
	return closeHandler(h)
}

// flushHandler flushes h, recursing into fan-out handlers.
func flushHandler(h lx.Handler) error {
	if h == nil {
		return nil
	}
	var err error
	if w, ok := h.(lx.Walker); ok {
		w.Walk(func(child lx.Handler) {
			if e := flushHandler(child); e != nil && err == nil {
				err = e
			}
		})
		return err
	}
	if f, ok := h.(lx.Flusher); ok {
		f.Flush()
	}
	return err
}

// closeHandler closes h, recursing into fan-out handlers.
func closeHandler(h lx.Handler) error {
	if h == nil {
		return nil
	}
	var err error
	if w, ok := h.(lx.Walker); ok {
		w.Walk(func(child lx.Handler) {
			if e := closeHandler(child); e != nil && err == nil {
				err = e
			}
		})
		return err
	}
	if c, ok := h.(lx.Closer); ok {
		return c.Close()
	}
	return nil
}

// NamespaceLevel sets a minimum severity for a namespace path and its descendants,
// e.g. NamespaceLevel("db", lx.LevelWarn) keeps only Warn/Error/Fatal from that
// subtree. Unlike Logger.Level it always uses severity ordering (see
// lx.LevelType.Severity). It composes with NamespaceEnable/NamespaceDisable: a
// namespace must be enabled (or the logger enabled) and the entry must meet the
// namespace level.
// Example:
//
//	logger.NamespaceLevel("app/db", lx.LevelWarn) // quiet the db namespace
func (l *Logger) NamespaceLevel(relativePath string, level lx.LevelType) *Logger {
	l.mu.RLock()
	fullPath := l.joinPath(l.currentPath, relativePath)
	l.mu.RUnlock()
	l.namespaces.SetLevel(fullPath, level)
	return l
}

// Line adds vertical spacing (newlines) to the log output, defaulting to 1 if no arguments
func (l *Logger) Indent(depth int) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.indent = depth
	l.publish()
	return l
}

// Info logs a message at Info level, formatting it and delegating to the internal log
// method. It is thread-safe.
// Example:
//
//	logger := New("app").Enable().Style(lx.NestedPath)
//	logger.Info("Started") // Output: [app]: INFO: Started
func (l *Logger) Info(args ...any) {
	if l.suspend.Load() {
		return
	}
	if !l.shouldLog(lx.LevelInfo) {
		return
	}
	l.logChecked(lx.LevelInfo, lx.ClassText, cat.Space(args...), nil, false)
}

// Infof logs a formatted message at Info level, delegating to Info. It is thread-safe.
// Example:
//
//	logger := New("app").Enable().Style(lx.NestedPath)
//	logger.Infof("Started %s", "now") // Output: [app]: INFO: Started now
func (l *Logger) Infof(format string, args ...any) {
	if l.suspend.Load() {
		return
	}
	l.Info(fmt.Sprintf(format, args...))
}

// Len returns the total number of log entries sent to the handler, using atomic operations
// for thread safety.
// Example:
//
//	logger := New("app").Enable()
//	logger.Info("Test")
//	count := logger.Len() // Returns 1
func (l *Logger) Len() int64 {
	return l.entries.Load()
}

// Labels temporarily attaches one or more label names to the logger for the next log entry.
// Labels are typically used for metrics, benchmarking, tracing, or categorizing logs in a structured way.
//
// The labels are stored atomically and intended to be short-lived, applying only to the next
// log operation (or until overwritten by a subsequent call to Labels). Multiple labels can
// be provided as separate string arguments.
//
// Example usage:
//
//	logger := New("app").Enable()
//
//	// Add labels for a specific operation
//	logger.Labels("load_users", "process_orders").Measure(func() {
//	    // ... perform work ...
//	}, func() {
//	    // ... optional callback ...
//	})
func (l *Logger) Labels(names ...string) *Logger {
	l.labels.Store(&names) // store temporarily
	return l
}

// Level sets the minimum log level, ignoring messages below it. It is thread-safe using
// a write lock and returns the logger for chaining.
// Example:
//
//	logger := New("app").Enable().Level(lx.LevelWarn)
//	logger.Info("Ignored") // Inactive output
//	logger.Warn("Logged") // Output: [app] WARN: Logged
func (l *Logger) Level(level lx.LevelType) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
	atomic.StoreInt32(&l.atomicLevel, int32(level))
	return l
}

// Line adds vertical spacing (newlines) to the log output, defaulting to 1 if no arguments
// are provided. Multiple values are summed for total lines. It is thread-safe and returns
// the logger for chaining.
// Example:
//
//	logger := New("app").Enable()
//	logger.Line(2).Info("After 2 newlines") // Adds 2 blank lines before logging
//	logger.Line().Error("After 1 newline")  // Defaults to 1
func (l *Logger) Line(lines ...int) *Logger {
	line := 1 // Default to 1 newline
	if len(lines) > 0 {
		line = 0
		// Sum all provided line counts
		for _, n := range lines {
			line += n
		}
		// Ensure at least 1 line
		if line < 1 {
			line = 1
		}
	}
	l.log(lx.LevelNone, lx.ClassRaw, strings.Repeat(lx.Newline, line), nil, false)
	return l
}

// Mark logs the current file and line number where it's called, without any additional debug information.
// It's useful for tracing execution flow without the verbosity of Dbg.
// Example:
//
//	logger.Mark() // *MARK*: [file.go:123]
func (l *Logger) Mark(name ...string) {
	if l.suspend.Load() {
		return
	}
	l.mark(2, name...)
}

// mark logs the caller's file and line number along with an optional custom name label for tracing execution flow.
func (l *Logger) mark(skip int, names ...string) {
	if l.suspend.Load() {
		return
	}
	// Skip logging if Info level is not enabled
	if !l.shouldLog(lx.LevelInfo) {
		return
	}
	// Get caller information (file, line)
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		l.logChecked(lx.LevelError, lx.ClassText, "Mark: Unable to parse runtime caller", nil, false)
		return
	}
	// Extract just the filename (without full path)
	shortFile := file
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		shortFile = file[idx+1:]
	}
	name := strings.Join(names, l.separator)
	if name == "" {
		name = "MARK"
	}
	// Format as [filename:line]
	out := fmt.Sprintf("[*%s*]: [%s:%d]\n", name, shortFile, line)
	l.logChecked(lx.LevelInfo, lx.ClassRaw, out, nil, false)
}

// Namespace creates a child logger with a sub-namespace appended to the current path,
// inheriting the parent configuration and context fields (copy-on-write: fields added
// to either logger never leak into the other). It is thread-safe using a read lock.
// Example:
//
//	parent := New("parent").Enable()
//	child := parent.Namespace("child")
//	child.Info("Child log") // Output: [parent/child] INFO: Child log
func (l *Logger) Namespace(name string) *Logger {
	if l.suspend.Load() {
		return l
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	// Construct full namespace path
	fullPath := name
	if l.currentPath != "" {
		fullPath = l.currentPath + l.separator + name
	}
	// Create child logger with inherited configuration
	child := &Logger{
		level:           l.level,
		atomicLevel:     l.atomicLevel,
		namespaces:      l.namespaces,
		currentPath:     fullPath,
		context:         l.context[:len(l.context):len(l.context)], // inherited, copy-on-append
		style:           l.style,
		handler:         l.handler,
		middleware:      l.middleware,
		prefix:          l.prefix,
		indent:          l.indent,
		stackBufferSize: l.stackBufferSize,
		separator:       l.separator,
		fatalExits:      l.fatalExits,
		fatalStack:      l.fatalStack,
	}
	// Atomic fields must be copied by value assignment, never by struct copy (go vet).
	child.enabled.Store(l.enabled.Load())
	child.suspend.Store(l.suspend.Load())
	child.labels.Store(l.labels.Load())
	child.publish()
	return child
}

// NamespaceDisable disables logging for a namespace and its children, invalidating the
// namespace cache. It is thread-safe via lx.Namespaceâ   s sync.Map and returns the logger
// for chaining.
// Example:
//
//	logger := New("parent").Enable().NamespaceDisable("parent/child")
//	logger.Namespace("child").Info("Ignored") // Inactive output
func (l *Logger) NamespaceDisable(relativePath string) *Logger {
	l.mu.RLock()
	fullPath := l.joinPath(l.currentPath, relativePath)
	l.mu.RUnlock()
	// Disable namespace in shared store
	l.namespaces.Set(fullPath, false)
	return l
}

// NamespaceEnable enables logging for a namespace and its children, invalidating the
// namespace cache. It is thread-safe via lx.Namespaceâ   s sync.Map and returns the logger
// for chaining.
// Example:
//
//	logger := New("parent").Enable().NamespaceEnable("parent/child")
//	logger.Namespace("child").Info("Log") // Output: [parent/child] INFO: Log
func (l *Logger) NamespaceEnable(relativePath string) *Logger {
	l.mu.RLock()
	fullPath := l.joinPath(l.currentPath, relativePath)
	l.mu.RUnlock()
	// Enable namespace in shared store
	l.namespaces.Set(fullPath, true)
	return l
}

// NamespaceEnabled checks if a namespace is enabled, considering parent namespaces and
// caching results for performance. It is thread-safe using a read lock.
// Example:
//
//	logger := New("parent").Enable().NamespaceDisable("parent/child")
//	enabled := logger.NamespaceEnabled("parent/child") // false
func (l *Logger) NamespaceEnabled(relativePath string) bool {
	fullPath := l.joinPath(l.currentPath, relativePath)
	separator := l.separator
	if separator == "" {
		separator = lx.Slash
	}
	// Handle root path case
	if fullPath == "" && relativePath == "" {
		return l.enabled.Load() == lx.Active
	}
	if fullPath != "" {
		// Check namespace rules
		isEnabledByNSRule, isDisabledByNSRule := l.namespaces.Enabled(fullPath, separator)
		if isDisabledByNSRule {
			return false
		}
		if isEnabledByNSRule {
			return true
		}
	}
	// Fall back to logger's enabled state
	return l.enabled.Load() == lx.Active
}

// Panic logs a message at Error level with a stack trace and triggers a panic. It is
// thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Panic("Panic error") // Output: [app] ERROR: Panic error [stack=...], then panics
func (l *Logger) Panic(args ...any) {
	// Build message by concatenating arguments with spaces
	msg := cat.Space(args...)
	if l.suspend.Load() {
		panic(msg)
	}
	// Panic immediately if Error level is not enabled
	if !l.shouldLog(lx.LevelError) {
		panic(msg)
	}
	l.log(lx.LevelFatal, lx.ClassText, msg, nil, true)
	panic(msg)
}

// Panicf logs a formatted message at Error level with a stack trace and triggers a panic.
// It delegates to Panic and is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Panicf("Panic %s", "error") // Output: [app] ERROR: Panic error [stack=...], then panics
func (l *Logger) Panicf(format string, args ...any) {
	l.Panic(fmt.Sprintf(format, args...))
}

// Prefix sets a prefix prepended to all log messages. It is thread-safe using a write
// lock and returns the logger for chaining.
// Example:
//
//	logger := New("app").Enable().Prefix("APP: ")
//	logger.Info("Started") // Output: [app] INFO: APP: Started
func (l *Logger) Prefix(prefix string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prefix = prefix
	l.publish()
	return l
}

// Print logs a message at Info level without format specifiers, minimizing allocations
// by concatenating arguments with spaces. It is thread-safe via the log method.
// Example:
//
//	logger := New("app").Enable()
//	logger.Print("message", "value") // Output: [app] INFO: message value
func (l *Logger) Print(args ...any) {
	if l.suspend.Load() {
		return
	}
	// Skip logging if Info level is not enabled
	if !l.shouldLog(lx.LevelInfo) {
		return
	}
	l.logChecked(lx.LevelNone, lx.ClassRaw, cat.Space(args...), nil, false)
}

// Println logs a message at Info level without format specifiers, minimizing allocations
// by concatenating arguments with spaces. It is thread-safe via the log method.
// Example:
//
//	logger := New("app").Enable()
//	logger.Println("message", "value") // Output: [app] INFO: message value
func (l *Logger) Println(args ...any) {
	if l.suspend.Load() {
		return
	}
	// Skip logging if Info level is not enabled
	if !l.shouldLog(lx.LevelInfo) {
		return
	}
	l.logChecked(lx.LevelNone, lx.ClassRaw, cat.SuffixWith(lx.Space, lx.Newline, args...), nil, false)
}

// Printf logs a formatted message at Info level, delegating to Print. It is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Printf("Message %s", "value") // Output: [app] INFO: Message value
func (l *Logger) Printf(format string, args ...any) {
	if l.suspend.Load() {
		return
	}
	l.Print(fmt.Sprintf(format, args...))
}

// Remove removes middleware by the reference returned from Use, delegating to the
// Middlewareâ   s Remove method for thread-safe removal.
// Example:
//
//	logger := New("app").Enable()
//	mw := logger.Use(someMiddleware)
//	logger.Remove(mw) // Removes middleware
func (l *Logger) Remove(m *Middleware) {
	m.Remove()
}

// Resume reactivates logging for the current logger after it has been suspended.
// It clears the suspend flag, allowing logs to be emitted if other conditions (e.g., level, namespace)
// are met. Thread-safe with a write lock. Returns the logger for method chaining.
// Example:
//
//	logger := New("app").Enable().Suspend()
//	logger.Resume()
//	logger.Info("Resumed") // Output: [app] INFO: Resumed
func (l *Logger) Resume() *Logger {
	l.suspend.Store(false)
	return l
}

// Separator sets the namespace separator for grouping namespaces and log entries (e.g., "/" or ".").
// It is thread-safe using a write lock and returns the logger for chaining.
// Example:
//
//	logger := New("app").Separator(".")
//	logger.Namespace("sub").Info("Log") // Output: [app.sub] INFO: Log
func (l *Logger) Separator(separator string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.separator = separator
	l.publish()
	return l
}

// Suspend temporarily deactivates logging for the current logger.
// It sets the suspend flag, suppressing all logs regardless of level or namespace until resumed.
// Thread-safe with a write lock. Returns the logger for method chaining.
// Example:
//
//	logger := New("app").Enable()
//	logger.Suspend()
//	logger.Info("Ignored") // Inactive output
func (l *Logger) Suspend() *Logger {
	l.suspend.Store(true)
	return l
}

// Suspended returns whether the logger is currently suspended.
// It provides thread-safe read access to the suspend flag using a write lock.
// Example:
//
//	logger := New("app").Enable().Suspend()
//	if logger.Suspended() {
//	    fmt.Println("Logging is suspended") // Prints message
//	}
func (l *Logger) Suspended() bool {
	return l.suspend.Load()
}

// Stack logs messages at Error level with a stack trace for each provided argument.
// It is thread-safe and skips logging if Debug level is not enabled.
// Example:
//
//	logger := New("app").Enable()
//	logger.Stack("Critical error") // Output: [app] ERROR: Critical error [stack=...]
func (l *Logger) Stack(args ...any) {
	for _, arg := range args {
		l.log(lx.LevelError, lx.ClassStack, cat.Concat(arg), nil, true)
	}
}

// Stackf logs a formatted message at Error level with a stack trace, delegating to Stack.
// It is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Stackf("Critical %s", "error") // Output: [app] ERROR: Critical error [stack=...]
func (l *Logger) Stackf(format string, args ...any) {
	l.Stack(fmt.Sprintf(format, args...))
}

// StackSize sets the buffer size for stack trace capture in Stack, Fatal, and Panic methods.
// It is thread-safe using a write lock and returns the logger for chaining.
// Example:
//
//	logger := New("app").Enable().StackSize(65536)
//	logger.Stack("Error") // Captures up to 64KB stack trace
func (l *Logger) StackSize(size int) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	if size > 0 {
		l.stackBufferSize = size
	}
	l.publish()
	return l
}

// Style sets the namespace formatting style (FlatPath or NestedPath). FlatPath uses
// [parent/child], while NestedPath uses [parent]â   [child]. It is thread-safe using a write
// lock and returns the logger for chaining.
// Example:
//
//	logger := New("parent/child").Enable().Style(lx.NestedPath)
//	logger.Info("Log") // Output: [parent]â   [child]: INFO: Log
func (l *Logger) Style(style lx.StyleType) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.style = style
	l.publish()
	return l
}

// Timestamped enables or disables timestamp logging for the logger and optionally sets the timestamp format.
// It is thread-safe, using a write lock to ensure safe concurrent access.
// If the logger's handler supports the lx.Timestamper interface, the timestamp settings are applied.
// The method returns the logger instance to support method chaining.
// Parameters:
//
//	enable: Boolean to enable or disable timestamp logging
//	format: Optional string(s) to specify the timestamp format
func (l *Logger) Timestamped(enable bool, format ...string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h, ok := l.handler.(lx.Timestamper); ok {
		h.Timestamped(enable, format...)
	}
	return l
}

// Toggle enables or disables the logger based on the provided boolean value and returns the updated logger instance.
func (l *Logger) Toggle(v bool) *Logger {
	if v {
		l.Resume()
		return l.Enable()
	}
	l.Suspend()
	return l.Disable()
}

// Use adds a middleware function to process log entries before they are handled, returning
// a Middleware handle for removal. Middleware returning a non-nil error stops the log.
// It is thread-safe using a write lock.
// Example:
//
//	logger := New("app").Enable()
//	mw := logger.Use(ll.FuncMiddleware(func(e *lx.Entry) error {
//	    if e.Level < lx.LevelWarn {
//	        return fmt.Errorf("level too low")
//	    }
//	    return nil
//	}))
//	logger.Info("Ignored") // Inactive output
//	mw.Remove()
//	logger.Info("Now logged") // Output: [app] INFO: Now logged
func (l *Logger) Use(fn lx.Handler) *Middleware {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Assign a unique ID to the middleware
	id := len(l.middleware) + 1
	// Append middleware to the chain
	l.middleware = append(l.middleware, Middleware{id: id, fn: fn})
	l.publish()
	return &Middleware{
		logger: l,
		id:     id,
	}
}

// Warn logs a message at Warn level, formatting it and delegating to the internal log
// method. It is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Warn("Warning") // Output: [app] WARN: Warning
func (l *Logger) Warn(args ...any) {
	if l.suspend.Load() {
		return
	}
	// Skip logging if Warn level is not enabled
	if !l.shouldLog(lx.LevelWarn) {
		return
	}
	l.logChecked(lx.LevelWarn, lx.ClassText, cat.Space(args...), nil, false)
}

// Warnf logs a formatted message at Warn level, delegating to Warn. It is thread-safe.
// Example:
//
//	logger := New("app").Enable()
//	logger.Warnf("Warning %s", "issued") // Output: [app] WARN: Warning issued
func (l *Logger) Warnf(format string, args ...any) {
	if l.suspend.Load() {
		return
	}
	l.Warn(fmt.Sprintf(format, args...))
}

// Warning is an alias for Warn that logs a message or object at the warning level
// using variadic arguments.
func (l *Logger) Warning(args ...any) {
	l.Warn(args...)
}

// Warningf is an alias for Warnf that logs a formatted warning-level message
// using the provided format and arguments.
func (l *Logger) Warningf(format string, args ...any) {
	l.Warnf(format, args...)
}

// joinPath joins a base path and a relative path using the logger's separator, handling
// empty base or relative paths. It is used internally for namespace path construction.
// Example (internal usage):
//
//	logger.joinPath("parent", "child") // Returns "parent/child"
func (l *Logger) joinPath(base, relative string) string {
	if base == "" {
		return relative
	}
	if relative == "" {
		return base
	}
	separator := l.separator
	if separator == "" {
		separator = lx.Slash // Default separator
	}
	return cat.Concat(base, separator, relative)
}

// log is the internal method for processing a log entry, applying rate limiting, sampling,
// middleware, and context before passing to the handler. Middleware returning a non-nil
// error stops the log. It is thread-safe with read/write locks for configuration and stack
// trace buffer.
// Example (internal usage):
//
//	logger := New("app").Enable()
//	logger.Info("Test") // Calls log(lx.LevelInfo, "Test", nil, false)
//
// log is the internal method for processing a log entry, applying rate limiting, sampling,
// middleware, and context before passing to the handler. Middleware returning a non-nil
// error stops the log. It is thread-safe with read/write locks for configuration and stack
// trace buffer.
func (l *Logger) log(level lx.LevelType, class lx.ClassType, msg string, fields lx.Fields, withStack bool) {
	// Skip logging if level is not enabled (fast path)
	if !l.shouldLog(level) {
		return
	}
	l.emit(level, class, msg, fields, withStack)
}

// logChecked emits an entry whose level was already validated via shouldLog.
// It exists so the hot path performs the (comparatively expensive) enablement
// check exactly once instead of repeating it for every layer (Logger method →
// FieldBuilder/SinceBuilder → log).
func (l *Logger) logChecked(level lx.LevelType, class lx.ClassType, msg string, fields lx.Fields, withStack bool) {
	l.emit(level, class, msg, fields, withStack)
}

// shortPath returns the base name of a source file path.
func shortPath(file string) string {
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		return file[idx+1:]
	}
	return file
}

// emit builds the entry and hands it to the middleware chain and handler.
// It performs no level/enablement checks (callers do), but it does honour
// Suspend so that paths which bypass Logger methods (FieldBuilder, Line,
// logWriter) are still suppressed.
func (l *Logger) emit(level lx.LevelType, class lx.ClassType, msg string, fields lx.Fields, withStack bool) {
	if l.suspend.Load() {
		return
	}

	var stack []byte
	// Capture stack trace if requested (outside lock)
	if withStack {
		size := l.config().stackSize

		buf := stackBufPool.Get().([]byte)
		if cap(buf) < size {
			buf = make([]byte, size)
		} else {
			buf = buf[:size]
		}
		n := runtime.Stack(buf, false)
		stack = append([]byte(nil), buf[:n]...)
		stackBufPool.Put(buf)
	}

	// Read the published configuration snapshot: a single atomic load, no locking.
	c := l.config()
	handler := c.handler
	prefix := c.prefix
	indent := c.indent
	context := c.context
	style := c.style
	currentPath := c.path
	middleware := c.middleware

	// Capture the caller location when enabled (opt-in: it walks the stack).
	var caller string
	if c.caller {
		if file, line, ok := callerFrame(0); ok {
			caller = shortPath(file) + ":" + strconv.Itoa(line)
		}
	}

	// Apply prefix and indentation to the message. When neither is configured
	// (the common case) the message is passed through without copying.
	finalMsg := msg
	if indent > 0 || prefix != "" {
		var builder strings.Builder
		builder.Grow(indent*2 + len(prefix) + len(msg))
		if indent > 0 {
			builder.WriteString(strings.Repeat(lx.DoubleSpace, indent))
		}
		builder.WriteString(prefix)
		builder.WriteString(msg)
		finalMsg = builder.String()
	}

	// Optimized field merging - avoid allocation when possible
	var combinedFields lx.Fields
	var pooledFields *lx.Fields // Track if we allocated from pool

	if len(context) == 0 {
		combinedFields = fields
	} else if len(fields) == 0 {
		combinedFields = context
	} else {
		// Get pooled slice
		pooledFields = fieldsSlicePool.Get().(*lx.Fields)
		combinedFields = (*pooledFields)[:0] // Reset length, keep capacity
		combinedFields = append(combinedFields, context...)
		combinedFields = append(combinedFields, fields...)
	}

	// Get entry from pool
	entry := entryPool.Get().(*lx.Entry)

	// Ensure pool return on ALL paths (including middleware errors).
	// Fields/Stack are cleared so the pool never retains a reference to a
	// caller-owned or pooled backing array after the entry is recycled.
	defer func() {
		entry.Fields = nil
		entry.Stack = nil
		entryPool.Put(entry)
		if pooledFields != nil {
			*pooledFields = (*pooledFields)[:0]
			fieldsSlicePool.Put(pooledFields)
		}
	}()

	entry.Timestamp = time.Now()
	entry.Level = level
	entry.Message = finalMsg
	entry.Namespace = currentPath
	entry.Fields = combinedFields
	entry.Style = style
	entry.Class = class
	entry.Caller = caller
	entry.Stack = stack
	entry.Error = nil
	entry.Id = 0

	// Apply middleware, stopping if any returns an error
	for _, mw := range middleware {
		if err := mw.fn.Handle(entry); err != nil {
			// Defer handles pool return
			return
		}
	}

	// Pass to handler if set
	if handler != nil {
		_ = handler.Handle(entry)
		l.entries.Add(1)
	}
	// Defer handles pool return
}

// shouldLog determines if a log should be emitted based on enabled state, level,
// namespaces, sampling, and rate limits, caching namespace results for performance.
// It is thread-safe (lock-free on the common path).
//
// Note: levels are compared numerically (None=0 … Debug=5), so a lower value acts
// as the "minimum" threshold: a message is emitted when its level does not exceed
// the configured one.
// Example (internal usage):
//
//	logger := New("app").Enable().Level(lx.LevelWarn)
//	if logger.shouldLog(lx.LevelInfo) { // true under numeric ordering
//	    // Log would be skipped
//	}
func (l *Logger) shouldLog(level lx.LevelType) bool {
	// Skip if global logging system is inactive
	if !Active() {
		return false
	}

	// Level threshold: lock-free read of the configured minimum.
	min := lx.LevelType(atomic.LoadInt32(&l.atomicLevel))
	if l.ordered.Load() {
		// Opt-in severity ordering (see WithOrderedLevels).
		if level.Severity() < min.Severity() {
			return false
		}
	} else if level > min {
		return false
	}

	// Common case: no namespace rules or levels were ever registered, so
	// namespace state cannot override the logger's own enable flag. This avoids
	// the sync.Map lookups that used to happen on every single log call.
	if !l.namespaces.HasRules() && !l.namespaces.HasLevels() {
		return l.enabled.Load() == lx.Active
	}

	return l.namespaceLogs(level)
}

// namespaceLogs resolves the (rare) case where explicit namespace rules exist.
// The path/separator are read under the read lock to stay race-free with
// Separator()/NamespaceEnable() during runtime reconfiguration.
func (l *Logger) namespaceLogs(level lx.LevelType) bool {
	c := l.config()
	currentPath := c.path
	separator := c.separator

	if currentPath == "" {
		return l.enabled.Load() == lx.Active
	}
	if separator == "" {
		separator = lx.Slash
	}

	// Per-namespace threshold (NamespaceLevel). It always uses severity ordering:
	// it is a new API, so there is no legacy numeric behaviour to preserve, and
	// "only Warn and above" is the only sensible reading of a namespace level.
	if nsLevel, ok := l.namespaces.Level(currentPath, separator); ok {
		if level.Severity() < nsLevel.Severity() {
			return false
		}
	}

	// Check namespace rules
	isEnabledByNSRule, isDisabledByNSRule := l.namespaces.Enabled(currentPath, separator)
	if isDisabledByNSRule {
		return false
	}
	if isEnabledByNSRule {
		return true
	}
	// Fall back to logger's enabled state
	return l.enabled.Load() == lx.Active
}
