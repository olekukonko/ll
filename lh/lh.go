package lh

import "strings"

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
