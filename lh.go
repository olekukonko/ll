package ll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// MultiHandler combines multiple handlers to process log entries concurrently.
type MultiHandler struct {
	Handlers []Handler // List of handlers to process each log entry
}

// NewMultiHandler creates a new MultiHandler with the specified handlers.
// It accepts a variadic list of handlers to be executed in order.
func NewMultiHandler(h ...Handler) *MultiHandler {
	return &MultiHandler{
		Handlers: h,
	}
}

// Handle implements the Handler interface, calling Handle on each handler in sequence.
// It collects any errors from handlers and combines them into a single error using errors.Join.
func (h *MultiHandler) Handle(e *Entry) error {
	var errs []error
	for i, handler := range h.Handlers {
		// Execute each handler and capture any error with its index
		if err := handler.Handle(e); err != nil {
			errs = append(errs, fmt.Errorf("handler %d: %w", i, err))
		}
	}
	// Return a combined error if any handlers failed, or nil if all succeeded
	return errors.Join(errs...)
}

// TextHandler is a simple handler that outputs logs in plain text format.
type TextHandler struct {
	writer io.Writer // Destination for log output
}

// NewTextHandler creates a new TextHandler that writes to the specified writer.
func NewTextHandler(w io.Writer) *TextHandler {
	return &TextHandler{writer: w}
}

// Handle implements the Handler interface, formatting the log entry as plain text.
// It includes the namespace, level, message, and fields, using the specified style (FlatPath or NestedPath).
func (h *TextHandler) Handle(e *Entry) error {
	var sb strings.Builder
	// Format namespace if present
	if e.Namespace != "" {
		if e.style == NestedPath {
			// Split namespace into parts and format as [parent] -> [child]
			parts := strings.Split(e.Namespace, "/")
			for i, p := range parts {
				if i > 0 {
					sb.WriteString(" -> ")
				}
				sb.WriteString(fmt.Sprintf("[%s]", p))
			}
			sb.WriteString(" : ")
		} else {
			// Format as [parent/child]
			sb.WriteString(fmt.Sprintf("[%s] ", e.Namespace))
		}
	}
	// Add level and message
	sb.WriteString(fmt.Sprintf("%s: %s", e.Level.String(), e.Message))
	// Add sorted fields if present
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteString(" [")
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(fmt.Sprintf("%s=%v", k, e.Fields[k]))
		}
		sb.WriteString("]")
	}
	sb.WriteString("\n")
	// Write the formatted string to the writer
	_, err := h.writer.Write([]byte(sb.String()))
	return err
}

// ColorizedHandler outputs logs with ANSI color codes for enhanced terminal readability.
type ColorizedHandler struct {
	writer io.Writer // Destination for log output
}

// NewColorizedHandler creates a new ColorizedHandler that writes to the specified writer.
func NewColorizedHandler(w io.Writer) *ColorizedHandler {
	return &ColorizedHandler{writer: w}
}

// Handle implements the Handler interface, formatting the log entry with ANSI colors.
// Namespaces are gray, levels are color-coded (e.g., Debug=cyan, Error=red), and fields have blue keys.
func (h *ColorizedHandler) Handle(e *Entry) error {
	var sb strings.Builder
	// Format namespace in gray if present
	if e.Namespace != "" {
		if e.style == NestedPath {
			// Split namespace into parts and format as [parent] -> [child]
			parts := strings.Split(e.Namespace, "/")
			for i, p := range parts {
				if i > 0 {
					sb.WriteString(" -> ")
				}
				sb.WriteString(fmt.Sprintf("\033[90m[%s]\033[0m", p))
			}
			sb.WriteString(" : ")
		} else {
			// Format as [parent/child]
			sb.WriteString(fmt.Sprintf("\033[90m[%s]\033[0m ", e.Namespace))
		}
	}
	// Format level with appropriate color
	var levelStr string
	switch e.Level {
	case LevelDebug:
		levelStr = "\033[36mDEBUG\033[0m" // Cyan
	case LevelInfo:
		levelStr = "\033[32mINFO\033[0m" // Green
	case LevelWarn:
		levelStr = "\033[33mWARN\033[0m" // Yellow
	case LevelError:
		levelStr = "\033[31mERROR\033[0m" // Red
	default:
		levelStr = e.Level.String()
	}
	sb.WriteString(fmt.Sprintf("%s: %s", levelStr, e.Message))
	// Add sorted fields with blue keys if present
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteString(" [")
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(fmt.Sprintf("\033[34m%s\033[0m=%v", k, e.Fields[k]))
		}
		sb.WriteString("]")
	}
	sb.WriteString("\n")
	// Write the formatted string to the writer
	_, err := h.writer.Write([]byte(sb.String()))
	return err
}

// JSONHandler outputs logs in JSON format for structured logging.
type JSONHandler struct {
	writer  io.Writer // Destination for log output
	timeFmt string    // Timestamp format (e.g., RFC3339Nano)
}

// NewJSONHandler creates a new JSONHandler with the specified writer and optional timestamp format.
// If timeFmt is empty, it defaults to time.RFC3339Nano.
func NewJSONHandler(w io.Writer, timeFmt string) *JSONHandler {
	if timeFmt == "" {
		timeFmt = time.RFC3339Nano
	}
	return &JSONHandler{writer: w, timeFmt: timeFmt}
}

// Handle implements the Handler interface, serializing the log entry to JSON.
// It includes timestamp, level, message, namespace, and fields in the output.
func (h *JSONHandler) Handle(e *Entry) error {
	// Create a map with standard log fields
	entry := map[string]interface{}{
		"timestamp": e.Timestamp.Format(h.timeFmt),
		"level":     e.Level.String(),
		"message":   e.Message,
		"namespace": e.Namespace,
	}
	// Add custom fields
	for k, v := range e.Fields {
		entry[k] = v
	}
	// Marshal to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	// Append newline for readability
	data = append(data, '\n')
	// Write the JSON data to the writer
	_, err = h.writer.Write(data)
	return err
}

// SlogHandler adapts the standard library’s slog.Handler for compatibility with this package.
type SlogHandler struct {
	slogHandler slog.Handler // Underlying slog handler
}

// NewSlogHandler creates a new SlogHandler wrapping the provided slog.Handler.
func NewSlogHandler(h slog.Handler) *SlogHandler {
	return &SlogHandler{slogHandler: h}
}

// Handle implements the Handler interface, converting the log entry to an slog.Record.
// It maps entry fields to slog attributes and delegates to the underlying slog.Handler.
func (h *SlogHandler) Handle(e *Entry) error {
	// Create attributes for standard fields
	attrs := make([]slog.Attr, 0, len(e.Fields)+3)
	attrs = append(attrs,
		slog.String("timestamp", e.Timestamp.Format(time.RFC3339Nano)),
		slog.String("level", e.Level.String()),
		slog.String("namespace", e.Namespace),
	)
	// Add custom fields as attributes
	for k, v := range e.Fields {
		attrs = append(attrs, slog.Any(k, v))
	}
	// Create an slog.Record with the entry’s timestamp, level, and message
	record := slog.NewRecord(e.Timestamp, slog.Level(e.Level), e.Message, 0)
	record.AddAttrs(attrs...)
	// Delegate to the underlying slog.Handler
	return h.slogHandler.Handle(context.Background(), record)
}
