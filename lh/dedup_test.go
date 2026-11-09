package lh

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olekukonko/ll/lx"
)

// TestDedup_ShardDistribution tests that entries are distributed across shards
func TestDedup_ShardDistribution(t *testing.T) {
	handler := &countingHandler{}
	d := NewDedup(handler, time.Second)

	// Send entries with different keys
	for i := 0; i < 1000; i++ {
		entry := &lx.Entry{
			Level:   lx.LevelInfo,
			Message: string(rune('a' + (i % 26))),
		}
		d.Handle(entry)
	}

	// Check that shards were used
	usedShards := 0
	for i := 0; i < len(d.shards); i++ {
		d.shards[i].mu.Lock()
		if len(d.shards[i].seen) > 0 {
			usedShards++
		}
		d.shards[i].mu.Unlock()
	}

	if usedShards < 2 {
		t.Fatalf("expected distribution across multiple shards, got %d", usedShards)
	}

	d.Close()
}

// TestDedup_NilKeyFn tests panic protection
func TestDedup_NilKeyFn(t *testing.T) {
	handler := &countingHandler{}
	d := NewDedup(handler, time.Second)
	d.keyFn = nil // Simulate bug

	// Should not panic, should pass through
	err := d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})
	if err != nil {
		t.Fatalf("expected nil error with nil keyFn, got %v", err)
	}

	d.Close()
}

// TestDedup_TTLExpiration tests that entries expire correctly
func TestDedup_TTLExpiration(t *testing.T) {
	handler := &countingHandler{}
	ttl := 50 * time.Millisecond
	d := NewDedup(handler, ttl, WithDedupCleanupInterval(10*time.Millisecond))

	// First entry
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "duplicate"})

	// Same entry immediately - should be deduped
	handler.count.Store(0)
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "duplicate"})
	if handler.count.Load() != 0 {
		t.Fatal("entry should have been deduped")
	}

	// Wait for TTL
	time.Sleep(ttl + 20*time.Millisecond)

	// Same entry after TTL - should be allowed
	handler.count.Store(0)
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "duplicate"})
	if handler.count.Load() != 1 {
		t.Fatal("entry should have been allowed after TTL")
	}

	d.Close()
}

type countingHandler struct {
	count atomic.Int32
}

func (c *countingHandler) Handle(e *lx.Entry) error {
	c.count.Add(1)
	return nil
}

func benchEntries(n int) []*lx.Entry {
	out := make([]*lx.Entry, n)
	for i := range out {
		out[i] = &lx.Entry{
			Level:     lx.LevelInfo,
			Message:   "upstream connect timeout " + strconv.Itoa(i),
			Namespace: "lb/pool/edge-7",
			Fields: lx.Fields{
				{Key: "duration", Value: "1200ms"},
				{Key: "upstream", Value: "10.0.3.14:8080"},
				{Key: "attempt", Value: 3},
			},
		}
	}
	return out
}

func BenchmarkDedup_Key(b *testing.B) {
	d := NewDefaultDedup()
	e := benchEntries(1)[0]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Calculate(e)
	}
}

// BenchmarkDedup_DuplicatePath is a storm on one key: the whole suppress path.
func BenchmarkDedup_DuplicatePath(b *testing.B) {
	d := NewDedup(&countingHandler{}, time.Hour)
	e := benchEntries(1)[0]
	d.Handle(e)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Handle(e)
	}
	b.StopTimer()
	d.Close()
}

func BenchmarkDedup_ShardContention(b *testing.B) {
	d := NewDedup(&countingHandler{}, time.Hour)
	entries := benchEntries(26)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = d.Handle(entries[i%len(entries)])
			i++
		}
	})
	b.StopTimer()
	d.Close()
}

func TestDedup_SuppressesWithFields(t *testing.T) {
	handler := &countingHandler{}
	d := NewDedup(handler, time.Second)

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "login failed",
		Fields: lx.Fields{
			{Key: "user", Value: "alice"},
			{Key: "ip", Value: "1.2.3.4"},
		},
	}

	d.Handle(entry)
	handler.count.Store(0)

	// Identical entry — must be suppressed
	d.Handle(entry)
	if handler.count.Load() != 0 {
		t.Fatal("entry with fields should have been deduped")
	}

	d.Close()
}

// captureHandler stores a deep copy: entries are recycled after Handle.
type captureHandler struct {
	mu      sync.Mutex
	entries []*lx.Entry
}

func (c *captureHandler) Handle(e *lx.Entry) error {
	clone := CloneEntry(e)
	c.mu.Lock()
	c.entries = append(c.entries, clone)
	c.mu.Unlock()
	return nil
}

func (c *captureHandler) snapshot() []*lx.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*lx.Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

// flushingHandler records flush calls in addition to capturing entries.
type flushingHandler struct {
	captureHandler
	flushes atomic.Int32
}

func (f *flushingHandler) Flush() {
	f.flushes.Add(1)
}

// waitFor blocks until at least want entries have been captured.
func (c *captureHandler) waitFor(t *testing.T, want int) []*lx.Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := c.snapshot()
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d entries, got %d", want, len(got))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// fieldUint32 reads a numeric field that was stored as a uint32 count.
func fieldUint32(t *testing.T, f lx.Fields, key string) uint32 {
	t.Helper()
	v, ok := f.Get(key)
	if !ok {
		t.Fatalf("field %q missing from %s", key, f.String())
	}
	n, ok := v.(uint32)
	if !ok {
		t.Fatalf("field %q has type %T, want uint32", key, v)
	}
	return n
}

// TestDedup_SummaryOnWindowClose: the total lands when the window closes.
func TestDedup_IgnoreFieldsAndOrder(t *testing.T) {
	h := &countingHandler{}
	d := NewDedup(h, time.Hour, WithDedupIgnore("duration"))
	defer d.Close()

	base := lx.Fields{{Key: "user", Value: "alice"}, {Key: "duration", Value: "1ms"}}

	// Same key: ignored field differs, and field order is reversed.
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "m", Fields: base})
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "m", Fields: lx.Fields{
		{Key: "duration", Value: "999ms"},
		{Key: "user", Value: "alice"},
	}})
	if got := h.count.Load(); got != 1 {
		t.Fatalf("ignored field or field order changed the key, next got %d entries", got)
	}

	// Different value on a tracked field: not a duplicate.
	d.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "m", Fields: lx.Fields{
		{Key: "user", Value: "bob"},
	}})
	if got := h.count.Load(); got != 2 {
		t.Fatalf("distinct field value was deduped, next got %d entries", got)
	}
}

func TestDedup_SummaryOnWindowClose(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, 20*time.Millisecond, WithDedupSummary(""))
	defer d.Close()

	entry := func() *lx.Entry {
		return &lx.Entry{
			Level:   lx.LevelInfo,
			Message: "upstream timeout",
			Fields:  lx.Fields{{Key: "user", Value: "alice"}},
		}
	}

	d.Handle(entry()) // emitted
	for i := 0; i < 9; i++ {
		d.Handle(entry()) // suppressed
	}

	got := handler.waitFor(t, 2)
	if len(got) != 2 {
		t.Fatalf("expected 1 occurrence + 1 summary, got %d entries", len(got))
	}

	summary := got[1]
	if n := fieldUint32(t, summary.Fields, DefaultDedupSummaryField); n != 9 {
		t.Fatalf("duplicates = %d, want 9", n)
	}
	if w, _ := summary.Fields.Get(DefaultDedupSummaryField + dedupWindowSuffix); w != d.ttl.String() {
		t.Fatalf("window = %v, want %s", w, d.ttl)
	}
	if summary.Message != "upstream timeout" {
		t.Fatalf("summary message = %q, want the original message", summary.Message)
	}
	if summary.Level != lx.LevelInfo {
		t.Fatalf("summary level = %v, want %v", summary.Level, lx.LevelInfo)
	}
	if user, ok := summary.Fields.Get("user"); !ok || user != "alice" {
		t.Fatalf("summary dropped the original fields: %s", summary.Fields.String())
	}

	// The occurrence itself must stay untouched — the total is not known yet.
	if _, ok := got[0].Fields.Get(DefaultDedupSummaryField); ok {
		t.Fatal("the first occurrence must not carry a duplicate total")
	}
}

// TestDedup_SummaryOnCloseFlushes: shutdown reports still-open windows.
func TestDedup_SummaryOnCloseFlushes(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary(""))

	entry := &lx.Entry{Level: lx.LevelWarn, Message: "boom"}
	d.Handle(entry)
	for i := 0; i < 4; i++ {
		d.Handle(entry)
	}

	if got := handler.snapshot(); len(got) != 1 {
		t.Fatalf("only the first occurrence should be emitted before the window closes, got %d", len(got))
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close returned %v", err)
	}

	got := handler.snapshot()
	if len(got) != 2 {
		t.Fatalf("Close must flush the pending total, got %d entries", len(got))
	}
	if n := fieldUint32(t, got[1].Fields, DefaultDedupSummaryField); n != 4 {
		t.Fatalf("duplicates = %d, want 4", n)
	}
}

// TestDedup_SummaryDisabledByDefault: no extra entries without the option.
func TestDedup_SummaryDisabledByDefault(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, 20*time.Millisecond)

	entry := &lx.Entry{Level: lx.LevelInfo, Message: "plain"}
	d.Handle(entry)
	for i := 0; i < 5; i++ {
		d.Handle(entry)
	}

	time.Sleep(60 * time.Millisecond) // let several sweeps run

	if got := handler.snapshot(); len(got) != 1 {
		t.Fatalf("expected only the first occurrence, got %d entries", len(got))
	}

	d.Close()

	if got := handler.snapshot(); len(got) != 1 {
		t.Fatalf("Close must not invent summaries, got %d entries", len(got))
	}

	// The count is still tracked, it is simply never attached to a log entry.
	if s := d.Stats(); s.Suppressed != 5 {
		t.Fatalf("Suppressed = %d, want 5", s.Suppressed)
	}
}

// TestDedup_SummaryCustomFieldName verifies the field name is configurable.
func TestDedup_SummaryCustomFieldName(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary("suppressed"))

	entry := &lx.Entry{Level: lx.LevelInfo, Message: "custom"}
	d.Handle(entry)
	d.Handle(entry)

	d.Close()

	got := handler.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if n := fieldUint32(t, got[1].Fields, "suppressed"); n != 1 {
		t.Fatalf("suppressed = %d, want 1", n)
	}
	if _, ok := got[1].Fields.Get(DefaultDedupSummaryField); ok {
		t.Fatal("default field must not be used when a custom name is given")
	}
}

// TestDedup_SummaryClassForcedText: raw entries render without fields.
func TestDedup_SummaryClassForcedText(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary(""))

	entry := &lx.Entry{Level: lx.LevelInfo, Message: "raw line", Class: lx.ClassRaw}
	d.Handle(entry)
	d.Handle(entry)
	d.Close()

	got := handler.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[1].Class != lx.ClassText {
		t.Fatalf("summary class = %v, want %v", got[1].Class, lx.ClassText)
	}
}

// TestDedup_SummaryCompactSkipsFields verifies the allocation-free summary mode.
func TestDedup_SummaryCompactSkipsFields(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary(""), WithDedupSummaryCompact())

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "compact",
		Fields:  lx.Fields{{Key: "user", Value: "alice"}},
	}
	d.Handle(entry)
	d.Handle(entry)
	d.Close()

	got := handler.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if _, ok := got[1].Fields.Get("user"); ok {
		t.Fatalf("compact summaries must not carry the original fields: %s", got[1].Fields.String())
	}
	if n := fieldUint32(t, got[1].Fields, DefaultDedupSummaryField); n != 1 {
		t.Fatalf("duplicates = %d, want 1", n)
	}
}

// TestDedup_DoesNotMutateCallerFields: entry.Fields may be a shared array.
func TestDedup_DoesNotMutateCallerFields(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary(""))

	// Spare capacity on purpose: an append would overwrite shared memory
	// instead of allocating.
	backing := make(lx.Fields, 1, 4)
	backing[0] = lx.Field{Key: "user", Value: "alice"}

	entry := &lx.Entry{Level: lx.LevelInfo, Message: "shared", Fields: backing}
	d.Handle(entry)
	d.Handle(entry)
	d.Close()

	if len(backing) != 1 || backing[0].Key != "user" || backing[0].Value != "alice" {
		t.Fatalf("shared backing array was overwritten: %v", backing)
	}
	if _, ok := entry.Fields.Get(DefaultDedupSummaryField); ok {
		t.Fatal("the caller's entry must not be mutated")
	}

	got := handler.snapshot()
	if n := fieldUint32(t, got[1].Fields, DefaultDedupSummaryField); n != 1 {
		t.Fatalf("duplicates = %d, want 1", n)
	}
	if user, ok := got[1].Fields.Get("user"); !ok || user != "alice" {
		t.Fatalf("summary lost the caller's field: %s", got[1].Fields.String())
	}
}

// TestDedup_StatsAndReport: counters, and Report resets pending only.
func TestDedup_StatsAndReport(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary(""))

	entry := &lx.Entry{Level: lx.LevelInfo, Message: "stats"}
	d.Handle(entry)
	for i := 0; i < 7; i++ {
		d.Handle(entry)
	}

	if got := d.Suppressed(); got != 7 {
		t.Fatalf("Suppressed = %d, want 7", got)
	}

	st := d.Stats()
	if st.Suppressed != 7 || st.Pending != 7 || st.Tracked != 1 {
		t.Fatalf("Stats = %+v, want {Suppressed:7 Pending:7 Tracked:1}", st)
	}

	d.Report()

	st = d.Stats()
	if st.Pending != 0 {
		t.Fatalf("Pending after Report = %d, want 0", st.Pending)
	}
	if st.Suppressed != 7 {
		t.Fatalf("Report must not reset the lifetime total, got %d", st.Suppressed)
	}
	if st.Tracked != 1 {
		t.Fatalf("Report must keep tracking live windows, tracked = %d", st.Tracked)
	}

	got := handler.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 1 occurrence + 1 summary, got %d", len(got))
	}
	if n := fieldUint32(t, got[1].Fields, DefaultDedupSummaryField); n != 7 {
		t.Fatalf("duplicates = %d, want 7", n)
	}

	d.Close()

	// Close reports nothing new: the counter was already flushed.
	if got := handler.snapshot(); len(got) != 2 {
		t.Fatalf("Close must not report an already reported total, got %d entries", len(got))
	}
}

// TestDedup_ReportKeepsWindowAlive: reporting keeps the window closed.
func TestDedup_ReportKeepsWindowAlive(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary(""))
	defer d.Close()

	entry := &lx.Entry{Level: lx.LevelInfo, Message: "live"}
	d.Handle(entry)
	d.Handle(entry)
	d.Report()
	d.Handle(entry) // still inside the TTL window — must stay suppressed

	if got := handler.snapshot(); len(got) != 2 {
		t.Fatalf("want 1 occurrence + 1 summary, got %d entries", len(got))
	}
}

// TestDedup_FlushReachesNestedHandlers: report first, then flush next.
func TestDedup_FlushReachesNestedHandlers(t *testing.T) {
	nested := &flushingHandler{}
	multi := NewMultiHandler(nested)
	d := NewDedup(multi, time.Hour, WithDedupSummary(""))
	defer d.Close()

	entry := &lx.Entry{Level: lx.LevelInfo, Message: "flush"}
	d.Handle(entry)
	d.Handle(entry)

	d.Flush()

	if got := nested.flushes.Load(); got != 1 {
		t.Fatalf("nested handler flushed %d times, want 1", got)
	}
	got := nested.snapshot()
	if len(got) != 2 {
		t.Fatalf("Flush must report pending totals before flushing, got %d entries", len(got))
	}
	if n := fieldUint32(t, got[1].Fields, DefaultDedupSummaryField); n != 1 {
		t.Fatalf("duplicates = %d, want 1", n)
	}
}

// TestDedup_ConcurrentCountAccuracy: exact count under concurrency.
func TestDedup_ConcurrentCountAccuracy(t *testing.T) {
	handler := &captureHandler{}
	d := NewDedup(handler, time.Hour, WithDedupSummary(""))

	const goroutines, perGoroutine = 8, 250

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = d.Handle(&lx.Entry{Level: lx.LevelError, Message: "storm"})
			}
		}()
	}
	wg.Wait()

	want := uint64(goroutines*perGoroutine - 1)
	if got := d.Suppressed(); got != want {
		t.Fatalf("Suppressed = %d, want %d", got, want)
	}
	if got := handler.snapshot(); len(got) != 1 {
		t.Fatalf("exactly one occurrence should be emitted, got %d", len(got))
	}

	d.Close()

	got := handler.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries after Close, got %d", len(got))
	}
	if n := fieldUint32(t, got[1].Fields, DefaultDedupSummaryField); uint64(n) != want {
		t.Fatalf("duplicates = %d, want %d", n, want)
	}
}
