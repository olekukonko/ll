package lh

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/olekukonko/ll/lx"
)

// rightPad pads a string with spaces on the right to reach the specified length.
// Returns the original string if it's already at or exceeds the target length.
// Uses strings.Builder for efficient memory allocation.
func rightPad(str string, length int) string {
	if len(str) >= length {
		return str
	}
	var sb strings.Builder
	sb.Grow(length)
	sb.WriteString(str)
	sb.WriteString(strings.Repeat(" ", length-len(str)))
	return sb.String()
}

// stringWriter is the interface for types that can write strings and bytes.
// Both *strings.Builder and *bytes.Buffer implement this.
type stringWriter interface {
	WriteString(s string) (int, error)
	Write(p []byte) (n int, err error)
}

// HandlerFunc adapts a plain function to the lx.Handler interface, mirroring
// http.HandlerFunc. It is handy for tests, one-off sinks and on-the-fly filtering.
//
// Example:
//
//	logger.Handler(lh.NewHandlerFunc(func(e *lx.Entry) error {
//	    fmt.Println(e.Message)
//	    return nil
//	}))
type HandlerFunc func(*lx.Entry) error

// Handle calls f(e).
func (f HandlerFunc) Handle(e *lx.Entry) error { return f(e) }

// NewHandlerFunc wraps fn as an lx.Handler.
func NewHandlerFunc(fn func(*lx.Entry) error) HandlerFunc { return HandlerFunc(fn) }

// CloneEntry returns a deep copy of an entry.
//
// Handlers must copy an entry before retaining it beyond the Handle call: the
// logger recycles *lx.Entry (including its Fields slice) as soon as Handle
// returns, so keeping the original pointer is a data race waiting to happen.
func CloneEntry(e *lx.Entry) *lx.Entry {
	if e == nil {
		return nil
	}
	entryCopy := &lx.Entry{
		Timestamp: e.Timestamp,
		Level:     e.Level,
		Message:   e.Message,
		Namespace: e.Namespace,
		Style:     e.Style,
		Class:     e.Class,
		Error:     e.Error,
		Caller:    e.Caller,
		Id:        e.Id,
	}
	if len(e.Fields) > 0 {
		entryCopy.Fields = make(lx.Fields, len(e.Fields))
		copy(entryCopy.Fields, e.Fields)
	}
	if len(e.Stack) > 0 {
		entryCopy.Stack = make([]byte, len(e.Stack))
		copy(entryCopy.Stack, e.Stack)
	}
	return entryCopy
}

// writeFieldValue writes a field value to the builder using type switches
// to avoid reflection and allocations associated with fmt.Fprint.
func writeFieldValue(b stringWriter, v interface{}) {
	switch val := v.(type) {
	case string:
		b.WriteString(val)
	case int:
		b.WriteString(strconv.Itoa(val))
	case int8:
		b.WriteString(strconv.FormatInt(int64(val), 10))
	case int16:
		b.WriteString(strconv.FormatInt(int64(val), 10))
	case int32:
		b.WriteString(strconv.FormatInt(int64(val), 10))
	case int64:
		b.WriteString(strconv.FormatInt(val, 10))
	case uint:
		b.WriteString(strconv.FormatUint(uint64(val), 10))
	case uint8:
		b.WriteString(strconv.FormatUint(uint64(val), 10))
	case uint16:
		b.WriteString(strconv.FormatUint(uint64(val), 10))
	case uint32:
		b.WriteString(strconv.FormatUint(uint64(val), 10))
	case uint64:
		b.WriteString(strconv.FormatUint(val, 10))
	case float32:
		b.WriteString(strconv.FormatFloat(float64(val), 'g', -1, 32))
	case float64:
		b.WriteString(strconv.FormatFloat(val, 'g', -1, 64))
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("nil")
	case error:
		b.WriteString(val.Error())
	case fmt.Stringer:
		b.WriteString(val.String())
	default:
		fmt.Fprint(b, val)
	}
}
