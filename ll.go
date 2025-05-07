package ll

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Logging constants
const (
	defaultEnabled = false
)

// Level represents log levels
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

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

// Style defines how namespaces are displayed
type Style int

const (
	FlatPath   Style = iota // [parent/child]
	NestedPath              // [parent] -> [child]
)

// Entry represents a log entry, passed to handlers
type Entry struct {
	Timestamp time.Time
	Level     Level
	Message   string
	Namespace string
	Fields    map[string]interface{}
	style     Style
}

// Handler is the interface for log output
type Handler interface {
	Handle(e *Entry) error
}

// Logger is the main logging structure
type Logger struct {
	mu          sync.RWMutex
	enabled     bool
	level       Level
	namespaces  sync.Map
	currentPath string
	context     map[string]interface{}
	style       Style
	handler     Handler
	rateLimits  map[Level]*rateLimit
	sampleRates map[Level]float64
}

type rateLimit struct {
	count    int
	maxCount int
	interval time.Duration
	last     time.Time
	mu       sync.Mutex
}

// FieldBuilder is used for fluent field addition before logging
type FieldBuilder struct {
	logger *Logger
	fields map[string]interface{}
}

var defaultLogger = &Logger{
	enabled:     defaultEnabled,
	level:       LevelDebug,
	namespaces:  sync.Map{},
	context:     make(map[string]interface{}),
	style:       FlatPath,
	rateLimits:  make(map[Level]*rateLimit),
	sampleRates: make(map[Level]float64),
}

// New creates a new logger instance
func New(namespace string) *Logger {
	return defaultLogger.Namespace(namespace)
}

// Namespace creates a new child logger with the given namespace
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
	}
}

// WithContext adds contextual fields to the logger
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
	}

	for k, v := range l.context {
		newLogger.context[k] = v
	}
	for k, v := range fields {
		newLogger.context[k] = v
	}

	return newLogger
}

// SetHandler sets the log handler
func (l *Logger) SetHandler(handler Handler) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handler = handler
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Enable turns on logging
func (l *Logger) Enable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = true
}

// Disable turns off logging
func (l *Logger) Disable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = false
}

// SetStyle sets the namespace display style
func (l *Logger) SetStyle(style Style) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.style = style
}

// EnableNamespace enables logging for a specific namespace and its children
func (l *Logger) EnableNamespace(path string) {
	// fmt.Printf("EnableNamespace: storing %s=true\n", path)
	l.namespaces.Store(path, true)
}

// DisableNamespace disables logging for a specific namespace and its children
func (l *Logger) DisableNamespace(path string) {
	// fmt.Printf("DisableNamespace: storing %s=false\n", path)
	l.namespaces.Store(path, false)
}

// SetRateLimit sets rate limiting for a log level
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

// SetSampling sets sampling rate for a log level
func (l *Logger) SetSampling(level Level, rate float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sampleRates[level] = rate
}

// Fields starts a fluent chain for adding fields
func (l *Logger) Fields(key string, value any, moreKeysAndValues ...any) *FieldBuilder {
	fb := &FieldBuilder{
		logger: l,
		fields: make(map[string]interface{}),
	}
	fb.fields[key] = value
	if len(moreKeysAndValues)%2 != 0 {
		fb.fields["error"] = fmt.Errorf("uneven key-value pairs in Fields: %v", moreKeysAndValues)
		return fb
	}
	for i := 0; i < len(moreKeysAndValues); i += 2 {
		k, ok := moreKeysAndValues[i].(string)
		if !ok {
			fb.fields["error"] = fmt.Errorf("non-string key in Fields: %v", moreKeysAndValues[i])
			continue
		}
		v := moreKeysAndValues[i+1]
		fb.fields[k] = v
	}
	return fb
}

// log is the internal logging method
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

	if l.handler != nil {
		// fmt.Printf("log: calling handler with message=%s\n", msg)
		_ = l.handler.Handle(entry)
	}
}

// shouldLog checks if a log should be emitted
func (l *Logger) shouldLog(level Level) bool {
	// Debug logging to trace conditions
	// fmt.Printf("shouldLog: enabled=%v, level=%v, requiredLevel=%v, path=%s\n", l.enabled, level, l.level, l.currentPath)

	if !l.enabled || level < l.level {
		return false
	}

	if l.currentPath != "" {
		// Check namespace and its parents
		parts := strings.Split(l.currentPath, "/")
		for i := len(parts); i > 0; i-- {
			path := strings.Join(parts[:i], "/")
			if enabled, ok := l.namespaces.Load(path); ok && !enabled.(bool) {
				// fmt.Printf("shouldLog: namespace %s disabled\n", path)
				return false
			}
			// fmt.Printf("shouldLog: namespace %s not found or enabled\n", path)
		}
	}

	if rate, ok := l.sampleRates[level]; ok {
		if rand.Float64() > rate {
			return false
		}
	}

	if limit, ok := l.rateLimits[level]; ok {
		limit.mu.Lock()
		defer limit.mu.Unlock()

		now := time.Now()
		if now.Sub(limit.last) < limit.interval {
			limit.count++
			if limit.count > limit.maxCount {
				return false
			}
		} else {
			limit.last = now
			limit.count = 0
		}
	}

	return true
}

// Info logs a message at Info level
func (l *Logger) Info(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelInfo, msg, nil, false)
}

// Debug logs a message at Debug level
func (l *Logger) Debug(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelDebug, msg, nil, false)
}

// Warn logs a message at Warn level
func (l *Logger) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelWarn, msg, nil, false)
}

// Error logs a message at Error level
func (l *Logger) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelError, msg, nil, false)
}

// Stack logs a message at Error level with a stack trace
func (l *Logger) Stack(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.log(LevelError, msg, nil, true)
}

// FieldBuilder logging methods
func (fb *FieldBuilder) Info(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelInfo, msg, fb.fields, false)
}

func (fb *FieldBuilder) Debug(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelDebug, msg, fb.fields, false)
}

func (fb *FieldBuilder) Warn(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelWarn, msg, fb.fields, false)
}

func (fb *FieldBuilder) Error(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelError, msg, fb.fields, false)
}

func (fb *FieldBuilder) Stack(format string, args ...any) {
	if fb.fields == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fb.logger.log(LevelError, msg, fb.fields, true)
}

// Global functions
func SetHandler(handler Handler) {
	defaultLogger.SetHandler(handler)
}

func SetLevel(level Level) {
	defaultLogger.SetLevel(level)
}

func Enable() {
	defaultLogger.Enable()
}

func Disable() {
	defaultLogger.Disable()
}

func SetStyle(style Style) {
	defaultLogger.SetStyle(style)
}

func EnableNamespace(path string) {
	defaultLogger.EnableNamespace(path)
}

func DisableNamespace(path string) {
	defaultLogger.DisableNamespace(path)
}

func SetRateLimit(level Level, count int, interval time.Duration) {
	defaultLogger.SetRateLimit(level, count, interval)
}

func SetSampling(level Level, rate float64) {
	defaultLogger.SetSampling(level, rate)
}

func Info(format string, args ...any) {
	defaultLogger.Info(format, args...)
}

func Debug(format string, args ...any) {
	defaultLogger.Debug(format, args...)
}

func Warn(format string, args ...any) {
	defaultLogger.Warn(format, args...)
}

func Error(format string, args ...any) {
	defaultLogger.Error(format, args...)
}

func Stack(format string, args ...any) {
	defaultLogger.Stack(format, args...)
}

// Conditional for conditional logging
type Conditional struct {
	logger    *Logger
	condition bool
}

func (l *Logger) If(condition bool) *Conditional {
	return &Conditional{
		logger:    l,
		condition: condition,
	}
}

func (cl *Conditional) Fields(key string, value any, moreKeysAndValues ...any) *FieldBuilder {
	if !cl.condition {
		return &FieldBuilder{logger: cl.logger, fields: nil}
	}
	return cl.logger.Fields(key, value, moreKeysAndValues...)
}

func (cl *Conditional) Info(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Info(format, args...)
}

func (cl *Conditional) Debug(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Debug(format, args...)
}

func (cl *Conditional) Warn(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Warn(format, args...)
}

func (cl *Conditional) Error(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Error(format, args...)
}

func (cl *Conditional) Stack(format string, args ...any) {
	if !cl.condition {
		return
	}
	cl.logger.Stack(format, args...)
}
