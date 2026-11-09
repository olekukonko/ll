package tests

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/olekukonko/ll"
	"github.com/olekukonko/ll/lh"
)

// TestDedupSummaryThroughLogger: Dedup -> MultiHandler -> text/JSON sinks.
func TestDedupSummaryThroughLogger(t *testing.T) {
	textBuf := &bytes.Buffer{}
	jsonBuf := &bytes.Buffer{}

	multi := lh.NewMultiHandler(
		lh.NewTextHandler(textBuf),
		lh.NewJSONHandler(jsonBuf),
	)
	dedup := lh.NewDedup(multi, 50*time.Millisecond,
		lh.WithDedupSummary(""),
		lh.WithDedupIgnore("duration"),
	)

	logger := ll.New("app", ll.WithHandler(dedup)).Enable()
	for i := 0; i < 5; i++ {
		logger.Fields("duration", "12ms").Info("upstream timeout")
	}

	// Before the window closes only the first occurrence is written.
	if got := strings.Count(textBuf.String(), "upstream timeout"); got != 1 {
		t.Fatalf("expected 1 occurrence before the window closes, got %d\n%s", got, textBuf.String())
	}

	// Close reports the total of the still-open window.
	if err := logger.Close(); err != nil {
		t.Fatalf("logger.Close returned %v", err)
	}

	text := textBuf.String()
	if !strings.Contains(text, "dup=4") {
		t.Fatalf("text sink missing the duplicate total:\n%s", text)
	}
	if !strings.Contains(text, "dup_w=50ms") {
		t.Fatalf("text sink missing the window length:\n%s", text)
	}
	// The ignored field still describes the summary line it stands for.
	if !strings.Contains(text, "duration=12ms") {
		t.Fatalf("summary dropped the original fields:\n%s", text)
	}

	var summary map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(jsonBuf.String()), "\n") {
		if !strings.Contains(line, "dup") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			t.Fatalf("invalid JSON summary %q: %v", line, err)
		}
	}
	if summary == nil {
		t.Fatalf("json sink missing the duplicate total:\n%s", jsonBuf.String())
	}
	fields, _ := summary["fields"].(map[string]interface{})
	if n, _ := fields["dup"].(float64); n != 4 {
		t.Fatalf("json dup = %v, want 4", fields["dup"])
	}
}

// TestDedupFlushReachesBufferedSink: Logger.Flush reaches a Buffered sink.
func TestDedupFlushReachesBufferedSink(t *testing.T) {
	buf := &bytes.Buffer{}

	buffered := lh.NewBuffered(lh.NewTextHandler(buf),
		lh.WithBatchSize(100),
		lh.WithFlushInterval(time.Hour),
	)
	defer buffered.Close()

	dedup := lh.NewDedup(lh.NewMultiHandler(buffered), time.Hour, lh.WithDedupSummary(""))
	logger := ll.New("app", ll.WithHandler(dedup)).Enable()

	for i := 0; i < 3; i++ {
		logger.Info("buffered line")
	}

	logger.Flush()   // must reach the buffered handler behind the deduper
	buffered.Close() // drain deterministically

	out := buf.String()
	if !strings.Contains(out, "buffered line") {
		t.Fatalf("buffered sink never received the entry:\n%s", out)
	}
	if !strings.Contains(out, "dup=2") {
		t.Fatalf("buffered sink missing the duplicate total:\n%s", out)
	}
}
