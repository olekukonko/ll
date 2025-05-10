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

// isDumpOutput detects if the message contains dump formatting
func isDumpOutput(msg string) bool {
	return strings.Contains(msg, "pos ") && strings.Contains(msg, "hex:")
}

// Handle processes a log entry and writes it with ANSI color codes.
func (h *ColorizedHandler) Handle(e *lx.Entry) error {
	// Special handling for dump output
	if isDumpOutput(e.Message) {
		return h.handleDumpOutput(e)
	}
	return h.handleRegularOutput(e)
}

// handleRegularOutput handles normal log entries
func (h *ColorizedHandler) handleRegularOutput(e *lx.Entry) error {
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
	if e.Level != lx.LevelNone {
		builder.WriteString(lx.Newline)
	}

	// Write to output
	_, err := h.w.Write([]byte(builder.String()))
	return err
}

// ColorizedHandler's handleDumpOutput (clean version)
func (h *ColorizedHandler) handleDumpOutput(e *lx.Entry) error {
	lines := strings.Split(e.Message, "\n")
	var builder strings.Builder

	// Color scheme remains unchanged
	posColor := "\033[38;5;117m"
	hexColor := "\033[38;5;156m"
	asciiColor := "\033[38;5;224m"
	reset := "\033[0m"

	builder.WriteString("---- BEGIN DUMP ----\n")
	total := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(line, "pos ") {
			parts := strings.SplitN(line, "hex:", 2)
			if len(parts) != 2 {
				continue
			}

			builder.WriteString(posColor)
			builder.WriteString(parts[0])
			builder.WriteString(reset)

			hexAscii := strings.SplitN(parts[1], "'", 2)
			builder.WriteString(hexColor)
			builder.WriteString("hex:")
			builder.WriteString(hexAscii[0])
			builder.WriteString(reset)

			if len(hexAscii) > 1 {
				builder.WriteString(asciiColor)
				builder.WriteString("'")
				builder.WriteString(hexAscii[1])
				builder.WriteString(reset)
			}
		} else if strings.HasPrefix(line, "Dumping value of type:") {
			builder.WriteString("\033[1;35m")
			builder.WriteString(line)
			builder.WriteString(reset)
		} else {
			builder.WriteString(line)
		}

		if i < total-1 {
			builder.WriteString(lx.Newline)
		}
	}
	builder.WriteString("---- END DUMP ----\n")
	_, err := h.w.Write([]byte(builder.String()))
	return err
}
