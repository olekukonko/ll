package ll

import (
	"context"

	"github.com/olekukonko/ll/lx"
)

// WithHandler sets the handler for the logger as a functional option for configuring
// a new logger instance.
// Example:
//
//	logger := New("app", WithHandler(lh.NewJSONHandler(os.Stdout)))
func WithHandler(handler lx.Handler) Option {
	return func(l *Logger) {
		l.handler = handler
	}
}

// WithTimestamped returns an Option that configures timestamp settings for the logger's existing handler.
// It enables or disables timestamp logging and optionally sets the timestamp format if the handler
// supports the lx.Timestamper interface. If no handler is set, the function has no effect.
// Parameters:
//
//	enable: Boolean to enable or disable timestamp logging
//	format: Optional string(s) to specify the timestamp format
func WithTimestamped(enable bool, format ...string) Option {
	return func(l *Logger) {
		if l.handler != nil { // Check if a handler is set
			// Verify if the handler supports the lx.Timestamper interface
			if h, ok := l.handler.(lx.Timestamper); ok {
				h.Timestamped(enable, format...) // Apply timestamp settings to the handler
			}
		}
	}
}

// WithLevel sets the minimum log level for the logger as a functional option for
// configuring a new logger instance.
// Example:
//
//	logger := New("app", WithLevel(lx.LevelWarn))
func WithLevel(level lx.LevelType) Option {
	return func(l *Logger) {
		l.Level(level)
	}
}

// WithStyle sets the namespace formatting style for the logger as a functional option
// for configuring a new logger instance.
// Example:
//
//	logger := New("app", WithStyle(lx.NestedPath))
func WithStyle(style lx.StyleType) Option {
	return func(l *Logger) {
		l.style = style
	}
}

// Functional options (can be passed to New() or applied later)
func WithFatalExits(enabled bool) Option {
	return func(l *Logger) {
		l.fatalExits = enabled
	}
}

// WithFatalStack enables stack trace capture for Fatal entries.
func WithFatalStack(enabled bool) Option {
	return func(l *Logger) {
		l.fatalStack = enabled
	}
}

// WithCaller enables or disables caller reporting ("file.go:42") on every entry.
// It is off by default because capturing the caller costs a stack walk.
//
// When enabled, Text/Colorized handlers prefix the line with [file.go:42] and the
// JSON handler emits a "caller" field.
func WithCaller(enabled bool) Option {
	return func(l *Logger) {
		l.caller = enabled
	}
}

// WithOrderedLevels switches level filtering from the library's historic numeric
// ordering to severity ordering.
//
// Legacy (default): an entry is emitted when its LevelType value does not exceed
// the configured one. Because LevelDebug has the highest numeric value, a
// non-Debug threshold filters in a way most users do not expect (for example
// Level(LevelError) still emits Info and drops Fatal).
//
// Ordered: an entry is emitted when its severity is at least the configured
// severity, i.e. Level(LevelError) emits Error and Fatal only.
// Nothing changes unless this option is enabled.
func WithOrderedLevels(enabled bool) Option {
	return func(l *Logger) {
		l.ordered.Store(enabled)
	}
}

// WithContextExtractor registers a function that derives log fields from a
// context.Context. See Logger.Ctx.
//
// Example:
//
//	ll.New("app", ll.WithContextExtractor(func(ctx context.Context) []lx.Field {
//	    return []lx.Field{{Key: "trace_id", Value: traceID(ctx)}}
//	}))
func WithContextExtractor(fn func(ctx context.Context) []lx.Field) Option {
	return func(l *Logger) {
		l.mu.Lock()
		l.ctxFields = fn
		l.mu.Unlock()
	}
}
