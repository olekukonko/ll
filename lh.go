package ll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fatih/color"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// MultiHandler combines multiple handlers
type MultiHandler struct {
	Handlers []Handler
}

// NewMultiHandler creates a new MultiHandler with the given handlers
func NewMultiHandler(h ...Handler) *MultiHandler {
	return &MultiHandler{
		Handlers: h,
	}
}

// Handle implements Handler, calling Handle on each handler and collecting errors
func (h *MultiHandler) Handle(e *Entry) error {
	var errs []error
	for i, handler := range h.Handlers {
		// fmt.Printf("MultiHandler: calling handler %d\n", i)
		if err := handler.Handle(e); err != nil {
			errs = append(errs, fmt.Errorf("handler %d: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

// TextHandler is a simple text-based handler
type TextHandler struct {
	writer io.Writer
}

func NewTextHandler(w io.Writer) *TextHandler {
	return &TextHandler{writer: w}
}

func (h *TextHandler) Handle(e *Entry) error {
	var buf bytes.Buffer

	if e.Namespace != "" {
		switch e.style {
		case NestedPath:
			parts := strings.Split(e.Namespace, "/")
			for i, p := range parts {
				if i > 0 {
					buf.WriteString(" -> ")
				}
				buf.WriteString(fmt.Sprintf("[%s]", p))
			}
			buf.WriteString(" : ")
		default:
			buf.WriteString(fmt.Sprintf("[%s] ", e.Namespace))
		}
	}

	buf.WriteString(fmt.Sprintf("%s: %s", e.Level.String(), e.Message))

	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf.WriteString(" [")
		for i, k := range keys {
			if i > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString(fmt.Sprintf("%s=%v", k, e.Fields[k]))
		}
		buf.WriteString("]")
	}
	buf.WriteString("\n")

	// fmt.Printf("TextHandler: writing %s to %v\n", buf.String(), h.writer)
	_, err := h.writer.Write(buf.Bytes())
	return err
}

// JSONHandler outputs logs in JSON format
type JSONHandler struct {
	writer io.Writer
}

func NewJSONHandler(w io.Writer) *JSONHandler {
	return &JSONHandler{writer: w}
}

func (h *JSONHandler) Handle(e *Entry) error {
	entry := map[string]interface{}{
		"timestamp": e.Timestamp.Format(time.RFC3339Nano),
		"level":     e.Level.String(),
		"message":   e.Message,
		"namespace": e.Namespace,
	}
	for k, v := range e.Fields {
		entry[k] = v
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = h.writer.Write(data)
	return err
}

// ColorizedHandler outputs logs with colors using fatih/color
type ColorizedHandler struct {
	writer io.Writer
}

func NewColorizedHandler(w io.Writer) *ColorizedHandler {
	return &ColorizedHandler{writer: w}
}

func (h *ColorizedHandler) Handle(e *Entry) error {
	var buf bytes.Buffer

	// Define colors using fatih/color
	namespaceColor := color.New(color.FgHiBlack) // Gray
	debugColor := color.New(color.FgCyan)
	infoColor := color.New(color.FgGreen)
	warnColor := color.New(color.FgYellow)
	errorColor := color.New(color.FgRed)
	keyColor := color.New(color.FgBlue)

	// Namespace
	if e.Namespace != "" {
		switch e.style {
		case NestedPath:
			parts := strings.Split(e.Namespace, "/")
			for i, p := range parts {
				if i > 0 {
					buf.WriteString(" -> ")
				}
				buf.WriteString(namespaceColor.Sprintf("[%s]", p))
			}
			buf.WriteString(" : ")
		default:
			buf.WriteString(namespaceColor.Sprintf("[%s] ", e.Namespace))
		}
	}

	// Level and message
	var levelColor *color.Color
	switch e.Level {
	case LevelDebug:
		levelColor = debugColor
	case LevelInfo:
		levelColor = infoColor
	case LevelWarn:
		levelColor = warnColor
	case LevelError:
		levelColor = errorColor
	default:
		levelColor = color.New(color.FgWhite)
	}
	buf.WriteString(levelColor.Sprintf("%s", e.Level.String()))
	buf.WriteString(": ")
	buf.WriteString(e.Message)

	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf.WriteString(" [")
		for i, k := range keys {
			if i > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString(keyColor.Sprintf("%s", k))
			buf.WriteString(fmt.Sprintf("=%v", e.Fields[k]))
		}
		buf.WriteString("]")
	}
	buf.WriteString("\n")

	_, err := h.writer.Write(buf.Bytes())
	return err
}

// SlogHandler adapts slog for compatibility
type SlogHandler struct {
	slogHandler slog.Handler
}

func NewSlogHandler(h slog.Handler) *SlogHandler {
	return &SlogHandler{slogHandler: h}
}

func (h *SlogHandler) Handle(e *Entry) error {
	attrs := make([]slog.Attr, 0, len(e.Fields)+3)
	attrs = append(attrs,
		slog.String("timestamp", e.Timestamp.Format(time.RFC3339Nano)),
		slog.String("level", e.Level.String()),
		slog.String("namespace", e.Namespace),
	)
	for k, v := range e.Fields {
		attrs = append(attrs, slog.Any(k, v))
	}

	record := slog.NewRecord(e.Timestamp, slog.Level(e.Level), e.Message, 0)
	record.AddAttrs(attrs...)
	return h.slogHandler.Handle(context.Background(), record)
}
