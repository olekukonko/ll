package lh

import (
	"fmt"
	"github.com/olekukonko/ll/lx"
	"io"
	"sort"
	"strings"
)

// TextHandler is a handler that outputs log entries as plain text.
type TextHandler struct {
	w io.Writer
}

// NewTextHandler creates a new TextHandler writing to the specified writer.
func NewTextHandler(w io.Writer) *TextHandler {
	return &TextHandler{w: w}
}

// Handle processes a log entry and writes it as plain text.
func (h *TextHandler) Handle(e *lx.Entry) error {
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

	// GetLevel
	builder.WriteString(e.Level.String())
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
