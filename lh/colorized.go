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
// It specifies colors for headers, goroutines, functions, paths, stack traces, and log levels,
// used by ColorizedHandler to format log output with color.
type Palette struct {
	Header    string // Color for stack trace header and dump separators
	Goroutine string // Color for goroutine lines in stack traces
	Func      string // Color for function names in stack traces
	Path      string // Color for file paths in stack traces
	FileLine  string // Color for file line numbers (not used in provided code)
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

	// New field type colors
	Key     string // Color for field keys
	Number  string // Color for numbers
	String  string // Color for strings
	Bool    string // Color for booleans
	Time    string // Color for timestamps/durations
	Nil     string // Color for nil values
	Default string // Default color for unknown types
}

// darkPalette defines colors optimized for dark terminal backgrounds.
// It uses bright, contrasting colors for readability on dark backgrounds.
var darkPalette = Palette{
	Header:    "\033[1;31m",     // Bold red for headers
	Goroutine: "\033[1;36m",     // Bold cyan for goroutines
	Func:      "\033[97m",       // Bright white for functions
	Path:      "\033[38;5;245m", // Light gray for paths
	FileLine:  "\033[38;5;111m", // Muted light blue (unused)
	Reset:     "\033[0m",        // Reset color formatting

	Title: "\033[38;5;245m", // Light gray for dump titles
	Pos:   "\033[38;5;117m", // Light blue for dump positions
	Hex:   "\033[38;5;156m", // Light green for hex values
	Ascii: "\033[38;5;224m", // Light pink for ASCII values

	Debug: "\033[36m",   // Cyan for Debug level
	Info:  "\033[32m",   // Green for Info level
	Warn:  "\033[33m",   // Yellow for Warn level
	Error: "\033[31m",   // Standard red
	Fatal: "\033[1;31m", // Bold red - stands out more

	// New colors for field types
	Key:     "\033[38;5;117m", // Light blue for keys
	Number:  "\033[38;5;141m", // Purple for numbers
	String:  "\033[38;5;223m", // Light orange for strings
	Bool:    "\033[38;5;85m",  // Green for booleans
	Time:    "\033[38;5;110m", // Blue for timestamps
	Nil:     "\033[38;5;243m", // Gray for nil
	Default: "\033[38;5;250m", // Default light gray
}

// lightPalette defines colors optimized for light terminal backgrounds.
// It uses darker colors for better contrast on light backgrounds.
var lightPalette = Palette{
	Header:    "\033[1;31m", // Same red for headers
	Goroutine: "\033[34m",   // Blue (darker for light bg)
	Func:      "\033[30m",   // Black text for functions
	Path:      "\033[90m",   // Dark gray for paths
	FileLine:  "\033[94m",   // Blue for file lines (unused)
	Reset:     "\033[0m",    // Reset color formatting

	Title: "\033[38;5;245m", // Light gray for dump titles
	Pos:   "\033[38;5;117m", // Light blue for dump positions
	Hex:   "\033[38;5;156m", // Light green for hex values
	Ascii: "\033[38;5;224m", // Light pink for ASCII values

	Debug: "\033[36m",   // Cyan for Debug level
	Info:  "\033[32m",   // Green for Info level
	Warn:  "\033[33m",   // Yellow for Warn level
	Error: "\033[31m",   // Standard red
	Fatal: "\033[1;31m", // Bold red - stands out more

	// New colors for field types (darker for light background)
	Key:     "\033[34m",       // Blue for keys
	Number:  "\033[35m",       // Magenta for numbers
	String:  "\033[38;5;94m",  // Dark orange/brown for strings
	Bool:    "\033[32m",       // Green for booleans
	Time:    "\033[38;5;24m",  // Dark blue for timestamps
	Nil:     "\033[38;5;240m", // Dark gray for nil
	Default: "\033[30m",       // Black for unknown types
}

// brightPalette defines vibrant, high-contrast colors
var brightPalette = Palette{
	Header:    "\033[1;91m",     // Bright bold red
	Goroutine: "\033[1;96m",     // Bright bold cyan
	Func:      "\033[1;97m",     // Bright bold white
	Path:      "\033[38;5;250m", // Bright gray
	FileLine:  "\033[38;5;117m", // Bright blue
	Reset:     "\033[0m",

	Title: "\033[1;37m", // Bright white
	Pos:   "\033[1;33m", // Bright yellow
	Hex:   "\033[1;32m", // Bright green
	Ascii: "\033[1;35m", // Bright magenta

	Debug: "\033[1;36m", // Bright cyan
	Info:  "\033[1;32m", // Bright green
	Warn:  "\033[1;33m", // Bright yellow
	Error: "\033[1;31m", // Bright red
	Fatal: "\033[1;91m", // Bright bold red

	// Bright field type colors
	Key:     "\033[1;34m", // Bright blue
	Number:  "\033[1;35m", // Bright magenta
	String:  "\033[1;33m", // Bright yellow
	Bool:    "\033[1;32m", // Bright green
	Time:    "\033[1;36m", // Bright cyan
	Nil:     "\033[1;37m", // Bright white
	Default: "\033[1;37m", // Bright white
}

// pastelPalette defines soft, pastel colors
var pastelPalette = Palette{
	Header:    "\033[38;5;211m", // Pastel red
	Goroutine: "\033[38;5;153m", // Pastel blue
	Func:      "\033[38;5;255m", // Off-white
	Path:      "\033[38;5;248m", // Light gray
	FileLine:  "\033[38;5;111m", // Pastel blue
	Reset:     "\033[0m",

	Title: "\033[38;5;248m", // Light gray
	Pos:   "\033[38;5;153m", // Pastel blue
	Hex:   "\033[38;5;158m", // Pastel green
	Ascii: "\033[38;5;218m", // Pastel pink

	Debug: "\033[38;5;122m", // Pastel cyan
	Info:  "\033[38;5;120m", // Pastel green
	Warn:  "\033[38;5;221m", // Pastel yellow
	Error: "\033[38;5;211m", // Pastel red
	Fatal: "\033[38;5;204m", // Brighter pastel red

	// Pastel field type colors
	Key:     "\033[38;5;153m", // Pastel blue
	Number:  "\033[38;5;183m", // Pastel purple
	String:  "\033[38;5;223m", // Pastel orange
	Bool:    "\033[38;5;120m", // Pastel green
	Time:    "\033[38;5;117m", // Pastel blue
	Nil:     "\033[38;5;247m", // Pastel gray
	Default: "\033[38;5;250m", // Pastel light gray
}

// vibrantPalette defines highly saturated, eye-catching colors
var vibrantPalette = Palette{
	Header:    "\033[38;5;196m", // Vivid red
	Goroutine: "\033[38;5;51m",  // Vivid cyan
	Func:      "\033[38;5;15m",  // Pure white
	Path:      "\033[38;5;244m", // Medium gray
	FileLine:  "\033[38;5;75m",  // Vivid blue
	Reset:     "\033[0m",

	Title: "\033[38;5;244m", // Medium gray
	Pos:   "\033[38;5;51m",  // Vivid cyan
	Hex:   "\033[38;5;46m",  // Vivid green
	Ascii: "\033[38;5;201m", // Vivid magenta

	Debug: "\033[38;5;51m",    // Vivid cyan
	Info:  "\033[38;5;46m",    // Vivid green
	Warn:  "\033[38;5;226m",   // Vivid yellow
	Error: "\033[38;5;196m",   // Vivid red
	Fatal: "\033[1;38;5;196m", // Bold vivid red

	// Vibrant field type colors
	Key:     "\033[38;5;33m",  // Vivid blue
	Number:  "\033[38;5;129m", // Vivid purple
	String:  "\033[38;5;214m", // Vivid orange
	Bool:    "\033[38;5;46m",  // Vivid green
	Time:    "\033[38;5;75m",  // Vivid blue
	Nil:     "\033[38;5;242m", // Dark gray
	Default: "\033[38;5;15m",  // White
}

// noColorPalette defines a palette with empty strings for environments without color support
var noColorPalette = Palette{
	Header:    "",
	Goroutine: "",
	Func:      "",
	Path:      "",
	FileLine:  "",
	Reset:     "",
	Title:     "",
	Pos:       "",
	Hex:       "",
	Ascii:     "",
	Debug:     "",
	Info:      "",
	Warn:      "",
	Error:     "",
	Fatal:     "",
	Key:       "",
	Number:    "",
	String:    "",
	Bool:      "",
	Time:      "",
	Nil:       "",
	Default:   "",
}

// builderPool is a pool of strings.Builder instances to reduce allocations
var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

// ColorizedHandler is a handler that outputs log entries with ANSI color codes.
// It formats log entries with colored namespace, level, message, fields, and stack traces,
// writing the result to the provided writer.
// Thread-safe if the underlying writer is thread-safe.
type ColorizedHandler struct {
	w          io.Writer // Destination for colored log output
	palette    Palette   // Color scheme for formatting
	showTime   bool      // Whether to display timestamps
	timeFormat string    // Format for timestamps (defaults to time.RFC3339)
	mu         sync.Mutex
	noColor    bool           // Whether to disable colors entirely
	intensity  ColorIntensity // Color intensity level
}

// ColorOption defines a configuration function for ColorizedHandler.
// It allows customization of the handler, such as setting the color palette.
type ColorOption func(*ColorizedHandler)

// WithColorPallet sets the color palette for the ColorizedHandler.
// It allows specifying a custom Palette for dark or light terminal backgrounds.
// Example:
//
//	handler := NewColorizedHandler(os.Stdout, WithColorPallet(lightPalette))
func WithColorPallet(pallet Palette) ColorOption {
	return func(c *ColorizedHandler) {
		c.palette = pallet
	}
}

// WithNoColor disables all color output regardless of terminal capabilities.
// This is useful for non-terminal outputs or when colors are not desired.
// Example:
//
//	handler := NewColorizedHandler(os.Stdout, WithNoColor())
func WithNoColor() ColorOption {
	return func(c *ColorizedHandler) {
		c.noColor = true
	}
}

// WithColorShowTime enables or disables the display of timestamps in colored log entries.
// It controls whether the ColorizedHandler prepends a time string to its output,
// providing temporal context while maintaining the handler's color formatting.
// Setting show to true activates the timestamp prefix using the current time format.
func WithColorShowTime(show bool) ColorOption {
	return func(c *ColorizedHandler) {
		c.showTime = show
	}
}

// WithColorIntensity sets the color intensity for the ColorizedHandler.
// It allows choosing between Normal, Bright, Pastel, or Vibrant color schemes.
// Example:
//
//	handler := NewColorizedHandler(os.Stdout, WithColorIntensity(IntensityBright))
func WithColorIntensity(intensity ColorIntensity) ColorOption {
	return func(c *ColorizedHandler) {
		c.intensity = intensity
	}
}

// NewColorizedHandler creates a new ColorizedHandler writing to the specified writer.
// It initializes the handler with a detected or specified color palette and applies
// optional configuration functions.
// Example:
//
//	handler := NewColorizedHandler(os.Stdout)
//	logger := ll.New("app").Enable().Handler(handler)
//	logger.Info("Test") // Output: [app] <colored INFO>: Test
func NewColorizedHandler(w io.Writer, opts ...ColorOption) *ColorizedHandler {
	// Initialize with writer
	c := &ColorizedHandler{
		w:          w,
		showTime:   false,
		timeFormat: time.RFC3339,
		noColor:    false,
		intensity:  IntensityNormal,
	}

	// Apply configuration options
	for _, opt := range opts {
		opt(c)
	}

	// Detect palette if not set
	c.palette = c.detectPalette()
	return c
}

// Handle processes a log entry and writes it with ANSI color codes.
// It delegates to specialized methods based on the entry's class (Dump, Raw, or regular).
// Returns an error if writing to the underlying writer fails.
// Thread-safe if the writer is thread-safe.
// Example:
//
//	handler.Handle(&lx.Entry{Message: "test", Level: lx.LevelInfo}) // Writes colored output
func (h *ColorizedHandler) Handle(e *lx.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch e.Class {
	case lx.ClassDump:
		// Handle hex dump entries
		return h.handleDumpOutput(e)
	case lx.ClassRaw:
		// Write raw entries directly
		_, err := h.w.Write([]byte(e.Message))
		return err
	default:
		// Handle standard log entries
		return h.handleRegularOutput(e)
	}
}

// Timestamped enables or disables timestamp display and optionally sets a custom time format.
// If format is empty, defaults to RFC3339.
// Example:
//
//	handler := NewColorizedHandler(os.Stdout).Timestamped(true, time.StampMilli)
//	// Output: Jan 02 15:04:05.000 [app] INFO: Test
func (h *ColorizedHandler) Timestamped(enable bool, format ...string) {
	h.showTime = enable
	if len(format) > 0 && format[0] != "" {
		h.timeFormat = format[0]
	}
}

// handleRegularOutput handles normal log entries.
// It formats the entry with colored namespace, level, message, fields, and stack trace (if present),
// writing the result to the handler's writer.
// Returns an error if writing fails.
// Example (internal usage):
//
//	h.handleRegularOutput(&lx.Entry{Message: "test", Level: lx.LevelInfo}) // Writes colored output
func (h *ColorizedHandler) handleRegularOutput(e *lx.Entry) error {
	// Get a builder from pool
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	// Add timestamp if enabled
	if h.showTime {
		builder.WriteString(e.Timestamp.Format(h.timeFormat))
		builder.WriteString(lx.Space)
	}

	// Format namespace with colors
	h.formatNamespace(builder, e)

	// Format level with color based on severity
	h.formatLevel(builder, e)

	// Add message and fields
	builder.WriteString(e.Message)
	h.formatFields(builder, e)

	// Format stack trace if present
	if len(e.Stack) > 0 {
		h.formatStack(builder, e.Stack)
	}

	// Append newline for non-None levels
	if e.Level != lx.LevelNone {
		builder.WriteString(lx.Newline)
	}

	// Write formatted output to writer
	_, err := h.w.Write([]byte(builder.String()))
	return err
}

// formatNamespace formats the namespace with ANSI color codes.
// It supports FlatPath ([parent/child]) and NestedPath ([parent]→[child]) styles.
// Example (internal usage):
//
//	h.formatNamespace(&builder, &lx.Entry{Namespace: "parent/child", Style: lx.FlatPath}) // Writes "[parent/child]: "
func (h *ColorizedHandler) formatNamespace(b *strings.Builder, e *lx.Entry) {
	if e.Namespace == "" {
		return
	}

	b.WriteString(lx.LeftBracket)
	switch e.Style {
	case lx.NestedPath:
		// Split namespace and format as [parent]→[child]
		parts := strings.Split(e.Namespace, lx.Slash)
		for i, part := range parts {
			b.WriteString(part)
			b.WriteString(lx.RightBracket)
			if i < len(parts)-1 {
				b.WriteString(lx.Arrow)
				b.WriteString(lx.LeftBracket)
			}
		}
	default: // FlatPath
		// Format as [parent/child]
		b.WriteString(e.Namespace)
		b.WriteString(lx.RightBracket)
	}
	b.WriteString(lx.Colon)
	b.WriteString(lx.Space)
}

// formatLevel formats the log level with ANSI color codes.
// It applies a color based on the level (Debug, Info, Warn, Error) and resets afterward.
// Example (internal usage):
//
//	h.formatLevel(&builder, &lx.Entry{Level: lx.LevelInfo}) // Writes "<green>INFO<reset>: "
func (h *ColorizedHandler) formatLevel(b *strings.Builder, e *lx.Entry) {
	// Map levels to colors
	color := map[lx.LevelType]string{
		lx.LevelDebug: h.palette.Debug, // Cyan
		lx.LevelInfo:  h.palette.Info,  // Green
		lx.LevelWarn:  h.palette.Warn,  // Yellow
		lx.LevelError: h.palette.Error, // Red
		lx.LevelFatal: h.palette.Fatal, // Bold Red
	}[e.Level]

	b.WriteString(color)
	b.WriteString(rightPad(e.Level.String(), 5))
	b.WriteString(h.palette.Reset)
	b.WriteString(lx.Colon)
	b.WriteString(lx.Space)
}

// formatFields formats the log entry's fields in sorted order.
// It writes fields as [key=value key=value], with type-based coloring for values.
// Example (internal usage):
//
//	h.formatFields(&builder, &lx.Entry{Fields: map[string]interface{}{"key": "value"}}) // Writes " [key=value]"
func (h *ColorizedHandler) formatFields(b *strings.Builder, e *lx.Entry) {
	if len(e.Fields) == 0 {
		return
	}

	b.WriteString(lx.Space)
	b.WriteString(lx.LeftBracket)

	// Format fields as key=value in insertion order
	for i, pair := range e.Fields {
		if i > 0 {
			b.WriteString(lx.Space)
		}

		// Color the key
		b.WriteString(h.palette.Key)
		b.WriteString(pair.Key)
		b.WriteString(h.palette.Reset)
		b.WriteString("=")

		// Format value with type-based coloring
		h.formatFieldValue(b, pair.Value)
	}

	b.WriteString(lx.RightBracket)
}

// formatFieldValue formats a field value with type-based ANSI color codes.
// It applies different colors for numbers, strings, booleans, times, errors, etc.
// Example (internal usage):
//
//	h.formatFieldValue(&builder, 42) // Writes colored "42"
func (h *ColorizedHandler) formatFieldValue(b *strings.Builder, value interface{}) {
	switch v := value.(type) {
	case time.Time:
		// Format timestamps
		b.WriteString(h.palette.Time)
		b.WriteString(v.Format("2006-01-02 15:04:05"))
		b.WriteString(h.palette.Reset)

	case time.Duration:
		// Format durations in a human-readable way
		b.WriteString(h.palette.Time)
		h.formatDuration(b, v)
		b.WriteString(h.palette.Reset)

	case error:
		// Format errors with special highlighting
		b.WriteString(h.palette.Error)
		b.WriteString(`"`)
		b.WriteString(v.Error())
		b.WriteString(`"`)
		b.WriteString(h.palette.Reset)

	// Integer types
	case int, int8, int16, int32, int64:
		b.WriteString(h.palette.Number)
		fmt.Fprint(b, v)
		b.WriteString(h.palette.Reset)

	// Unsigned integer types
	case uint, uint8, uint16, uint32, uint64:
		b.WriteString(h.palette.Number)
		fmt.Fprint(b, v)
		b.WriteString(h.palette.Reset)

	// Float types
	case float32, float64:
		b.WriteString(h.palette.Number)
		// Smart precision formatting
		switch f := v.(type) {
		case float32:
			fmt.Fprintf(b, "%.6g", f)
		case float64:
			fmt.Fprintf(b, "%.6g", f)
		}
		b.WriteString(h.palette.Reset)

	case string:
		b.WriteString(h.palette.String)
		// Quote strings for clarity
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
		// Default formatting for unknown types
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
// It structures the stack trace with colored goroutine, function, and path segments,
// using indentation and separators for readability.
// Example (internal usage):
//
//	h.formatStack(&builder, []byte("goroutine 1 [running]:\nmain.main()\n\tmain.go:10")) // Appends colored stack trace
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

	// Format goroutine line
	b.WriteString("  ┌─ ")
	b.WriteString(h.palette.Goroutine)
	b.WriteString(lines[0])
	b.WriteString(h.palette.Reset)
	b.WriteString("\n")

	// Field function name and file path lines
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

			// Look for last "/" before ".go:"
			lastSlash := strings.LastIndex(pathLine, "/")
			goIndex := strings.Index(pathLine, ".go:")

			if lastSlash >= 0 && goIndex > lastSlash {
				// Prefix path
				prefix := pathLine[:lastSlash+1]
				// File and line (e.g., ll.go:698 +0x5c)
				suffix := pathLine[lastSlash+1:]

				b.WriteString(h.palette.Path)
				b.WriteString(prefix)
				b.WriteString(h.palette.Reset)

				b.WriteString(h.palette.Path) // Use mainPath color for suffix
				b.WriteString(suffix)
				b.WriteString(h.palette.Reset)
			} else {
				// Fallback: whole line is gray
				b.WriteString(h.palette.Path)
				b.WriteString(pathLine)
				b.WriteString(h.palette.Reset)
			}

			b.WriteString("\n")
		}
	}

	// Handle any remaining unpaired line
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
// It applies colors to position, hex, ASCII, and title components of the dump,
// wrapping the output with colored BEGIN/END separators.
// Returns an error if writing fails.
// Example (internal usage):
//
//	h.handleDumpOutput(&lx.Entry{Class: lx.ClassDump, Message: "pos 00 hex: 61 62 'ab'"}) // Writes colored dump
func (h *ColorizedHandler) handleDumpOutput(e *lx.Entry) error {
	// Get a builder from pool
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	// Add timestamp if enabled
	if h.showTime {
		builder.WriteString(e.Timestamp.Format(h.timeFormat))
		builder.WriteString(lx.Newline)
	}

	// Write colored BEGIN separator
	builder.WriteString(h.palette.Title)
	builder.WriteString("---- BEGIN DUMP ----")
	builder.WriteString(h.palette.Reset)
	builder.WriteString("\n")

	// Process each line of the dump
	lines := strings.Split(e.Message, "\n")
	length := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(line, "pos ") {
			// Parse and color position and hex/ASCII parts
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
			// Color type dump lines
			builder.WriteString(h.palette.Header)
			builder.WriteString(line)
			builder.WriteString(h.palette.Reset)
		} else {
			// Write non-dump lines as-is
			builder.WriteString(line)
		}

		// Don't add newline for the last line
		if i < length-1 {
			builder.WriteString("\n")
		}
	}

	// Write colored END separator
	builder.WriteString(h.palette.Title)
	builder.WriteString("---- END DUMP ----")
	builder.WriteString(h.palette.Reset)
	builder.WriteString("\n")

	// Write formatted output to writer
	_, err := h.w.Write([]byte(builder.String()))
	return err
}

// detectPalette selects a color palette based on terminal environment variables and intensity setting.
// It checks TERM_BACKGROUND, COLORFGBG, AppleInterfaceStyle, and NO_COLOR to determine
// whether a light or dark palette is appropriate, and applies intensity settings.
// Example (internal usage):
//
//	palette := h.detectPalette() // Returns appropriate palette with intensity applied
func (h *ColorizedHandler) detectPalette() Palette {
	// If colors are explicitly disabled, return noColorPalette
	if h.noColor {
		return noColorPalette
	}

	// Check NO_COLOR environment variable (standard: https://no-color.org/)
	if os.Getenv("NO_COLOR") != "" {
		return noColorPalette
	}

	// Check if terminal supports colors
	term := os.Getenv("TERM")
	if term == "dumb" || term == "" {
		// Check if we're on Windows without ANSI support
		if runtime.GOOS == "windows" && !h.isWindowsTerminalAnsiSupported() {
			return noColorPalette
		}
		// For non-Windows or Windows with ANSI, continue detection
	}

	// Get base palette based on terminal background
	var basePalette Palette

	// Check TERM_BACKGROUND (e.g., iTerm2)
	if bg, ok := os.LookupEnv("TERM_BACKGROUND"); ok {
		if bg == "light" {
			basePalette = lightPalette
		} else {
			basePalette = darkPalette
		}
	} else if fgBg, ok := os.LookupEnv("COLORFGBG"); ok {
		// Check COLORFGBG (traditional xterm)
		parts := strings.Split(fgBg, ";")
		if len(parts) >= 2 {
			bg := parts[len(parts)-1] // Last part (some terminals add more fields)

			// Try to parse background color
			bgInt, err := strconv.Atoi(bg)
			if err == nil {
				// Light background colors: 0-7, 15 (white)
				// Dark background colors: 8-14 (bright colors)
				if bgInt >= 0 && bgInt <= 7 || bgInt == 15 {
					basePalette = lightPalette
				} else {
					basePalette = darkPalette
				}
			} else {
				basePalette = darkPalette // Default to dark
			}
		} else {
			basePalette = darkPalette // Default to dark
		}
	} else if style, ok := os.LookupEnv("AppleInterfaceStyle"); ok && strings.EqualFold(style, "dark") {
		// Check macOS dark mode
		basePalette = darkPalette
	} else {
		// Default: dark (conservative choice for terminals)
		basePalette = darkPalette
	}

	// Apply intensity setting
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

	// Windows Terminal
	if os.Getenv("WT_SESSION") != "" {
		return true
	}

	// ConEmu/ConEmu64
	if os.Getenv("ConEmuANSI") == "ON" {
		return true
	}

	// ANSICON
	if os.Getenv("ANSICON") != "" {
		return true
	}

	// Check Windows version (Windows 10+ has ANSI support in cmd/powershell)
	// This is a simplified check - in production you might want more detailed version checking
	return false
}

//func padRight(str string, length int) string {
//	if len(str) >= length {
//		return str
//	}
//	return str + strings.Repeat(" ", length-len(str))
//}
