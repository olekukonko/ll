package ll

import (
	"container/list"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/olekukonko/ll/lx"
)

// -----------------------------------------------------------------------------
// Global Cache Instance
// -----------------------------------------------------------------------------

// sourceCache caches up to 128 source files using LRU eviction.
var sourceCache = newFileLRU(128)

// -----------------------------------------------------------------------------
// File-Level LRU Cache
// -----------------------------------------------------------------------------

type fileLRU struct {
	capacity int
	mu       sync.Mutex // Simplifies logic compared to RWMutex for LRU updates
	list     *list.List
	items    map[string]*list.Element
}

type fileItem struct {
	key   string
	lines []string
}

func newFileLRU(capacity int) *fileLRU {
	if capacity <= 0 {
		capacity = 1
	}
	return &fileLRU{
		capacity: capacity,
		list:     list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

// getLine retrieves a specific 1-indexed line from a file.
func (c *fileLRU) getLine(file string, line int) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. Cache Hit
	if elem, ok := c.items[file]; ok {
		c.list.MoveToFront(elem)
		// If lines is nil, it means we previously failed to read this file (negative cache)
		item := elem.Value.(*fileItem)
		if item.lines == nil {
			return "", false
		}
		return nthLine(item.lines, line)
	}

	// 2. Cache Miss - Read File
	// Release lock during I/O to avoid blocking other loggers
	c.mu.Unlock()
	data, err := os.ReadFile(file)
	c.mu.Lock()

	// 3. Double-check (another goroutine might have loaded it while unlocked)
	if elem, ok := c.items[file]; ok {
		c.list.MoveToFront(elem)
		item := elem.Value.(*fileItem)
		if item.lines == nil {
			return "", false
		}
		return nthLine(item.lines, line)
	}

	var lines []string
	if err == nil {
		lines = strings.Split(string(data), "\n")
	}

	// 4. Store (Positive or Negative Cache)
	// We store 'nil' lines if error occurred to prevent repeated disk hits
	item := &fileItem{
		key:   file,
		lines: lines,
	}
	elem := c.list.PushFront(item)
	c.items[file] = elem

	// 5. Evict if needed
	if c.list.Len() > c.capacity {
		old := c.list.Back()
		if old != nil {
			c.list.Remove(old)
			delete(c.items, old.Value.(*fileItem).key)
		}
	}

	if lines == nil {
		return "", false
	}
	return nthLine(lines, line)
}

// nthLine returns the 1-indexed line from slice.
func nthLine(lines []string, n int) (string, bool) {
	if n <= 0 || n > len(lines) {
		return "", false
	}
	return strings.TrimSuffix(lines[n-1], "\r"), true
}

// -----------------------------------------------------------------------------
// Logger Debug Implementation
// -----------------------------------------------------------------------------

// Dbg logs debug information including source file, line number,
// and the best-effort extracted expression.
//
// Example:
//
//	x := 42
//	logger.Dbg("value", x)
//	Output: [file.go:123] "value", x = "value", 42
func (l *Logger) Dbg(values ...interface{}) {
	if !l.shouldLog(lx.LevelInfo) {
		return
	}
	l.dbg(2, values...)
}

func (l *Logger) dbg(skip int, values ...interface{}) {
	file, line, ok := callerFrame(skip)
	if !ok {
		// Fallback if we can't get frame
		var sb strings.Builder
		sb.WriteString("[?:?] ")
		for i, v := range values {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%+v", v))
		}
		l.log(lx.LevelInfo, lx.ClassText, sb.String(), nil, false)
		return
	}

	shortFile := file
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		shortFile = file[idx+1:]
	}

	srcLine, hit := sourceCache.getLine(file, line)

	var expr string
	if hit && srcLine != "" {
		// Attempt to extract the text inside Dbg(...)
		if a := strings.Index(srcLine, "Dbg("); a >= 0 {
			rest := srcLine[a+len("Dbg("):]
			if b := strings.LastIndex(rest, ")"); b >= 0 {
				expr = strings.TrimSpace(rest[:b])
			}
		} else {
			// Fallback: extract first (...) group if Dbg isn't explicit prefix
			a := strings.Index(srcLine, "(")
			b := strings.LastIndex(srcLine, ")")
			if a >= 0 && b > a {
				expr = strings.TrimSpace(srcLine[a+1 : b])
			}
		}
	}

	// Aggregate values into a single string to avoid double logging
	var valBuilder strings.Builder
	for i, v := range values {
		if i > 0 {
			valBuilder.WriteString(", ")
		}
		valBuilder.WriteString(fmt.Sprintf("%+v", v))
	}

	var out string
	if expr != "" {
		out = fmt.Sprintf("[%s:%d] %s = %s", shortFile, line, expr, valBuilder.String())
	} else {
		out = fmt.Sprintf("[%s:%d] %s", shortFile, line, valBuilder.String())
	}

	l.log(lx.LevelInfo, lx.ClassText, out, nil, false)
}

// -----------------------------------------------------------------------------
// Caller Resolution
// -----------------------------------------------------------------------------

// callerFrame walks stack frames until it finds the first frame
// outside the ll package.
func callerFrame(skip int) (file string, line int, ok bool) {
	// +2 to skip callerFrame + dbg itself.
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip+2, pcs)
	if n == 0 {
		return "", 0, false
	}

	frames := runtime.CallersFrames(pcs[:n])
	for {
		fr, more := frames.Next()

		// fr.Function looks like: "github.com/you/mod/ll.(*Logger).Dbg"
		// We want the first frame that is NOT inside package ll.
		if fr.Function == "" || !strings.Contains(fr.Function, "/ll.") && !strings.Contains(fr.Function, ".ll.") {
			return fr.File, fr.Line, true
		}

		if !more {
			// Fallback: return the last frame we saw
			return fr.File, fr.Line, fr.File != ""
		}
	}
}
