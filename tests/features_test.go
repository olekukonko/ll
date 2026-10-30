package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/olekukonko/ll"
	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lx"
)

// TestLogAtArbitraryLevel verifies the dynamic-level entry points.
func TestLogAtArbitraryLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := ll.New("feat/log", ll.WithHandler(lh.NewTextHandler(buf))).Enable()

	logger.Log(lx.LevelWarn, "warn", "entry")
	logger.Logf(lx.LevelError, "error %d", 7)
	logger.Fields("k", "v").Log(lx.LevelDebug, "with", "fields")
	logger.Fields("k", "v").Logf(lx.LevelInfo, "fmt %s", "fields")

	out := buf.String()
	for _, want := range []string{"WARN: warn entry", "ERROR: error 7", "DEBUG: with fields [k=v]", "INFO: fmt fields [k=v]"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got %q", want, out)
		}
	}
}

// TestOrderedLevelsOptIn verifies severity-based filtering is opt-in and that it
// behaves like a minimum-severity threshold once enabled.
func TestOrderedLevelsOptIn(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := ll.New("feat/ordered",
		ll.WithHandler(lh.NewTextHandler(buf)),
		ll.WithOrderedLevels(true),
	).Level(lx.LevelError).Enable()

	logger.Info("nope")
	logger.Warn("nope")
	logger.Error("yes")
	logger.Fatal("yes-fatal") // Fatal must never be dropped by a threshold

	out := buf.String()
	if strings.Contains(out, "nope") {
		t.Errorf("ordered levels emitted a below-threshold entry: %q", out)
	}
	if !strings.Contains(out, "ERROR: yes") || !strings.Contains(out, "FATAL: yes-fatal") {
		t.Errorf("ordered levels dropped an at/above-threshold entry: %q", out)
	}

	// Toggle back to legacy ordering: Info is emitted again at LevelError.
	logger.OrderedLevels(false)
	buf.Reset()
	logger.Info("legacy")
	if !strings.Contains(buf.String(), "legacy") {
		t.Errorf("legacy ordering not restored: %q", buf.String())
	}
}

// TestCallerReporting verifies caller capture renders into text and JSON output.
func TestCallerReporting(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := ll.New("feat/caller", ll.WithHandler(lh.NewTextHandler(buf)), ll.WithCaller(true)).Enable()

	logger.Info("with caller") // this line number is reported

	out := buf.String()
	if !strings.Contains(out, "features_test.go:") {
		t.Fatalf("expected caller location in text output, got %q", out)
	}
	if !strings.Contains(out, "with caller") {
		t.Fatalf("expected message, got %q", out)
	}

	// JSON handler exposes the caller as a field.
	jb := &bytes.Buffer{}
	jl := ll.New("feat/caller", ll.WithHandler(lh.NewJSONHandler(jb)), ll.WithCaller(true)).Enable()
	jl.Info("json caller")

	var rec lh.JsonOutput
	if err := json.Unmarshal(jb.Bytes(), &rec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !strings.HasPrefix(rec.Caller, "features_test.go:") {
		t.Fatalf("expected caller in JSON, got %q", rec.Caller)
	}

	// Disabled by default: no caller anywhere.
	plain := &bytes.Buffer{}
	ll.New("feat/caller", ll.WithHandler(lh.NewTextHandler(plain))).Enable().Info("no caller")
	if strings.Contains(plain.String(), "features_test.go:") {
		t.Fatalf("caller reporting should be off by default: %q", plain.String())
	}
}

// TestCtxExtractor verifies context-derived fields are attached per call.
func TestCtxExtractor(t *testing.T) {
	type ctxKey struct{}
	extract := func(ctx context.Context) []lx.Field {
		if v, ok := ctx.Value(ctxKey{}).(string); ok {
			return []lx.Field{{Key: "trace_id", Value: v}}
		}
		return nil
	}

	buf := &bytes.Buffer{}
	logger := ll.New("feat/ctx",
		ll.WithHandler(lh.NewTextHandler(buf)),
		ll.WithContextExtractor(extract),
	).Enable()

	ctx := context.WithValue(context.Background(), ctxKey{}, "trace-123")
	logger.Ctx(ctx).Info("handled")

	out := buf.String()
	if !strings.Contains(out, "trace_id=trace-123") {
		t.Fatalf("expected extracted context field, got %q", out)
	}

	// Without an extractor Ctx is a no-op (returns the same logger).
	plain := ll.New("feat/ctx2", ll.WithHandler(lh.NewTextHandler(new(bytes.Buffer)))).Enable()
	if plain.Ctx(context.Background()) != plain {
		t.Fatal("Ctx without extractor should return the receiver")
	}
}

// TestLoggerFlushAndClose verifies Logger.Flush/Close reach buffered handlers,
// including through a MultiHandler.
func TestLoggerFlushAndClose(t *testing.T) {
	mem := lh.NewMemoryHandler()
	buffered := lh.NewBuffered(mem, lh.WithBatchSize(100), lh.WithFlushInterval(time.Hour))
	multi := lh.NewMultiHandler(buffered)

	logger := ll.New("feat/flush", ll.WithHandler(multi)).Enable()
	logger.Info("pending")

	if got := len(mem.Entries()); got != 0 {
		t.Fatalf("expected entry to be buffered, got %d immediate entries", got)
	}
	if err := logger.Flush(); err != nil {
		t.Fatalf("Flush() error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(mem.Entries()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(mem.Entries()); got != 1 {
		t.Fatalf("expected 1 flushed entry, got %d", got)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

// TestNamespaceLevel verifies a per-namespace threshold overrides the logger's.
func TestNamespaceLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	root := ll.New("feat/nslevel", ll.WithHandler(lh.NewTextHandler(buf))).Enable()

	root.NamespaceLevel("db", lx.LevelError)

	root.Namespace("db").Info("db info")   // suppressed (below namespace level)
	root.Namespace("db").Error("db error") // emitted
	root.Namespace("api").Info("api info") // emitted (no namespace level)

	out := buf.String()
	if strings.Contains(out, "db info") {
		t.Errorf("namespace level did not suppress info: %q", out)
	}
	if !strings.Contains(out, "db error") {
		t.Errorf("namespace level suppressed error: %q", out)
	}
	if !strings.Contains(out, "api info") {
		t.Errorf("unrelated namespace was suppressed: %q", out)
	}
}
