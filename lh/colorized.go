package lh

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/olekukonko/ll/lx"
)

// ColorIntensity defines the intensity level for ANSI colors
type ColorIntensity int

const (
	IntensityNormal ColorIntensity = iota
	IntensityBright
	IntensityPastel
	IntensityVibrant
)

// Palette defines ANSI color codes for various log components.
type Palette struct {
	Header    string // Color for stack trace header and dump separators
	Goroutine string // Color for goroutine lines in stack traces
	Func      string // Color for function names in stack traces
	Path      string // Color for file paths in stack traces
	FileLine  string // Color for file line numbers
	Reset     string // Reset code to clear color formatting
	Pos       string // Color for position in hex dumps
	Hex       string // Color for hex values in dumps
	Ascii     string // Color for ASCII values in dumps
	Debug     string // Color for Debug level messages
	Info      string // Color for Info level messages
	Warn      string // Color for Warn level messages
	Error     string // Color for Error level messages
	Fatal     string // Color for Fatal level messages
	Title     string // Color for dump titles (BEGIN/END separators)

	// Field type colors
	Key     string // Color for field keys
	Number  string // Color for numbers
	String  string // Color for strings
	Bool    string // Color for booleans
	Time    string // Color for timestamps/durations
	Nil     string // Color for nil values
	Default string // Default color for unknown types

	// JSON and Inspect specific colors
	JSONKey      string // Color for JSON keys
	JSONString   string // Color for JSON string values
	JSONNumber   string // Color for JSON number values
	JSONBool     string // Color for JSON boolean values
	JSONNull     string // Color for JSON null values
	JSONBrace    string // Color for JSON braces and brackets
	InspectKey   string // Color for inspect keys
	InspectValue string // Color for inspect values
	InspectMeta  string // Color for inspect metadata (annotations)
}

// darkPalette defines colors optimized for dark terminal backgrounds.
var darkPalette = Palette{
	Header:    "\033[1;31m",
	Goroutine: "\033[1;36m",
	Func:      "\033[97m",
	Path:      "\033[38;5;245m",
	FileLine:  "\033[38;5;111m",
	Reset:     "\033[0m",
	Title:     "\033[38;5;245m",
	Pos:       "\033[38;5;117m",
	Hex:       "\033[38;5;156m",
	Ascii:     "\033[38;5;224m",
	Debug:     "\033[36m",
	Info:      "\033[32m",
	Warn:      "\033[33m",
	Error:     "\033[31m",
	Fatal:     "\033[1;31m",

	// Field type colors
	Key:     "\033[38;5;117m",
	Number:  "\033[38;5;141m",
	String:  "\033[38;5;223m",
	Bool:    "\033[38;5;85m",
	Time:    "\033[38;5;110m",
	Nil:     "\033[38;5;243m",
	Default: "\033[38;5;250m",

	// JSON and Inspect colors
	JSONKey:      "\033[38;5;117m",
	JSONString:   "\033[38;5;223m",
	JSONNumber:   "\033[38;5;141m",
	JSONBool:     "\033[38;5;85m",
	JSONNull:     "\033[38;5;243m",
	JSONBrace:    "\033[38;5;245m",
	InspectKey:   "\033[38;5;117m",
	InspectValue: "\033[38;5;223m",
	InspectMeta:  "\033[38;5;243m",
}

// lightPalette defines colors optimized for light terminal backgrounds.
var lightPalette = Palette{
	Header:    "\033[1;31m",
	Goroutine: "\033[34m",
	Func:      "\033[30m",
	Path:      "\033[90m",
	FileLine:  "\033[94m",
	Reset:     "\033[0m",
	Title:     "\033[38;5;245m",
	Pos:       "\033[38;5;117m",
	Hex:       "\033[38;5;156m",
	Ascii:     "\033[38;5;224m",
	Debug:     "\033[36m",
	Info:      "\033[32m",
	Warn:      "\033[33m",
	Error:     "\033[31m",
	Fatal:     "\033[1;31m",

	Key:     "\033[34m",
	Number:  "\033[35m",
	String:  "\033[38;5;94m",
	Bool:    "\033[32m",
	Time:    "\033[38;5;24m",
	Nil:     "\033[38;5;240m",
	Default: "\033[30m",

	JSONKey:      "\033[34m",
	JSONString:   "\033[38;5;94m",
	JSONNumber:   "\033[35m",
	JSONBool:     "\033[32m",
	JSONNull:     "\033[38;5;240m",
	JSONBrace:    "\033[38;5;240m",
	InspectKey:   "\033[34m",
	InspectValue: "\033[38;5;94m",
	InspectMeta:  "\033[38;5;240m",
}

// brightPalette defines vibrant, high-contrast colors
var brightPalette = Palette{
	Header:    "\033[1;91m",
	Goroutine: "\033[1;96m",
	Func:      "\033[1;97m",
	Path:      "\033[38;5;250m",
	FileLine:  "\033[38;5;117m",
	Reset:     "\033[0m",
	Title:     "\033[1;37m",
	Pos:       "\033[1;33m",
	Hex:       "\033[1;32m",
	Ascii:     "\033[1;35m",
	Debug:     "\033[1;36m",
	Info:      "\033[1;32m",
	Warn:      "\033[1;33m",
	Error:     "\033[1;31m",
	Fatal:     "\033[1;91m",

	Key:     "\033[1;34m",
	Number:  "\033[1;35m",
	String:  "\033[1;33m",
	Bool:    "\033[1;32m",
	Time:    "\033[1;36m",
	Nil:     "\033[1;37m",
	Default: "\033[1;37m",

	JSONKey:      "\033[1;34m",
	JSONString:   "\033[1;33m",
	JSONNumber:   "\033[1;35m",
	JSONBool:     "\033[1;32m",
	JSONNull:     "\033[1;37m",
	JSONBrace:    "\033[1;37m",
	InspectKey:   "\033[1;34m",
	InspectValue: "\033[1;33m",
	InspectMeta:  "\033[1;37m",
}

// pastelPalette defines soft, pastel colors
var pastelPalette = Palette{
	Header:    "\033[38;5;211m",
	Goroutine: "\033[38;5;153m",
	Func:      "\033[38;5;255m",
	Path:      "\033[38;5;248m",
	FileLine:  "\033[38;5;111m",
	Reset:     "\033[0m",
	Title:     "\033[38;5;248m",
	Pos:       "\033[38;5;153m",
	Hex:       "\033[38;5;158m",
	Ascii:     "\033[38;5;218m",
	Debug:     "\033[38;5;122m",
	Info:      "\033[38;5;120m",
	Warn:      "\033[38;5;221m",
	Error:     "\033[38;5;211m",
	Fatal:     "\033[38;5;204m",

	Key:     "\033[38;5;153m",
	Number:  "\033[38;5;183m",
	String:  "\033[38;5;223m",
	Bool:    "\033[38;5;120m",
	Time:    "\033[38;5;117m",
	Nil:     "\033[38;5;247m",
	Default: "\033[38;5;250m",

	JSONKey:      "\033[38;5;153m",
	JSONString:   "\033[38;5;223m",
	JSONNumber:   "\033[38;5;183m",
	JSONBool:     "\033[38;5;120m",
	JSONNull:     "\033[38;5;247m",
	JSONBrace:    "\033[38;5;247m",
	InspectKey:   "\033[38;5;153m",
	InspectValue: "\033[38;5;223m",
	InspectMeta:  "\033[38;5;247m",
}

// vibrantPalette defines highly saturated, eye-catching colors
var vibrantPalette = Palette{
	Header:    "\033[38;5;196m",
	Goroutine: "\033[38;5;51m",
	Func:      "\033[38;5;15m",
	Path:      "\033[38;5;244m",
	FileLine:  "\033[38;5;75m",
	Reset:     "\033[0m",
	Title:     "\033[38;5;244m",
	Pos:       "\033[38;5;51m",
	Hex:       "\033[38;5;46m",
	Ascii:     "\033[38;5;201m",
	Debug:     "\033[38;5;51m",
	Info:      "\033[38;5;46m",
	Warn:      "\033[38;5;226m",
	Error:     "\033[38;5;196m",
	Fatal:     "\033[1;38;5;196m",

	Key:     "\033[38;5;33m",
	Number:  "\033[38;5;129m",
	String:  "\033[38;5;214m",
	Bool:    "\033[38;5;46m",
	Time:    "\033[38;5;75m",
	Nil:     "\033[38;5;242m",
	Default: "\033[38;5;15m",

	JSONKey:      "\033[38;5;33m",
	JSONString:   "\033[38;5;214m",
	JSONNumber:   "\033[38;5;129m",
	JSONBool:     "\033[38;5;46m",
	JSONNull:     "\033[38;5;242m",
	JSONBrace:    "\033[38;5;242m",
	InspectKey:   "\033[38;5;33m",
	InspectValue: "\033[38;5;214m",
	InspectMeta:  "\033[38;5;242m",
}

// noColorPalette defines a palette with empty strings for environments without color support
var noColorPalette = Palette{
	Header: "", Goroutine: "", Func: "", Path: "", FileLine: "", Reset: "",
	Title: "", Pos: "", Hex: "", Ascii: "", Debug: "", Info: "", Warn: "", Error: "", Fatal: "",
	Key: "", Number: "", String: "", Bool: "", Time: "", Nil: "", Default: "",
	JSONKey: "", JSONString: "", JSONNumber: "", JSONBool: "", JSONNull: "", JSONBrace: "",
	InspectKey: "", InspectValue: "", InspectMeta: "",
}

// builderPool is a pool of strings.Builder instances to reduce allocations
var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

// ColorizedHandler is a handler that outputs log entries with ANSI color codes.
type ColorizedHandler struct {
	w           io.Writer
	palette     Palette
	showTime    bool
	timeFormat  string
	mu          sync.Mutex
	noColor     bool           // Whether to disable colors entirely
	intensity   ColorIntensity // Color intensity level
	colorFields bool           // Whether to colorize fields (default: true)
}

// ColorOption defines a configuration function for ColorizedHandler.
type ColorOption func(*ColorizedHandler)

// WithColorPallet sets the color palette for the ColorizedHandler.
func WithColorPallet(pallet Palette) ColorOption {
	return func(c *ColorizedHandler) {
		c.palette = pallet
	}
}

// WithColorNone disables all color output.
func WithColorNone() ColorOption {
	return func(c *ColorizedHandler) {
		c.noColor = true
		c.colorFields = false // Also disable field coloring
	}
}

// WithColorField enables or disables field coloring specifically.
// This is useful for performance optimization or when field colors are too much.
// Example:
//
//	handler := NewColorizedHandler(os.Stdout, WithColorField(false)) // Disable field coloring only
func WithColorField(enable bool) ColorOption {
	return func(c *ColorizedHandler) {
		c.colorFields = enable
	}
}

// WithColorShowTime enables or disables the display of timestamps.
func WithColorShowTime(show bool) ColorOption {
	return func(c *ColorizedHandler) {
		c.showTime = show
	}
}

// WithColorIntensity sets the color intensity for the ColorizedHandler.
func WithColorIntensity(intensity ColorIntensity) ColorOption {
	return func(c *ColorizedHandler) {
		c.intensity = intensity
	}
}

// NewColorizedHandler creates a new ColorizedHandler writing to the specified writer.
func NewColorizedHandler(w io.Writer, opts ...ColorOption) *ColorizedHandler {
	c := &ColorizedHandler{
		w:           w,
		showTime:    false,
		timeFormat:  time.RFC3339,
		noColor:     false,
		intensity:   IntensityNormal,
		colorFields: true, // Default: enable field coloring
	}

	for _, opt := range opts {
		opt(c)
	}

	c.palette = c.detectPalette()
	return c
}

// Handle processes a log entry and writes it with ANSI color codes.
func (h *ColorizedHandler) Handle(e *lx.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch e.Class {
	case lx.ClassDump:
		return h.handleDumpOutput(e)
	case lx.ClassJSON:
		return h.handleJSONOutput(e)
	case lx.ClassInspect:
		return h.handleInspectOutput(e)
	case lx.ClassRaw:
		_, err := h.w.Write([]byte(e.Message))
		return err
	default:
		return h.handleRegularOutput(e)
	}
}

// Timestamped enables or disables timestamp display.
func (h *ColorizedHandler) Timestamped(enable bool, format ...string) {
	h.showTime = enable
	if len(format) > 0 && format[0] != "" {
		h.timeFormat = format[0]
	}
}

// handleRegularOutput handles normal log entries.
func (h *ColorizedHandler) handleRegularOutput(e *lx.Entry) error {
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	if h.showTime {
		builder.WriteString(e.Timestamp.Format(h.timeFormat))
		builder.WriteString(lx.Space)
	}

	h.formatNamespace(builder, e)
	h.formatLevel(builder, e)
	builder.WriteString(e.Message)
	h.formatFields(builder, e)

	if len(e.Stack) > 0 {
		h.formatStack(builder, e.Stack)
	}

	if e.Level != lx.LevelNone {
		builder.WriteString(lx.Newline)
	}

	_, err := h.w.Write([]byte(builder.String()))
	return err
}

// handleJSONOutput handles JSON log entries.
func (h *ColorizedHandler) handleJSONOutput(e *lx.Entry) error {
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	if h.showTime {
		builder.WriteString(e.Timestamp.Format(h.timeFormat))
		builder.WriteString(lx.Newline)
	}

	if e.Namespace != "" {
		h.formatNamespace(builder, e)
		h.formatLevel(builder, e)
	}

	h.colorizeJSON(builder, e.Message)
	builder.WriteString(lx.Newline)

	_, err := h.w.Write([]byte(builder.String()))
	return err
}

// handleInspectOutput handles inspect log entries.
func (h *ColorizedHandler) handleInspectOutput(e *lx.Entry) error {
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	if h.showTime {
		builder.WriteString(e.Timestamp.Format(h.timeFormat))
		builder.WriteString(lx.Space)
	}

	h.formatNamespace(builder, e)
	h.formatLevel(builder, e)
	h.colorizeInspect(builder, e.Message)
	builder.WriteString(lx.Newline)

	_, err := h.w.Write([]byte(builder.String()))
	return err
}

// colorizeJSON applies syntax highlighting to JSON strings without changing formatting
func (h *ColorizedHandler) colorizeJSON(b *strings.Builder, jsonStr string) {
	inString := false
	escapeNext := false

	for i := 0; i < len(jsonStr); i++ {
		ch := jsonStr[i]

		if escapeNext {
			b.WriteByte(ch)
			escapeNext = false
			continue
		}

		switch ch {
		case '\\':
			escapeNext = true
			if inString {
				b.WriteString(h.palette.JSONString)
			}
			b.WriteByte(ch)

		case '"':
			if inString {
				// End of string
				b.WriteString(h.palette.JSONString)
				b.WriteByte(ch)
				b.WriteString(h.palette.Reset)
				inString = false
			} else {
				// Start of string
				inString = true
				b.WriteString(h.palette.JSONString)
				b.WriteByte(ch)
			}

		case ':':
			if !inString {
				b.WriteString(h.palette.JSONBrace)
				b.WriteByte(ch)
				b.WriteString(h.palette.Reset)
			} else {
				b.WriteByte(ch)
			}

		case '{', '}', '[', ']', ',':
			if !inString {
				b.WriteString(h.palette.JSONBrace)
				b.WriteByte(ch)
				b.WriteString(h.palette.Reset)
			} else {
				b.WriteByte(ch)
			}

		default:
			if !inString {
				// Check for numbers, booleans, null
				remaining := jsonStr[i:]

				// Check for null
				if len(remaining) >= 4 && strings.HasPrefix(remaining, "null") {
					b.WriteString(h.palette.JSONNull)
					b.WriteString("null")
					b.WriteString(h.palette.Reset)
					i += 3 // Skip "null"
				} else if len(remaining) >= 4 && strings.HasPrefix(remaining, "true") {
					b.WriteString(h.palette.JSONBool)
					b.WriteString("true")
					b.WriteString(h.palette.Reset)
					i += 3 // Skip "true"
				} else if len(remaining) >= 5 && strings.HasPrefix(remaining, "false") {
					b.WriteString(h.palette.JSONBool)
					b.WriteString("false")
					b.WriteString(h.palette.Reset)
					i += 4 // Skip "false"
				} else if (ch >= '0' && ch <= '9') || ch == '-' || ch == '.' {
					b.WriteString(h.palette.JSONNumber)
					b.WriteByte(ch)
					// Continue writing digits
					for j := i + 1; j < len(jsonStr); j++ {
						nextCh := jsonStr[j]
						if (nextCh >= '0' && nextCh <= '9') || nextCh == '.' || nextCh == 'e' || nextCh == 'E' || nextCh == '+' || nextCh == '-' {
							b.WriteByte(nextCh)
							i = j
						} else {
							break
						}
					}
					b.WriteString(h.palette.Reset)
				} else if ch == ' ' || ch == '\n' || ch == '\t' || ch == '\r' {
					// Preserve whitespace exactly as is
					b.WriteByte(ch)
				} else {
					// Unexpected character outside string - preserve it
					b.WriteByte(ch)
				}
			} else {
				// Inside string
				b.WriteByte(ch)
			}
		}
	}
}

// colorizeInspect applies syntax highlighting to inspect output
func (h *ColorizedHandler) colorizeInspect(b *strings.Builder, inspectStr string) {
	lines := strings.Split(inspectStr, "\n")

	for lineIdx, line := range lines {
		if lineIdx > 0 {
			b.WriteString("\n")
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			b.WriteString(line)
			continue
		}

		// For inspect output, we'll do simple line-based coloring
		// This preserves the original formatting
		inString := false
		escapeNext := false

		for i := 0; i < len(line); i++ {
			ch := line[i]

			if escapeNext {
				b.WriteByte(ch)
				escapeNext = false
				continue
			}

			if ch == '\\' {
				escapeNext = true
				b.WriteByte(ch)
				continue
			}

			if ch == '"' {
				inString = !inString
				if inString {
					// Check if this is a metadata key
					if i+1 < len(line) && line[i+1] == '(' {
						b.WriteString(h.palette.InspectMeta)
					} else if i+2 < len(line) && line[i+1] == '*' && line[i+2] == '(' {
						b.WriteString(h.palette.InspectMeta)
					} else {
						b.WriteString(h.palette.InspectKey)
					}
				}
				b.WriteByte(ch)
				if !inString {
					b.WriteString(h.palette.Reset)
				}
				continue
			}

			if inString {
				// Inside a string key or value
				b.WriteByte(ch)
			} else {
				// Outside strings
				if ch == ':' {
					b.WriteString(h.palette.JSONBrace)
					b.WriteByte(ch)
					b.WriteString(h.palette.Reset)
				} else if ch == '{' || ch == '}' || ch == '[' || ch == ']' || ch == ',' {
					b.WriteString(h.palette.JSONBrace)
					b.WriteByte(ch)
					b.WriteString(h.palette.Reset)
				} else {
					// Check for numbers, booleans, null outside strings
					remaining := line[i:]

					if len(remaining) >= 4 && strings.HasPrefix(remaining, "null") {
						b.WriteString(h.palette.JSONNull)
						b.WriteString("null")
						b.WriteString(h.palette.Reset)
						i += 3
					} else if len(remaining) >= 4 && strings.HasPrefix(remaining, "true") {
						b.WriteString(h.palette.JSONBool)
						b.WriteString("true")
						b.WriteString(h.palette.Reset)
						i += 3
					} else if len(remaining) >= 5 && strings.HasPrefix(remaining, "false") {
						b.WriteString(h.palette.JSONBool)
						b.WriteString("false")
						b.WriteString(h.palette.Reset)
						i += 4
					} else if (ch >= '0' && ch <= '9') || ch == '-' {
						b.WriteString(h.palette.InspectValue)
						b.WriteByte(ch)
						// Continue writing digits
						for j := i + 1; j < len(line); j++ {
							nextCh := line[j]
							if (nextCh >= '0' && nextCh <= '9') || nextCh == '.' {
								b.WriteByte(nextCh)
								i = j
							} else {
								break
							}
						}
						b.WriteString(h.palette.Reset)
					} else {
						b.WriteByte(ch)
					}
				}
			}
		}
	}
}

// formatNamespace formats the namespace with ANSI color codes.
func (h *ColorizedHandler) formatNamespace(b *strings.Builder, e *lx.Entry) {
	if e.Namespace == "" {
		return
	}

	b.WriteString(lx.LeftBracket)
	switch e.Style {
	case lx.NestedPath:
		parts := strings.Split(e.Namespace, lx.Slash)
		for i, part := range parts {
			b.WriteString(part)
			b.WriteString(lx.RightBracket)
			if i < len(parts)-1 {
				b.WriteString(lx.Arrow)
				b.WriteString(lx.LeftBracket)
			}
		}
	default:
		b.WriteString(e.Namespace)
		b.WriteString(lx.RightBracket)
	}
	b.WriteString(lx.Colon)
	b.WriteString(lx.Space)
}

// formatLevel formats the log level with ANSI color codes.
func (h *ColorizedHandler) formatLevel(b *strings.Builder, e *lx.Entry) {
	color := map[lx.LevelType]string{
		lx.LevelDebug: h.palette.Debug,
		lx.LevelInfo:  h.palette.Info,
		lx.LevelWarn:  h.palette.Warn,
		lx.LevelError: h.palette.Error,
		lx.LevelFatal: h.palette.Fatal,
	}[e.Level]

	b.WriteString(color)
	b.WriteString(rightPad(e.Level.String(), 5))
	b.WriteString(h.palette.Reset)
	b.WriteString(lx.Colon)
	b.WriteString(lx.Space)
}

// formatFields formats the log entry's fields in sorted order.
func (h *ColorizedHandler) formatFields(b *strings.Builder, e *lx.Entry) {
	if len(e.Fields) == 0 {
		return
	}

	b.WriteString(lx.Space)
	b.WriteString(lx.LeftBracket)

	for i, pair := range e.Fields {
		if i > 0 {
			b.WriteString(lx.Space)
		}

		if h.colorFields {
			// Color the key
			b.WriteString(h.palette.Key)
			b.WriteString(pair.Key)
			b.WriteString(h.palette.Reset)
			b.WriteString("=")

			// Format value with type-based coloring
			h.formatFieldValue(b, pair.Value)
		} else {
			// No field coloring - just write plain text
			b.WriteString(pair.Key)
			b.WriteString("=")
			fmt.Fprint(b, pair.Value)
		}
	}

	b.WriteString(lx.RightBracket)
}

// formatFieldValue formats a field value with type-based ANSI color codes.
func (h *ColorizedHandler) formatFieldValue(b *strings.Builder, value interface{}) {
	// If field coloring is disabled, just write the value
	if !h.colorFields {
		fmt.Fprint(b, value)
		return
	}

	switch v := value.(type) {
	case time.Time:
		b.WriteString(h.palette.Time)
		b.WriteString(v.Format("2006-01-02 15:04:05"))
		b.WriteString(h.palette.Reset)

	case time.Duration:
		b.WriteString(h.palette.Time)
		h.formatDuration(b, v)
		b.WriteString(h.palette.Reset)

	case error:
		b.WriteString(h.palette.Error)
		b.WriteString(`"`)
		b.WriteString(v.Error())
		b.WriteString(`"`)
		b.WriteString(h.palette.Reset)

	case int, int8, int16, int32, int64:
		b.WriteString(h.palette.Number)
		fmt.Fprint(b, v)
		b.WriteString(h.palette.Reset)

	case uint, uint8, uint16, uint32, uint64:
		b.WriteString(h.palette.Number)
		fmt.Fprint(b, v)
		b.WriteString(h.palette.Reset)

	case float32, float64:
		b.WriteString(h.palette.Number)
		switch f := v.(type) {
		case float32:
			fmt.Fprintf(b, "%.6g", f)
		case float64:
			fmt.Fprintf(b, "%.6g", f)
		}
		b.WriteString(h.palette.Reset)

	case string:
		b.WriteString(h.palette.String)
		b.WriteString(`"`)
		b.WriteString(v)
		b.WriteString(`"`)
		b.WriteString(h.palette.Reset)

	case bool:
		b.WriteString(h.palette.Bool)
		fmt.Fprint(b, v)
		b.WriteString(h.palette.Reset)

	case nil:
		b.WriteString(h.palette.Nil)
		b.WriteString("nil")
		b.WriteString(h.palette.Reset)

	default:
		b.WriteString(h.palette.Default)
		fmt.Fprint(b, v)
		b.WriteString(h.palette.Reset)
	}
}

// formatDuration formats a duration in a human-readable way
func (h *ColorizedHandler) formatDuration(b *strings.Builder, d time.Duration) {
	if d < time.Microsecond {
		b.WriteString(d.String())
	} else if d < time.Millisecond {
		fmt.Fprintf(b, "%.3fµs", float64(d)/float64(time.Microsecond))
	} else if d < time.Second {
		fmt.Fprintf(b, "%.3fms", float64(d)/float64(time.Millisecond))
	} else if d < time.Minute {
		fmt.Fprintf(b, "%.3fs", float64(d)/float64(time.Second))
	} else if d < time.Hour {
		minutes := d / time.Minute
		seconds := (d % time.Minute) / time.Second
		fmt.Fprintf(b, "%dm%.3fs", minutes, float64(seconds)/float64(time.Second))
	} else {
		hours := d / time.Hour
		minutes := (d % time.Hour) / time.Minute
		fmt.Fprintf(b, "%dh%dm", hours, minutes)
	}
}

// formatStack formats a stack trace with ANSI color codes.
func (h *ColorizedHandler) formatStack(b *strings.Builder, stack []byte) {
	b.WriteString("\n")
	b.WriteString(h.palette.Header)
	b.WriteString("[stack]")
	b.WriteString(h.palette.Reset)
	b.WriteString("\n")

	lines := strings.Split(string(stack), "\n")
	if len(lines) == 0 {
		return
	}

	b.WriteString("  ┌─ ")
	b.WriteString(h.palette.Goroutine)
	b.WriteString(lines[0])
	b.WriteString(h.palette.Reset)
	b.WriteString("\n")

	for i := 1; i < len(lines)-1; i += 2 {
		funcLine := strings.TrimSpace(lines[i])
		pathLine := strings.TrimSpace(lines[i+1])

		if funcLine != "" {
			b.WriteString("  │   ")
			b.WriteString(h.palette.Func)
			b.WriteString(funcLine)
			b.WriteString(h.palette.Reset)
			b.WriteString("\n")
		}
		if pathLine != "" {
			b.WriteString("  │   ")

			lastSlash := strings.LastIndex(pathLine, "/")
			goIndex := strings.Index(pathLine, ".go:")

			if lastSlash >= 0 && goIndex > lastSlash {
				prefix := pathLine[:lastSlash+1]
				suffix := pathLine[lastSlash+1:]

				b.WriteString(h.palette.Path)
				b.WriteString(prefix)
				b.WriteString(h.palette.Reset)

				b.WriteString(h.palette.Path)
				b.WriteString(suffix)
				b.WriteString(h.palette.Reset)
			} else {
				b.WriteString(h.palette.Path)
				b.WriteString(pathLine)
				b.WriteString(h.palette.Reset)
			}

			b.WriteString("\n")
		}
	}

	if len(lines)%2 == 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		b.WriteString("  │   ")
		b.WriteString(h.palette.Func)
		b.WriteString(strings.TrimSpace(lines[len(lines)-1]))
		b.WriteString(h.palette.Reset)
		b.WriteString("\n")
	}

	b.WriteString("  └\n")
}

// handleDumpOutput formats hex dump output with ANSI color codes.
func (h *ColorizedHandler) handleDumpOutput(e *lx.Entry) error {
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	if h.showTime {
		builder.WriteString(e.Timestamp.Format(h.timeFormat))
		builder.WriteString(lx.Newline)
	}

	builder.WriteString(h.palette.Title)
	builder.WriteString("---- BEGIN DUMP ----")
	builder.WriteString(h.palette.Reset)
	builder.WriteString("\n")

	lines := strings.Split(e.Message, "\n")
	length := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(line, "pos ") {
			parts := strings.SplitN(line, "hex:", 2)
			if len(parts) == 2 {
				builder.WriteString(h.palette.Pos)
				builder.WriteString(parts[0])
				builder.WriteString(h.palette.Reset)

				hexAscii := strings.SplitN(parts[1], "'", 2)
				builder.WriteString(h.palette.Hex)
				builder.WriteString("hex:")
				builder.WriteString(hexAscii[0])
				builder.WriteString(h.palette.Reset)

				if len(hexAscii) > 1 {
					builder.WriteString(h.palette.Ascii)
					builder.WriteString("'")
					builder.WriteString(hexAscii[1])
					builder.WriteString(h.palette.Reset)
				}
			}
		} else if strings.HasPrefix(line, "Dumping value of type:") {
			builder.WriteString(h.palette.Header)
			builder.WriteString(line)
			builder.WriteString(h.palette.Reset)
		} else {
			builder.WriteString(line)
		}

		if i < length-1 {
			builder.WriteString("\n")
		}
	}

	builder.WriteString(h.palette.Title)
	builder.WriteString("---- END DUMP ----")
	builder.WriteString(h.palette.Reset)
	builder.WriteString("\n")

	_, err := h.w.Write([]byte(builder.String()))
	return err
}

// detectPalette selects a color palette based on terminal environment variables.
func (h *ColorizedHandler) detectPalette() Palette {
	// If colors are explicitly disabled, return noColorPalette
	if h.noColor {
		return noColorPalette
	}

	// Check NO_COLOR environment variable (standard: https://no-color.org/)
	if os.Getenv("NO_COLOR") != "" {
		return noColorPalette
	}

	term := os.Getenv("TERM")
	if term == "dumb" || term == "" {
		if runtime.GOOS == "windows" && !h.isWindowsTerminalAnsiSupported() {
			return noColorPalette
		}
	}

	var basePalette Palette

	if bg, ok := os.LookupEnv("TERM_BACKGROUND"); ok {
		if bg == "light" {
			basePalette = lightPalette
		} else {
			basePalette = darkPalette
		}
	} else if fgBg, ok := os.LookupEnv("COLORFGBG"); ok {
		parts := strings.Split(fgBg, ";")
		if len(parts) >= 2 {
			bg := parts[len(parts)-1]

			bgInt, err := strconv.Atoi(bg)
			if err == nil {
				if bgInt >= 0 && bgInt <= 7 || bgInt == 15 {
					basePalette = lightPalette
				} else {
					basePalette = darkPalette
				}
			} else {
				basePalette = darkPalette
			}
		} else {
			basePalette = darkPalette
		}
	} else if style, ok := os.LookupEnv("AppleInterfaceStyle"); ok && strings.EqualFold(style, "dark") {
		basePalette = darkPalette
	} else {
		basePalette = darkPalette
	}

	return h.applyIntensity(basePalette)
}

// applyIntensity applies the intensity setting to a base palette
func (h *ColorizedHandler) applyIntensity(basePalette Palette) Palette {
	switch h.intensity {
	case IntensityNormal:
		return basePalette
	case IntensityBright:
		return brightPalette
	case IntensityPastel:
		return pastelPalette
	case IntensityVibrant:
		return vibrantPalette
	default:
		return basePalette
	}
}

// isWindowsTerminalAnsiSupported checks if the Windows terminal supports ANSI colors
func (h *ColorizedHandler) isWindowsTerminalAnsiSupported() bool {
	if runtime.GOOS != "windows" {
		return true
	}

	if os.Getenv("WT_SESSION") != "" {
		return true
	}

	if os.Getenv("ConEmuANSI") == "ON" {
		return true
	}

	if os.Getenv("ANSICON") != "" {
		return true
	}

	return false
}
