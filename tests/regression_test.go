package tests

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/olekukonko/ll"
	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lx"
)

// runWithTimeout fails the test instead of hanging forever when the code under
// test deadlocks (the previous implementation of Err/Apply could).
func runWithTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("operation did not complete within %s (deadlock?)", d)
	}
}

// TestErrWithNonNilError verifies Err(err) logs and does not deadlock.
// Err used to hold the logger write lock while calling log(), which re-acquires
// the read lock on the same (non-reentrant) RWMutex.
func TestErrWithNonNilError(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := ll.New("reg/err", ll.WithHandler(lh.NewTextHandler(buf))).Enable()

	runWithTimeout(t, 2*time.Second, func() {
		logger.Err(errors.New("boom"))
	})

	out := buf.String()
	if !strings.Contains(out, "ERROR: boom") {
		t.Fatalf("expected error message in output, got %q", out)
	}
	// The error is added to the persistent context, so later logs carry it.
	buf.Reset()
	logger.Info("after")
	if !strings.Contains(buf.String(), "error=boom") {
		t.Fatalf("expected error to persist in context, got %q", buf.String())
	}
}

// TestApplyWithLevel verifies Apply() can be combined with options that take the
// logger lock (WithLevel -> Logger.Level). Apply used to hold the lock itself and
// deadlocked.
func TestApplyWithLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := ll.New("reg/apply", ll.WithHandler(lh.NewTextHandler(buf))).Enable()

	runWithTimeout(t, 2*time.Second, func() {
		logger.Apply(ll.WithLevel(lx.LevelDebug), ll.WithStyle(lx.NestedPath))
	})

	if got := logger.GetLevel(); got != lx.LevelDebug {
		t.Fatalf("expected level %v after Apply, got %v", lx.LevelDebug, got)
	}
	logger.Info("applied")
	if !strings.Contains(buf.String(), "applied") {
		t.Fatalf("expected log after Apply, got %q", buf.String())
	}
}

// TestNumericLevelFiltering pins the (numeric) level filtering semantics:
// a message is emitted when its level value does not exceed the configured one.
// This documents the existing behaviour so a future change is deliberate.
func TestNumericLevelFiltering(t *testing.T) {
	logger := ll.New("reg/level", ll.WithHandler(lh.NewTextHandler(new(bytes.Buffer)))).Enable()

	logger.Level(lx.LevelError)
	cases := []struct {
		level lx.LevelType
		want  bool
	}{
		{lx.LevelNone, true},
		{lx.LevelInfo, true}, // numeric: Info(1) <= Error(3)
		{lx.LevelWarn, true},
		{lx.LevelError, true},
		{lx.LevelFatal, false}, // numeric: Fatal(4) > Error(3)
		{lx.LevelDebug, false}, // numeric: Debug(5) > Error(3)
	}
	for _, c := range cases {
		if got := logger.CanLog(c.level); got != c.want {
			t.Errorf("Level(Error): CanLog(%v) = %v, want %v", c.level, got, c.want)
		}
	}
}

// TestDumpRendersAllBytes verifies Dump prints every byte for payload lengths
// that are exact multiples of the 8-byte row size.
func TestDumpRendersAllBytes(t *testing.T) {
	for _, payload := range []string{"12345678", "1234567890abcdef", "123456789"} {
		mem := lh.NewMemoryHandler()
		logger := ll.New("reg/dump", ll.WithHandler(mem)).Enable()
		logger.Dump([]byte(payload))

		var dump string
		for _, e := range mem.Entries() {
			if e.Class == lx.ClassDump {
				dump = e.Message
			}
		}
		if dump == "" {
			t.Fatalf("%q: no dump entry produced", payload)
		}
		if !strings.Contains(dump, "pos 00") {
			t.Errorf("%q: expected first row, got %q", payload, dump)
		}
		// Every 2-hex-digit group must be present, including for payloads whose
		// length is a multiple of 8 (which used to render empty rows).
		for i := 0; i < len(payload); i++ {
			want := fmt.Sprintf("%02x", payload[i])
			if !strings.Contains(dump, want) {
				t.Errorf("%q: byte %d (%s) missing from dump:\n%s", payload, i, want, dump)
			}
		}
		// No phantom trailing row.
		rows := len(strings.Split(strings.TrimRight(dump, "\n"), "\n"))
		wantRows := (len(payload) + 7) / 8
		if rows != wantRows {
			t.Errorf("%q: expected %d rows, got %d:\n%s", payload, wantRows, rows, dump)
		}
	}
}

// TestMemoryHandlerKeepsEntries verifies MemoryHandler stores independent copies:
// the logger recycles the entry (and its fields) as soon as Handle returns.
func TestMemoryHandlerKeepsEntries(t *testing.T) {
	mem := lh.NewMemoryHandler()
	logger := ll.New("reg/mem", ll.WithHandler(mem)).Enable()

	logger.Fields("i", 1).Info("first")
	logger.Fields("i", 2).Info("second")
	// Force many more logs so the pooled entry would be reused.
	for i := 0; i < 50; i++ {
		logger.Fields("i", i+100).Info("noise")
	}

	entries := mem.Entries()
	if len(entries) != 52 {
		t.Fatalf("expected 52 entries, got %d", len(entries))
	}
	if entries[0].Message != "first" {
		t.Errorf("entry 0 message = %q, want %q", entries[0].Message, "first")
	}
	if v, ok := entries[0].Fields.Get("i"); !ok || v != 1 {
		t.Errorf("entry 0 field i = %v (found=%v), want 1", v, ok)
	}
	if v, ok := entries[1].Fields.Get("i"); !ok || v != 2 {
		t.Errorf("entry 1 field i = %v (found=%v), want 2", v, ok)
	}
}

// TestNamespaceInheritsContext verifies child loggers inherit the parent's
// context fields (documented for AddContext) without aliasing the parent slice.
func TestNamespaceInheritsContext(t *testing.T) {
	buf := &bytes.Buffer{}
	root := ll.New("reg/ns").Enable().AddContext("service", "api").Handler(lh.NewTextHandler(buf))

	child := root.Namespace("http")
	buf.Reset()
	child.Info("child")
	if !strings.Contains(buf.String(), "service=api") {
		t.Fatalf("child namespace lost parent context: %q", buf.String())
	}

	// Copy-on-write: adding context on the child must not leak into the parent.
	child.AddContext("request_id", "abc")
	buf.Reset()
	root.Info("parent")
	if strings.Contains(buf.String(), "request_id") {
		t.Fatalf("child context leaked into parent: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "service=api") {
		t.Fatalf("parent lost its own context: %q", buf.String())
	}
}

// TestCloneInheritsContext verifies Clone keeps the parent's context.
func TestCloneInheritsContext(t *testing.T) {
	buf := &bytes.Buffer{}
	root := ll.New("reg/clone").Enable().AddContext("k", "v").Handler(lh.NewTextHandler(buf))
	clone := root.Clone()

	buf.Reset()
	clone.Info("cloned")
	if !strings.Contains(buf.String(), "k=v") {
		t.Fatalf("clone lost parent context: %q", buf.String())
	}
}

// TestFieldsLoggerAppendsToContext verifies Fields(...).Logger() keeps both the
// logger's context and the builder fields.
func TestFieldsLoggerAppendsToContext(t *testing.T) {
	buf := &bytes.Buffer{}
	root := ll.New("reg/fb").Enable().AddContext("service", "api").Handler(lh.NewTextHandler(buf))

	derived := root.Fields("user", "alice").Logger()
	buf.Reset()
	derived.Info("derived")

	out := buf.String()
	if !strings.Contains(out, "service=api") || !strings.Contains(out, "user=alice") {
		t.Fatalf("expected both context and builder fields, got %q", out)
	}
}

// TestSuspendSuppressesAllPaths verifies Suspend() also silences paths that
// bypass the Logger level methods (FieldBuilder, Line).
func TestSuspendSuppressesAllPaths(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := ll.New("reg/suspend", ll.WithHandler(lh.NewTextHandler(buf))).Enable().Suspend()

	logger.Fields("a", 1).Info("via fields")
	logger.Line(1)
	logger.Info("direct")

	if buf.Len() != 0 {
		t.Fatalf("suspended logger produced output: %q", buf.String())
	}
}

// TestSinceDoesNotDuplicateContext verifies timed entries list context fields once.
func TestSinceDoesNotDuplicateContext(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := ll.New("reg/since", ll.WithHandler(lh.NewTextHandler(buf))).Enable().AddContext("service", "api")

	logger.Since().Info("timed")

	out := buf.String()
	if n := strings.Count(out, "service=api"); n != 1 {
		t.Fatalf("expected context field exactly once, got %d in %q", n, out)
	}
	if !strings.Contains(out, "duration_ms=") {
		t.Fatalf("expected duration fields, got %q", out)
	}
}

// TestMultiHandlerErrorKeepsCause verifies the wrapped handler error keeps its text.
func TestMultiHandlerErrorKeepsCause(t *testing.T) {
	failing := lh.NewHandlerFunc(func(*lx.Entry) error { return errors.New("disk full") })
	multi := lh.NewMultiHandler(failing)

	err := multi.Handle(&lx.Entry{Message: "x"})
	if err == nil {
		t.Fatal("expected error from MultiHandler")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected cause in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "handler 0") {
		t.Fatalf("expected handler index in error, got %q", err.Error())
	}
}

// TestFatalOptionsInheritedByNamespace verifies the Fatal opt-ins survive Namespace().
func TestFatalOptionsInheritedByNamespace(t *testing.T) {
	parent := ll.New("reg/fatal",
		ll.WithHandler(lh.NewTextHandler(new(bytes.Buffer))),
		ll.WithFatalExits(true),
		ll.WithFatalStack(true),
	)
	child := parent.Namespace("sub")
	if !child.FatalExits() {
		t.Error("Namespace() dropped WithFatalExits")
	}
	if !child.FatalStack() {
		t.Error("Namespace() dropped WithFatalStack")
	}
}

// TestNamespaceRulesStillWork verifies the HasRules fast path does not disable
// explicit namespace rules.
func TestNamespaceRulesStillWork(t *testing.T) {
	buf := &bytes.Buffer{}
	root := ll.New("reg/nsrules").Disable().Handler(lh.NewTextHandler(buf))
	child := root.Namespace("child")

	child.NamespaceEnable("")
	buf.Reset()
	child.Info("enabled")
	if !strings.Contains(buf.String(), "enabled") {
		t.Fatalf("namespace enable rule ignored: %q", buf.String())
	}

	child.NamespaceDisable("")
	buf.Reset()
	child.Info("disabled")
	if strings.Contains(buf.String(), "disabled") {
		t.Fatalf("namespace disable rule ignored: %q", buf.String())
	}
}

// allocDelta returns the marginal allocations per call of fn compared with an
// empty control loop. AllocsPerRun counts process-wide allocations, so other
// tests' background goroutines would otherwise show up as noise; measuring the
// delta against a control loop right before it cancels most of that out.
func allocDelta(fn func()) float64 {
	best := math.Inf(1)
	for i := 0; i < 3; i++ {
		control := testing.AllocsPerRun(2000, func() {})
		target := testing.AllocsPerRun(2000, fn)
		if d := target - control; d < best {
			best = d
		}
	}
	return best
}

// TestDisabledLogAllocations verifies the disabled fast path stays allocation free.
func TestDisabledLogAllocations(t *testing.T) {
	logger := ll.New("reg/alloc", ll.WithHandler(lh.NewTextHandler(new(bytes.Buffer)))).Disable()
	if d := allocDelta(func() { logger.Info("skipped") }); d > 0.01 {
		t.Fatalf("disabled log allocates: %.2f allocs/op", d)
	}
	// Level-filtered logs must not allocate either.
	filtered := ll.New("reg/alloc2", ll.WithHandler(lh.NewTextHandler(new(bytes.Buffer)))).Level(lx.LevelNone)
	if d := allocDelta(func() { filtered.Info("filtered out") }); d > 0.01 {
		t.Fatalf("level-filtered log allocates: %.2f allocs/op", d)
	}
}

// TestEnabledLogAllocations verifies a simple enabled log does not allocate:
// the message used to be copied into a fresh strings.Builder on every entry.
func TestEnabledLogAllocations(t *testing.T) {
	logger := ll.New("reg/alloc3", ll.WithHandler(lh.NewHandlerFunc(func(*lx.Entry) error { return nil }))).Enable()
	if d := allocDelta(func() { logger.Info("message") }); d > 0.01 {
		t.Fatalf("enabled log allocates: %.2f allocs/op", d)
	}
}
