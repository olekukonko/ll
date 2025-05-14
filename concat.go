package ll

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unsafe"
)

const (
	maxRecursionDepth = 20
	nilString         = "<nil>"
	unexportedString  = "<?>"
)

// Concat efficiently concatenates values without a separator.
func Concat(args ...any) string {
	return ConcatWith("", args...)
}

// ConcatWith concatenates values with a separator using optimized type handling.
func ConcatWith(sep string, args ...any) string {
	switch len(args) {
	case 0:
		return ""
	case 1:
		return toString(args[0])
	}

	var b strings.Builder
	b.Grow(estimateArgsSize(sep, args))

	for i, arg := range args {
		if i > 0 {
			b.WriteString(sep)
		}
		writeValue(&b, arg, 0)
	}

	return b.String()
}

// ConcatAll concatenates elements with separators, prefixes and suffixes efficiently.
func ConcatAll(sep string, before []any, after []any, args ...any) string {
	totalLen := len(before) + len(after) + len(args)
	switch totalLen {
	case 0:
		return ""
	case 1:
		switch {
		case len(before) > 0:
			return toString(before[0])
		case len(args) > 0:
			return toString(args[0])
		default:
			return toString(after[0])
		}
	}

	var b strings.Builder
	b.Grow(estimateTotalSize(sep, before, after, args))

	// Write before elements
	writeGroup(&b, sep, before)

	// Write main arguments
	if len(before) > 0 && len(args) > 0 {
		b.WriteString(sep)
	}
	writeGroup(&b, sep, args)

	// Write after elements
	if len(after) > 0 && (len(before) > 0 || len(args) > 0) {
		b.WriteString(sep)
	}
	writeGroup(&b, sep, after)

	return b.String()
}

func writeGroup(b *strings.Builder, sep string, group []any) {
	for i, arg := range group {
		if i > 0 {
			b.WriteString(sep)
		}
		writeValue(b, arg, 0)
	}
}

func toString(arg any) string {
	switch v := arg.(type) {
	case string:
		return v
	case []byte:
		return *(*string)(unsafe.Pointer(&v))
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func estimateTotalSize(sep string, before, after, args []any) int {
	size := 0
	if len(before) > 0 {
		size += estimateArgsSize(sep, before)
	}
	if len(args) > 0 {
		if size > 0 {
			size += len(sep)
		}
		size += estimateArgsSize(sep, args)
	}
	if len(after) > 0 {
		if size > 0 {
			size += len(sep)
		}
		size += estimateArgsSize(sep, after)
	}
	return size
}

func estimateArgsSize(sep string, args []any) int {
	if len(args) == 0 {
		return 0
	}
	size := len(sep) * (len(args) - 1)
	for _, arg := range args {
		size += estimateArgSize(arg)
	}
	return size
}

func estimateArgSize(arg any) int {
	switch v := arg.(type) {
	case string:
		return len(v)
	case []byte:
		return len(v)
	case int:
		return numLen(int64(v))
	case int64:
		return numLen(v)
	case int32:
		return numLen(int64(v))
	case int16:
		return numLen(int64(v))
	case int8:
		return numLen(int64(v))
	case uint:
		return numLen(uint64(v))
	case uint64:
		return numLen(v)
	case uint32:
		return numLen(uint64(v))
	case uint16:
		return numLen(uint64(v))
	case uint8:
		return numLen(uint64(v))
	case float64:
		return 24 // Max digits for float64
	case float32:
		return 16 // Max digits for float32
	case bool:
		if v {
			return 4 // "true"
		}
		return 5 // "false"
	case fmt.Stringer:
		return 16 // Conservative estimate
	default:
		return 16 // Default estimate
	}
}

func numLen[T int64 | uint64](v T) int {
	if v < 0 {
		return 20 // Max digits for int64 + sign
	}
	return 20 // Max digits for uint64
}

func writeValue(b *strings.Builder, arg any, depth int) {
	if depth > maxRecursionDepth {
		b.WriteString("...")
		return
	}

	if arg == nil {
		b.WriteString(nilString)
		return
	}

	if s, ok := arg.(fmt.Stringer); ok {
		b.WriteString(s.String())
		return
	}

	switch v := arg.(type) {
	case string:
		b.WriteString(v)
	case []byte:
		b.Write(v)
	case int:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case int32:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case int16:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case int8:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case uint:
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint64:
		b.WriteString(strconv.FormatUint(v, 10))
	case uint32:
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint16:
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint8:
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	case float64:
		b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	case float32:
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	default:
		val := reflect.ValueOf(arg)
		if val.Kind() == reflect.Ptr {
			if val.IsNil() {
				b.WriteString(nilString)
				return
			}
			val = val.Elem()
		}

		switch val.Kind() {
		case reflect.Slice, reflect.Array:
			formatSlice(b, val, depth)
		case reflect.Struct:
			formatStruct(b, val, depth)
		default:
			fmt.Fprint(b, v)
		}
	}
}

func formatSlice(b *strings.Builder, val reflect.Value, depth int) {
	b.WriteByte('[')
	for i := 0; i < val.Len(); i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		writeValue(b, val.Index(i).Interface(), depth+1)
	}
	b.WriteByte(']')
}

func formatStruct(b *strings.Builder, val reflect.Value, depth int) {
	typ := val.Type()
	b.WriteByte('[')

	first := true
	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		fieldValue := val.Field(i)

		if !first {
			b.WriteString("; ")
		}
		first = false

		b.WriteString(field.Name)
		b.WriteByte(':')

		if !fieldValue.CanInterface() {
			b.WriteString(unexportedString)
			continue
		}

		writeValue(b, fieldValue.Interface(), depth+1)
	}

	b.WriteByte(']')
}
