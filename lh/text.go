package lh

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/olekukonko/ll/lx"
)

type TextOption func(*TextHandler)

var textBufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// textConfig is an immutable snapshot of a TextHandler's settings. It is
// republished (never mutated) whenever the configuration changes, which keeps
// the hot path lock-free.
type textConfig struct {
	writer     io.Writer
	showTime   bool
	timeFormat string
}

// WithTextTimeFormat enables timestamp display and optionally sets a custom time format.
// It configures the TextHandler to include temporal information in each log entry,
// allowing for precise tracking of when log events occur.
// If the format string is empty, it defaults to time.RFC3339.
func WithTextTimeFormat(format string) TextOption {
	return func(t *TextHandler) {
		t.Timestamped(true, format)
	}
}

// WithTextShowTime enables or disables timestamp display in log entries.
// This option provides direct control over the visibility of the time prefix
// without altering the underlying time format configured in the handler.
// Setting show to true will prepend timestamps to all subsequent regular log outputs.
func WithTextShowTime(show bool) TextOption {
	return func(t *TextHandler) {
		t.update(func(c *textConfig) { c.showTime = show })
	}
}

// TextHandler is a handler that outputs log entries as plain text.
// It formats log entries with namespace, level, message, fields, and optional stack traces,
// writing the result to the provided writer.
//
// Configuration is published through an atomic snapshot, so Handle never takes a
// configuration lock; only the final write is serialised (wmu) to keep entries
// from interleaving.
type TextHandler struct {
	cfg atomic.Pointer[textConfig]
	mu  sync.Mutex // serialises configuration updates
	wmu sync.Mutex // serialises writes so entries never interleave
}

// NewTextHandler creates a new TextHandler writing to the specified writer.
// It initializes the handler with the given writer, suitable for outputs like stdout or files.
// Example:
//
//	handler := NewTextHandler(os.Stdout)
//	logger := ll.New("app").Enable().Handler(handler)
//	logger.Info("Test") // Output: [app] INFO: Test
func NewTextHandler(w io.Writer, opts ...TextOption) *TextHandler {
	t := &TextHandler{}
	t.cfg.Store(&textConfig{
		writer:     w,
		showTime:   false,
		timeFormat: time.RFC3339,
	})

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// snapshot returns the current immutable configuration.
func (h *TextHandler) snapshot() *textConfig {
	if c := h.cfg.Load(); c != nil {
		return c
	}
	return &textConfig{timeFormat: time.RFC3339}
}

// update applies a mutation to a fresh copy of the configuration and publishes it.
func (h *TextHandler) update(fn func(*textConfig)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	next := *h.snapshot()
	fn(&next)
	h.cfg.Store(&next)
}

// Timestamped enables or disables timestamp display and optionally sets a custom time format.
// If format is empty, defaults to RFC3339.
// Example:
//
//	handler := NewTextHandler(os.Stdout).Timestamped(true, time.StampMilli)
//	// Output: Jan 02 15:04:05.000 [app] INFO: Test
func (h *TextHandler) Timestamped(enable bool, format ...string) {
	h.update(func(c *textConfig) {
		c.showTime = enable
		if len(format) > 0 && format[0] != "" {
			c.timeFormat = format[0]
		}
	})
}

// Output sets a new writer for the TextHandler.
// Thread-safe - safe for concurrent use.
func (h *TextHandler) Output(w io.Writer) {
	h.update(func(c *textConfig) { c.writer = w })
}

// Handle processes a log entry and writes it as plain text.
// It delegates to specialized methods based on the entry's class (Dump, Raw, or regular).
// Returns an error if writing to the underlying writer fails.
// Thread-safe if the writer is thread-safe.
// Example:
//
//	handler.Handle(&lx.Entry{Message: "test", Level: lx.LevelInfo}) // Writes "INFO: test"
func (h *TextHandler) Handle(e *lx.Entry) error {
	// Formatting happens outside any lock; only the final write is serialised.
	cfg := h.snapshot()
	if cfg.writer == nil {
		return nil
	}

	switch e.Class {
	case lx.ClassDump:
		return h.handleDumpOutput(cfg.writer, cfg.showTime, cfg.timeFormat, e)
	case lx.ClassRaw:
		return h.writeString(cfg.writer, e.Message)
	}

	return h.handleRegularOutput(cfg.writer, cfg.showTime, cfg.timeFormat, e)
}

// writeString writes s to w while holding the write lock, so concurrent entries
// never interleave within the same handler.
func (h *TextHandler) writeString(w io.Writer, s string) error {
	h.wmu.Lock()
	_, err := io.WriteString(w, s)
	h.wmu.Unlock()
	return err
}

// writeBytes writes b to w while holding the write lock.
func (h *TextHandler) writeBytes(w io.Writer, b []byte) error {
	h.wmu.Lock()
	_, err := w.Write(b)
	h.wmu.Unlock()
	return err
}

// handleRegularOutput handles normal log entries.
// It formats the entry with namespace, level, message, fields, and stack trace (if present),
// writing the result to the handler's writer.
// Returns an error if writing fails.
// Example (internal usage):
//
//	h.handleRegularOutput(&lx.Entry{Message: "test", Level: lx.LevelInfo}) // Writes "INFO: test"
func (h *TextHandler) handleRegularOutput(w io.Writer, showTime bool, timeFormat string, e *lx.Entry) error {
	buf := textBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer textBufPool.Put(buf)

	if showTime {
		buf.WriteString(e.Timestamp.Format(timeFormat))
		buf.WriteString(lx.Space)
	}

	// Caller location, when caller reporting is enabled on the logger.
	if e.Caller != "" {
		buf.WriteString(lx.LeftBracket)
		buf.WriteString(e.Caller)
		buf.WriteString(lx.RightBracket)
		buf.WriteString(lx.Space)
	}

	switch e.Style {
	case lx.NestedPath:
		if e.Namespace != "" {
			parts := strings.Split(e.Namespace, lx.Slash)
			for i, part := range parts {
				buf.WriteString(lx.LeftBracket)
				buf.WriteString(part)
				buf.WriteString(lx.RightBracket)
				if i < len(parts)-1 {
					buf.WriteString(lx.Arrow)
				}
			}
			buf.WriteString(lx.Colon)
			buf.WriteString(lx.Space)
		}
	default: // FlatPath
		if e.Namespace != "" {
			buf.WriteString(lx.LeftBracket)
			buf.WriteString(e.Namespace)
			buf.WriteString(lx.RightBracket)
			buf.WriteString(lx.Space)
		}
	}

	buf.WriteString(e.Level.Name(e.Class))
	// buf.WriteString(lx.Space)
	buf.WriteString(lx.Colon)
	buf.WriteString(lx.Space)
	buf.WriteString(e.Message)

	if len(e.Fields) > 0 {
		buf.WriteString(lx.Space)
		buf.WriteString(lx.LeftBracket)
		for i, pair := range e.Fields {
			if i > 0 {
				buf.WriteString(lx.Space)
			}
			buf.WriteString(pair.Key)
			buf.WriteString("=")
			writeFieldValue(buf, pair.Value)
		}
		buf.WriteString(lx.RightBracket)
	}

	if len(e.Stack) > 0 {
		h.formatStack(buf, e.Stack)
	}

	if e.Level != lx.LevelNone {
		buf.WriteString(lx.Newline)
	}

	return h.writeBytes(w, buf.Bytes())
}

// handleDumpOutput specially formats hex dump output (plain text version).
// It wraps the dump message with BEGIN/END separators for clarity.
// Returns an error if writing fails.
// Example (internal usage):
//
//	h.handleDumpOutput(&lx.Entry{Class: lx.ClassDump, Message: "pos 00 hex: 61"}) // Writes "---- BEGIN DUMP ----\npos 00 hex: 61\n---- END DUMP ----\n"
func (h *TextHandler) handleDumpOutput(w io.Writer, showTime bool, timeFormat string, e *lx.Entry) error {
	buf := textBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer textBufPool.Put(buf)

	if showTime {
		buf.WriteString(e.Timestamp.Format(timeFormat))
		buf.WriteString(lx.Newline)
	}

	buf.WriteString("---- BEGIN DUMP ----\n")
	buf.WriteString(e.Message)
	buf.WriteString("---- END DUMP ----\n\n")

	return h.writeBytes(w, buf.Bytes())
}

// formatStack formats a stack trace for plain text output.
// It structures the stack trace with indentation and separators for readability,
// including goroutine and function/file details.
// Example (internal usage):
//
//	h.formatStack(&builder, []byte("goroutine 1 [running]:\nmain.main()\n\tmain.go:10")) // Appends formatted stack trace
func (h *TextHandler) formatStack(b *bytes.Buffer, stack []byte) {
	lines := strings.Split(string(stack), "\n")
	if len(lines) == 0 {
		return
	}

	b.WriteString("\n[stack]\n")

	b.WriteString("  ┌─ ")
	b.WriteString(lines[0])
	b.WriteString("\n")

	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		if strings.Contains(line, ".go") {
			b.WriteString("  ├       ")
		} else {
			b.WriteString("  │   ")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("  └\n")
}
