package lh

import (
	"strings"
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
