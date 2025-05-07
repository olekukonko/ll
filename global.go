package ll

import (
	"fmt"
	"github.com/olekukonko/ll/lx"
	"strings"
	"time"
)

// defaultLogger is the global logger instance for package-level logging functions.
// It provides a shared logger for convenience, allowing logging without explicitly creating
// a logger instance. The logger is initialized with default settings: disabled, Debug level,
// flat namespace style, and no handler. It is thread-safe due to the Logger struct’s mutex.
var defaultLogger = &Logger{
	enabled:         lx.DefaultEnabled,            // Initially disabled (lx.DefaultEnabled = false)
	level:           lx.LevelDebug,                // Minimum log level set to Debug
	namespaces:      defaultStore,                 // Shared namespace store for enable/disable states
	context:         make(map[string]interface{}), // Empty context for global fields
	style:           lx.FlatPath,                  // Flat namespace style (e.g., [parent/child])
	handler:         nil,                          // No default handler (must be set)
	middleware:      make([]Middleware, 0),        // Empty middleware chain
	stackBufferSize: 4096,                         // Buffer size for stack traces
}

// Handler sets the handler for the default logger.
// It configures the output destination and format (e.g., text, JSON) for logs emitted by
// defaultLogger. Returns the default logger for method chaining, enabling fluent configuration.
// Example:
//
//	ll.Handler(lh.NewTextHandler(os.Stdout)).Enable()
//	ll.Info("Started") // Output: [] INFO: Started
func Handler(handler lx.Handler) *Logger {
	return defaultLogger.Handler(handler)
}

// Level sets the minimum log level for the default logger.
// It determines which log messages (Debug, Info, Warn, Error) are emitted. Messages below
// the specified level are ignored. Returns the default logger for method chaining.
// Example:
//
//	ll.Level(lx.LevelWarn)
//	ll.Info("Ignored") // No output (below Warn level)
//	ll.Warn("Logged")  // Output: [] WARN: Logged
func Level(level lx.LevelType) *Logger {
	return defaultLogger.Level(level)
}

// Style sets the namespace style for the default logger.
// It controls how namespace paths are formatted in logs (FlatPath: [parent/child],
// NestedPath: [parent]→[child]). Returns the default logger for method chaining.
// Example:
//
//	ll.Style(lx.NestedPath)
//	ll.Info("Test") // Output: []: INFO: Test
func Style(style lx.StyleType) *Logger {
	return defaultLogger.Style(style)
}

// NamespaceEnable enables logging for a namespace and its children using the default logger.
// It activates logging for the specified namespace path (e.g., "app/db") and all its
// descendants. Returns the default logger for method chaining. Thread-safe via the Logger’s mutex.
// Example:
//
//	ll.NamespaceEnable("app/db")
//	ll.Clone().Namespace("db").Info("Query") // Output: [app/db] INFO: Query
func NamespaceEnable(path string) *Logger {
	return defaultLogger.NamespaceEnable(path)
}

// NamespaceDisable disables logging for a namespace and its children using the default logger.
// It suppresses logging for the specified namespace path and all its descendants. Returns
// the default logger for method chaining. Thread-safe via the Logger’s mutex.
// Example:
//
//	ll.NamespaceDisable("app/db")
//	ll.Clone().Namespace("db").Info("Query") // No output
func NamespaceDisable(path string) *Logger {
	return defaultLogger.NamespaceDisable(path)
}

// Info logs a message at Info level using the default logger.
// It formats the message using the provided format string and arguments, then delegates to
// defaultLogger’s Info method. The log is processed through the logger’s middleware pipeline,
// which may reject it based on errors. Thread-safe.
// Example:
//
//	ll.Info("Service started") // Output: [] INFO: Service started
func Info(format string, args ...any) {
	defaultLogger.Info(format, args...)
}

// Debug logs a message at Debug level using the default logger.
// It formats the message and delegates to defaultLogger’s Debug method. Used for debugging
// information, typically disabled in production. Thread-safe via the Logger’s mutex.
// Example:
//
//	ll.Level(lx.LevelDebug)
//	ll.Debug("Debugging") // Output: [] DEBUG: Debugging
func Debug(format string, args ...any) {
	defaultLogger.Debug(format, args...)
}

// Warn logs a message at Warn level using the default logger.
// It formats the message and delegates to defaultLogger’s Warn method. Used for warning
// conditions that do not halt execution. Thread-safe.
// Example:
//
//	ll.Warn("Low memory") // Output: [] WARN: Low memory
func Warn(format string, args ...any) {
	defaultLogger.Warn(format, args...)
}

// Error logs a message at Error level using the default logger.
// It formats the message and delegates to defaultLogger’s Error method. Used for error
// conditions requiring attention. Thread-safe.
// Example:
//
//	ll.Error("Database failure") // Output: [] ERROR: Database failure
func Error(format string, args ...any) {
	defaultLogger.Error(format, args...)
}

// Stack logs a message at Error level with a stack trace using the default logger.
// It formats the message and delegates to defaultLogger’s Stack method, including a stack
// trace for debugging. Thread-safe.
// Example:
//
//	ll.Stack("Critical error") // Output: [] ERROR: Critical error [stack=...]
func Stack(format string, args ...any) {
	defaultLogger.Stack(format, args...)
}

// Measure is a benchmarking helper that measures and returns the duration of a function’s execution.
// It logs the duration at Info level with a "duration" field using defaultLogger. The function
// is executed once, and the elapsed time is returned. Thread-safe via the Logger’s mutex.
// Example:
//
//	duration := ll.Measure(func() { time.Sleep(time.Millisecond) })
//	// Output: [] INFO: function executed [duration=1.002ms]
func Measure(fn func()) time.Duration {
	start := time.Now()
	fn()
	duration := time.Since(start)
	defaultLogger.Fields("duration", duration).Info("function executed")
	return duration
}

// Benchmark logs the duration since a start time at Info level.
// It calculates the time elapsed since the provided start time and logs it with "start",
// "end", and "duration" fields using defaultLogger. Thread-safe.
// Example:
//
//	start := time.Now()
//	time.Sleep(time.Millisecond)
//	ll.Benchmark(start) // Output: [] INFO: benchmark [start=... end=... duration=...]
func Benchmark(start time.Time) {
	defaultLogger.Fields("start", start, "end", time.Now(), "duration", time.Now().Sub(start)).Info("benchmark")
}

// Timed logs the duration of a function’s execution at Info level.
// It measures the time taken to execute the provided function and logs it with "start",
// "end", and "duration" fields using defaultLogger. Thread-safe.
// Example:
//
//	ll.Timed(func() { time.Sleep(time.Millisecond) })
//	// Output: [] INFO: timed [start=... end=... duration=...]
func Timed(fn func()) {
	start := time.Now()
	fn()
	defaultLogger.Fields("start", start, "end", time.Now(), "duration", time.Now().Sub(start)).Info("timed")
}

// Clone returns a new logger with the same configuration as the default logger.
// It creates a copy of defaultLogger’s settings (level, style, namespaces, etc.) but with
// an independent context, allowing customization without affecting the global logger.
// Thread-safe via the Logger’s Clone method.
// Example:
//
//	logger := ll.Clone().Namespace("sub")
//	logger.Info("Sub-logger") // Output: [sub] INFO: Sub-logger
func Clone() *Logger {
	return defaultLogger.Clone()
}

// Print logs a message at Info level without format specifiers using the default logger.
// It concatenates variadic arguments with spaces, minimizing allocations, and delegates
// to defaultLogger’s internal log method. Thread-safe. Used for simple, low-overhead logging.
// Example:
//
//	ll.Print("message", "value") // Output: [] INFO: message value
func Print(args ...any) {
	// Build the message by concatenating arguments with spaces
	var builder strings.Builder
	for i, arg := range args {
		if i > 0 {
			builder.WriteString(lx.Space)
		}
		builder.WriteString(fmt.Sprint(arg))
	}
	// Log at Info level without fields or stack trace
	defaultLogger.log(lx.LevelInfo, builder.String(), nil, false)
}
