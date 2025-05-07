package lh

import (
	"fmt"
	"github.com/olekukonko/ll/lx"
	"io"
	"sort"
	"strings"
)

// ColorizedHandler is a handler that outputs log entries with ANSI color codes.
type ColorizedHandler struct {
	w io.Writer
}

// NewColorizedHandler creates a new ColorizedHandler writing to the specified writer.
func NewColorizedHandler(w io.Writer) *ColorizedHandler {
	return &ColorizedHandler{w: w}
}

// Handle processes a log entry and writes it with ANSI color codes.
func (h *ColorizedHandler) Handle(e *lx.Entry) error {
	var builder strings.Builder

	// Namespace formatting
	switch e.Style {
	case lx.NestedPath:
		if e.Namespace != "" {
			parts := strings.Split(e.Namespace, lx.Slash)
			for i, part := range parts {
				builder.WriteString(lx.LeftBracket)
				builder.WriteString(part)
				builder.WriteString(lx.RightBracket)
				if i < len(parts)-1 {
					builder.WriteString(lx.Arrow)
				}
			}
			builder.WriteString(lx.Colon)
			builder.WriteString(lx.Space)
		}
	default: // FlatPath
		if e.Namespace != "" {
			builder.WriteString(lx.LeftBracket)
			builder.WriteString(e.Namespace)
			builder.WriteString(lx.RightBracket)
			builder.WriteString(lx.Space)
		}
	}

	// Colorized level
	color := map[lx.LevelType]string{
		lx.LevelDebug: "\033[36m", // Cyan
		lx.LevelInfo:  "\033[32m", // Green
		lx.LevelWarn:  "\033[33m", // Yellow
		lx.LevelError: "\033[31m", // Red
	}[e.Level]
	reset := "\033[0m"
	builder.WriteString(color)
	builder.WriteString(e.Level.String())
	builder.WriteString(reset)
	builder.WriteString(lx.Colon)
	builder.WriteString(lx.Space)

	// Message
	builder.WriteString(e.Message)

	// Fields
	if len(e.Fields) > 0 {
		var keys []string
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		builder.WriteString(lx.Space)
		builder.WriteString(lx.LeftBracket)
		for i, k := range keys {
			if i > 0 {
				builder.WriteString(lx.Space)
			}
			builder.WriteString(k)
			builder.WriteString("=")
			builder.WriteString(fmt.Sprint(e.Fields[k]))
		}
		builder.WriteString(lx.RightBracket)
	}

	// Newline
	builder.WriteString("\n")

	// Write to output
	_, err := h.w.Write([]byte(builder.String()))
	return err
}
