package ll

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// defaultEnabled defines the default logging state (disabled).
const (
	defaultEnabled = false
)

// Level represents the severity of a log message.
type Level int

// Log level constants, ordered by increasing severity.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String converts a Level to its string representation.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Style defines how namespace paths are formatted in log output.
type Style int

// Namespace style constants.
const (
	FlatPath   Style = iota // Formats as [parent/child]
	NestedPath              // Formats as [parent] -> [child]
)

// Entry represents a single log entry passed to handlers.
type Entry struct {
	Timestamp time.Time              // Time the log was created
	Level     Level                  // Severity level of the log
	Message   string                 // Log message content
	Namespace string                 // Namespace path (e.g., "parent/child")
	Fields    map[string]interface{} // Additional key-value metadata
	style     Style                  // Namespace formatting style
}

// Handler defines the interface for processing log entries.
type Handler interface {
	Handle(e *Entry) error // Processes a log entry, returning any error
}

// Logger is the core structure for logging, managing configuration and behavior.
type Logger struct {
	mu          sync.RWMutex           // Protects concurrent access to fields
	enabled     bool                   // Whether logging is enabled
	level       Level                  // Minimum level for logging
	namespaces  *namespaceStore        // Stores namespace enable/disable states
	currentPath string                 // Current namespace path (e.g., "parent/child")
	context     map[string]interface{} // Contextual fields added to all logs
	style       Style                  // Namespace formatting style
	handler     Handler                // Output handler for logs
	rateLimits  map[Level]*rateLimit   // Rate limits per log level
	sampleRates map[Level]float64      // Sampling rates per log level
	middleware  []func(*Entry) bool    // Middleware functions to process entries
}

// namespaceStore manages namespace enable/disable states with a cache.
type namespaceStore struct {
	sync.Map          // Stores path -> bool (enabled/disabled)
	cache    sync.Map // Stores path -> bool (cached enabled state)
}

// defaultStore is the shared namespace store for all loggers.
var defaultStore = &namespaceStore{}

// rateLimit tracks rate-limiting state for a log level.
type rateLimit struct {
	count    int           // Current number of logs in the interval
	maxCount int           // Maximum allowed logs per interval
	interval time.Duration // Time window for rate limiting
	last     time.Time     // Time of the last log
	mu       sync.Mutex    // Protects concurrent access
}

// FieldBuilder enables fluent addition of fields before logging.
type FieldBuilder struct {
	logger *Logger                // Associated logger
	fields map[string]interface{} // Fields to include in the log
}

// defaultLogger is the global logger instance for package-level functions.
var defaultLogger = &Logger{
	enabled:     defaultEnabled,
	level:       LevelDebug,
	namespaces:  defaultStore,
	context:     make(map[string]interface{}),
	style:       FlatPath,
	handler:     nil,
	rateLimits:  make(map[Level]*rateLimit),
	sampleRates: make(map[Level]float64),
	middleware:  make([]func(*Entry) bool, 0),
}

// New creates a new logger instance with the specified namespace.
// The logger is initialized with a default TextHandler writing to os.Stdout.
func New(namespace string) *Logger {
	return &Logger{
		enabled:     defaultEnabled,
		level:       LevelDebug,
		namespaces:  defaultStore,
		currentPath: namespace,
		context:     make(map[string]interface{}),
		style:       FlatPath,
		handler:     NewTextHandler(os.Stdout),
		rateLimits:  make(map[Level]*rateLimit),
		sampleRates: make(map[Level]float64),
		middleware:  make([]func(*Entry) bool, 0),
	}
}

// Namespace creates a child logger with a sub-namespace appended to the current path.
// The child inherits the parent’s configuration but has its own context.
func (l *Logger) Namespace(name string) *Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()

	fullPath := name
	if l.currentPath != "" {
		fullPath = l.currentPath + "/" + name
	}

	return &Logger{
		enabled:     l.enabled,
		level:       l.level,
		namespaces:  l.namespaces,
		currentPath: fullPath,
		context:     make(map[string]interface{}),
		style:       l.style,
		handler:     l.handler,
		rateLimits:  l.rateLimits,
		sampleRates: l.sampleRates,
		middleware:  l.middleware,
	}
}

// WithContext creates a new logger with additional contextual fields.
// Existing context fields are preserved, and new fields are added.
func (l *Logger) WithContext(fields map[string]interface{}) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newLogger := &Logger{
		enabled:     l.enabled,
		level:       l.level,
		namespaces:  l.namespaces,
		currentPath: l.currentPath,
		context:     make(map[string]interface{}),
		style:       l.style,
		handler:     l.handler,
		rateLimits:  l.rateLimits,
		sampleRates: l.sampleRates,
		middleware:  l.middleware,
	}

	for k, v := range l.context {
		newLogger.context[k] = v
	}
	for k, v := range fields {
		newLogger.context[k] = v
	}

	return newLogger
}

// SetHandler sets the handler for processing log entries.
func (l *Logger) SetHandler(handler Handler) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handler = handler
}

// SetLevel sets the minimum log level required for logging.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Enable activates logging for the logger.
func (l *Logger) Enable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = true
}

// Disable deactivates logging for the logger.
func (l *Logger) Disable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = false
}

// SetStyle sets the namespace formatting style (FlatPath or NestedPath).
func (l *Logger) SetStyle(style Style) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.style = style
}

// NamespaceEnable enables logging for a namespace and its children.
// Invalidates the namespace cache to ensure updated state.
func (l *Logger) NamespaceEnable(path string) {
	fmt.Printf("NamespaceEnable: storing %s=true\n", path)
	l.namespaces.Store(path, true)
	// Invalidate cache for this path and its children
	l.namespaces.cache.Delete(path)
	l.namespaces.cache.Range(func(key, _ interface{}) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, path+"/") {
			l.namespaces.cache.Delete(k)
		}
		return true
	})
}

// NamespaceDisable disables logging for a namespace and its children.
// Invalidates the namespace cache to ensure updated state.
func (l *Logger) NamespaceDisable(path string) {
	fmt.Printf("NamespaceDisable: storing %s=false\n", path)
	l.namespaces.Store(path, false)
	// Invalidate cache for this path and its children
	l.namespaces.cache.Delete(path)
	l.namespaces.cache.Range(func(key, _ interface{}) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, path+"/") {
			l.namespaces.cache.Delete(k)
		}
		return true
	})
}

// SetRateLimit configures rate limiting for a log level, allowing a maximum number of logs within an interval.
func (l *Logger) SetRateLimit(level Level, count int, interval time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rateLimits[level] = &rateLimit{
		count:    0,
		maxCount: count,
		interval: interval,
		last:     time.Now(),
	}
}

// SetSampling sets a sampling rate (0.0 to 1.0) for a log level, where 0.0 suppresses all logs and 1.0 allows all.
func (l *Logger) SetSampling(level Level, rate float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sampleRates[level] = rate
}

// Use adds a middleware function to process log entries before they are handled.
// Middleware returns true to allow the log or false to skip it.
func (l *Logger) Use(middleware func(*Entry) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.middleware = append(l.middleware, middleware)
}

// Fields starts a fluent chain for adding fields using variadic key-value pairs.
// Non-string keys or uneven pairs generate an error field.
func (l *Logger) Fields(pairs ...any) *FieldBuilder {
	fb := &FieldBuilder{logger: l, fields: make(map[string]interface{})}
	for i := 0; i < len(pairs)-1; i += 2 {
		if key, ok := pairs[i].(string); ok {
			fb.fields[key] = pairs[i+1]
		} else {
			fb.fields["error"] = fmt.Errorf("non-string key in Fields: %v", pairs[i])
		}
	}
	if len(pairs)%2 != 0 {
		fb.fields["error"] = fmt.Errorf("uneven key-value pairs in Fields: [%v]", pairs[len(pairs)-1])
	}
	return fb
}

// Field starts a fluent chain for adding fields from a map.
func (l *Logger) Field(fields map[string]interface{}) *FieldBuilder {
	fb := &FieldBuilder{logger: l, fields: make(map[string]interface{})}
	for k, v := range fields {
		fb.fields[k] = v
	}
	return fb
}

// log is the internal method for processing a log entry.
// It applies rate limiting, sampling, middleware, and context before passing to the handler.
func (l *Logger) log(level Level, msg string, fields map[string]interface{}, withStack bool) {
	if !l.shouldLog(level) {
		return
	}

	if withStack {
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		if fields == nil {
			fields = make(map[string]interface{})
		}
		fields["stack"] = string(buf[:n])
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	entry := &Entry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
		Namespace: l.currentPath,
		Fields:    fields,
		style:     l.style,
	}

	if len(l.context) > 0 {
		if entry.Fields == nil {
			entry.Fields = make(map[string]interface{})
		}
		for k, v := range l.context {
			if _, exists := entry.Fields[k]; !exists {
				entry.Fields[k] = v
			}
		}
	}

	// Apply middleware in order, stopping if any returns false
	for _, mw := range l.middleware {
		if !mw(entry) {
			return
		}
	}

	if l.handler != nil {
		_ = l.handler.Handle(entry)
	}
}

// shouldLog determines if a log should be emitted based on enabled state, level, namespaces, sampling, and rate limits.
func (l *Logger) shouldLog(level Level) bool {
	// Check if logger is disabled or level is below minimum
	if !l.enabled || level < l.level {
		return false
	}

	// Check namespace hierarchy for enable/disable state
	enabled := true
	if l.currentPath != "" {
		// Try cache first for performance
		if cached, ok := l.namespaces.cache.Load(l.currentPath); ok {
			enabled = cached.(bool)
		} else {
			// Compute enabled state by checking all parent namespaces
			parts := strings.Split(l.currentPath, "/")
			for i := 1; i <= len(parts); i++ {
				path := strings.Join(parts[:i], "/")
				if val, ok := l.namespaces.Load(path); ok {
					if !val.(bool) {
						enabled = false
						break
					}
				}
			}
			// Cache the result to avoid repeated checks
			l.namespaces.cache.Store(l.currentPath, enabled)
		}
	}
	if !enabled {
		return false
	}

	// Check sampling rate (0.0 = never log, 1.0 = always log)
	if rate, ok := l.sampleRates[level]; ok {
		if rand.Float64() > rate {
			return false
		}
	}

	// Check rate limiting
	if limit, ok := l.rateLimits[level]; ok {
		limit.mu.Lock()
		defer limit.mu.Unlock()
		now := time.Now()
		// Reset count if interval has elapsed
		if now.Sub(limit.last) >= limit.interval {
			limit.last = now
			limit.count = 0
		}
		// Block if maximum count is reached
		if limit.count >= limit.maxCount {
			return false
		}
		// Increment count for this log
		limit.count++
		return true
	}

	return true
}

// Info logs a message at Info level.
func (l *Logger) Info(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelInfo, msg, nil, false)
}

// Timed logs the duration of a function execution at Info level.
func (l *Logger) Timed(fn func()) {
	start := time.Now()
	fn()
	l.Fields("start", start, "end", time.Now(), "duration", time.Now().Sub(start))
	l.log(LevelInfo, "timed", nil, false)
}

// Benchmark logs the duration since a start time at Info level.
func (l *Logger) Benchmark(start time.Time) {
	l.Fields("start", start, "end", time.Now(), "duration", time.Now().Sub(start))
	l.log(LevelInfo, "benchmark", nil, false)
}

// Debug logs a message at Debug level.
func (l *Logger) Debug(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelDebug, msg, nil, false)
}

// Warn logs a message at Warn level.
func (l *Logger) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelWarn, msg, nil, false)
}

// Error logs a message at Error level.
func (l *Logger) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelError, msg, nil, false)
}

// Stack logs a message at Error level with a stack trace.
func (l *Logger) Stack(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelError, msg, nil, true)
}

// FieldBuilder logging methods

// Info logs a message at Info level with the builder’s fields.
func (fb *FieldBuilder) Info(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelInfo, msg, fb.fields, false)
}

// Debug logs a message at Debug level with the builder’s fields.
func (fb *FieldBuilder) Debug(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelDebug, msg, fb.fields, false)
}

// Warn logs a message at Warn level with the builder’s fields.
func (fb *FieldBuilder) Warn(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelWarn, msg, fb.fields, false)
}

// Error logs a message at Error level with the builder’s fields.
func (fb *FieldBuilder) Error(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelError, msg, fb.fields, false)
}

// Stack logs a message at Error level with a stack trace and the builder’s fields.
func (fb *FieldBuilder) Stack(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelError, msg, fb.fields, true)
}

// Global functions for defaultLogger

// SetHandler sets the handler for the default logger.
func SetHandler(handler Handler) {
	defaultLogger.SetHandler(handler)
}

// SetLevel sets the minimum log level for the default logger.
func SetLevel(level Level) {
	defaultLogger.SetLevel(level)
}

// Enable activates logging for the default logger.
func Enable() {
	defaultLogger.Enable()
}

// Disable deactivates logging for the default logger.
func Disable() {
	defaultLogger.Disable()
}

// SetStyle sets the namespace style for the default logger.
func SetStyle(style Style) {
	defaultLogger.SetStyle(style)
}

// EnableNamespace enables logging for a namespace and its children using the default logger.
func EnableNamespace(path string) {
	defaultLogger.NamespaceEnable(path)
}

// DisableNamespace disables logging for a namespace and its children using the default logger.
func DisableNamespace(path string) {
	defaultLogger.NamespaceDisable(path)
}

// SetRateLimit configures rate limiting for a log level on the default logger.
func SetRateLimit(level Level, count int, interval time.Duration) {
	defaultLogger.SetRateLimit(level, count, interval)
}

// SetSampling sets a sampling rate for a log level on the default logger.
func SetSampling(level Level, rate float64) {
	defaultLogger.SetSampling(level, rate)
}

// Info logs a message at Info level using the default logger.
func Info(format string, args ...any) {
	defaultLogger.Info(format, args...)
}

// Debug logs a message at Debug level using the default logger.
func Debug(format string, args ...any) {
	defaultLogger.Debug(format, args...)
}

// Warn logs a message at Warn level using the default logger.
func Warn(format string, args ...any) {
	defaultLogger.Warn(format, args...)
}

// Error logs a message at Error level using the default logger.
func Error(format string, args ...any) {
	defaultLogger.Error(format, args...)
}

// Stack logs a message at Error level with a stack trace using the default logger.
func Stack(format string, args ...any) {
	defaultLogger.Stack(format, args...)
}

// Conditional enables conditional logging based on a boolean condition.
type Conditional struct {
	logger    *Logger // Associated logger
	condition bool    // Whether logging is allowed
}

// If creates a conditional logger that logs only if the condition is true.
func (l *Logger) If(condition bool) *Conditional {
	return &Conditional{logger: l, condition: condition}
}

// Fields starts a fluent chain for adding fields using variadic key-value pairs, if condition is true.
func (cl *Conditional) Fields(pairs ...any) *FieldBuilder {
	if !cl.condition {
		return &FieldBuilder{logger: cl.logger, fields: nil}
	}
	return cl.logger.Fields(pairs...)
}

// Field starts a fluent chain for adding fields from a map, if condition is true.
func (cl *Conditional) Field(fields map[string]interface{}) *FieldBuilder {
	if !cl.condition {
		return &FieldBuilder{logger: cl.logger, fields: nil}
	}
	return cl.logger.Field(fields)
}

// Info logs a message at Info level if the condition is true.
func (cl *Conditional) Info(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Info(format, args...)
}

// Debug logs a message at Debug level if the condition is true.
func (cl *Conditional) Debug(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Debug(format, args...)
}

// Warn logs a message at Warn level if the condition is true.
func (cl *Conditional) Warn(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Warn(format, args...)
}

// Error logs a message at Error level if the condition is true.
func (cl *Conditional) Error(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Error(format, args...)
}

// Stack logs a message at Error level with a stack trace if the condition is true.
func (cl *Conditional) Stack(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Stack(format, args...)
}
