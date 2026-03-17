package lh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olekukonko/ll/lx"
)

// mockHandlerBuffered is a test handler that tracks flush-batch calls and errors.
// It implements the batchHandler interface so flushBatch delivers the entire
// batch in one HandleBatch call; callCount therefore tracks the number of flush
// operations (batches) rather than the number of individual log entries.
type mockHandlerBuffered struct {
	mu        sync.Mutex
	entries   []*lx.Entry
	callCount int32
	err       error
	delay     time.Duration
}

// Handle processes a single entry without incrementing callCount.
// Because mockHandlerBuffered implements batchHandler, flushBatch always routes
// through HandleBatch instead of Handle.  This method satisfies lx.Handler and
// supports direct single-entry calls used by code paths that bypass flushBatch.
func (m *mockHandlerBuffered) Handle(e *lx.Entry) error {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	m.mu.Lock()
	m.entries = append(m.entries, e)
	m.mu.Unlock()
	return m.err
}

// HandleBatch implements batchHandler.  Called by flushBatch once per flush
// operation so callCount accurately tracks "number of flush batches".
func (m *mockHandlerBuffered) HandleBatch(entries []*lx.Entry) error {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	atomic.AddInt32(&m.callCount, 1)
	m.mu.Lock()
	m.entries = append(m.entries, entries...)
	m.mu.Unlock()
	return m.err
}

func (m *mockHandlerBuffered) Entries() []*lx.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*lx.Entry, len(m.entries))
	copy(out, m.entries)
	return out
}

func (m *mockHandlerBuffered) CallCount() int32 {
	return atomic.LoadInt32(&m.callCount)
}

// TestNewBuffered_Basic tests basic creation
func TestNewBuffered_Basic(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler)

	if b == nil {
		t.Fatal("expected buffered handler to be created")
	}
	if b.config.BatchSize != 100 {
		t.Fatalf("expected default batch size 100, got %d", b.config.BatchSize)
	}
	if b.config.FlushInterval != 10*time.Second {
		t.Fatalf("expected default flush interval 10s, got %v", b.config.FlushInterval)
	}

	b.Close()
}

// TestNewBuffered_CustomConfig tests custom configuration
func TestNewBuffered_CustomConfig(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler,
		WithBatchSize(50),
		WithFlushInterval(5*time.Second),
		WithMaxBuffer(500),
		WithFlushTimeout(200*time.Millisecond),
	)

	if b.config.BatchSize != 50 {
		t.Fatalf("expected batch size 50, got %d", b.config.BatchSize)
	}
	if b.config.FlushInterval != 5*time.Second {
		t.Fatalf("expected flush interval 5s, got %v", b.config.FlushInterval)
	}
	if b.config.MaxBuffer != 500 {
		t.Fatalf("expected max buffer 500, got %d", b.config.MaxBuffer)
	}
	if b.config.FlushTimeout != 200*time.Millisecond {
		t.Fatalf("expected flush timeout 200ms, got %v", b.config.FlushTimeout)
	}

	b.Close()
}

// TestNewBuffered_InvalidConfig tests config validation
func TestNewBuffered_InvalidConfig(t *testing.T) {
	handler := &mockHandlerBuffered{}

	// BatchSize < 1 should be set to 1
	b := NewBuffered(handler, WithBatchSize(0))
	if b.config.BatchSize != 1 {
		t.Fatalf("expected batch size 1, got %d", b.config.BatchSize)
	}
	b.Close()

	// MaxBuffer < BatchSize should be raised to BatchSize * 10
	b = NewBuffered(handler, WithBatchSize(100), WithMaxBuffer(50))
	if b.config.MaxBuffer != 1000 {
		t.Fatalf("expected max buffer 1000, got %d", b.config.MaxBuffer)
	}
	b.Close()

	// FlushInterval <= 0 should default to 10s
	b = NewBuffered(handler, WithFlushInterval(0))
	if b.config.FlushInterval != 10*time.Second {
		t.Fatalf("expected flush interval 10s, got %v", b.config.FlushInterval)
	}
	b.Close()

	// FlushTimeout <= 0 should default to 100ms
	b = NewBuffered(handler, WithFlushTimeout(0))
	if b.config.FlushTimeout != 100*time.Millisecond {
		t.Fatalf("expected flush timeout 100ms, got %v", b.config.FlushTimeout)
	}
	b.Close()
}

// TestBuffered_Handle_Basic tests basic entry handling
func TestBuffered_Handle_Basic(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler, WithBatchSize(10))

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "test message",
	}

	err := b.Handle(entry)
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// Entry should be buffered, not yet flushed
	if handler.CallCount() != 0 {
		t.Fatal("handler should not have been called yet")
	}

	b.Close()

	// After close, should be flushed
	if handler.CallCount() != 1 {
		t.Fatalf("expected 1 call, got %d", handler.CallCount())
	}
}

// TestBuffered_Handle_BatchFlush tests batch size triggering flush
func TestBuffered_Handle_BatchFlush(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler, WithBatchSize(5))

	// Send 4 entries - should not flush yet
	for i := 0; i < 4; i++ {
		b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: fmt.Sprintf("msg %d", i)})
	}

	if handler.CallCount() != 0 {
		t.Fatal("should not have flushed yet")
	}

	// 5th entry triggers flush
	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "trigger"})

	// Give time for async flush
	time.Sleep(50 * time.Millisecond)

	if handler.CallCount() != 1 {
		t.Fatalf("expected 1 flush, got %d", handler.CallCount())
	}

	entries := handler.Entries()
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	b.Close()
}

// TestBuffered_TimerFlush tests timer-based flushing
func TestBuffered_TimerFlush(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler,
		WithBatchSize(100),                      // Large batch size
		WithFlushInterval(100*time.Millisecond), // Short interval
	)

	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})

	// Should not flush immediately
	if handler.CallCount() != 0 {
		t.Fatal("should not have flushed immediately")
	}

	// Wait for timer
	time.Sleep(150 * time.Millisecond)

	if handler.CallCount() != 1 {
		t.Fatalf("expected timer flush, got %d calls", handler.CallCount())
	}

	b.Close()
}

// TestBuffered_ExplicitFlush tests explicit Flush() call
func TestBuffered_ExplicitFlush(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler, WithBatchSize(100))

	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})
	b.Flush()

	// Give time for async flush
	time.Sleep(50 * time.Millisecond)

	if handler.CallCount() != 1 {
		t.Fatalf("expected flush after Flush(), got %d calls", handler.CallCount())
	}

	b.Close()
}

// TestBuffered_FlushSignalResetsTicker tests that explicit flush resets the ticker
// This catches the goroutine leak bug where ticker wasn't reset
func TestBuffered_FlushSignalResetsTicker(t *testing.T) {
	handler := &mockHandlerBuffered{}
	flushInterval := 200 * time.Millisecond

	b := NewBuffered(handler,
		WithBatchSize(100), // Never flush on size
		WithFlushInterval(flushInterval),
	)

	// Send entry and flush immediately - resets ticker
	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "first"})
	b.Flush()
	time.Sleep(50 * time.Millisecond) // Wait for flush

	// Send another entry
	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "second"})

	// If ticker wasn't reset, it would fire at ~200ms from start
	// If reset, it fires at ~200ms from flush (~250ms from start)
	// We check at 150ms - should NOT have flushed yet if reset
	time.Sleep(100 * time.Millisecond)

	callCount := handler.CallCount()
	if callCount != 1 {
		t.Fatalf("ticker not reset - expected 1 call at 150ms, got %d", callCount)
	}

	// Wait for timer flush
	time.Sleep(150 * time.Millisecond)

	if handler.CallCount() != 2 {
		t.Fatalf("expected timer flush, got %d calls", handler.CallCount())
	}

	b.Close()
}

// blockingBatchHandler is a test handler whose HandleBatch blocks until the
// caller releases it via the gate channel.  This lets the test pin the worker
// goroutine inside HandleBatch so the entries channel stays full.
type blockingBatchHandler struct {
	mu      sync.Mutex
	entries []*lx.Entry
	gate    chan struct{} // close to unblock all waiting HandleBatch calls
}

func (h *blockingBatchHandler) Handle(e *lx.Entry) error {
	h.mu.Lock()
	h.entries = append(h.entries, e)
	h.mu.Unlock()
	return nil
}

func (h *blockingBatchHandler) HandleBatch(entries []*lx.Entry) error {
	<-h.gate // block until released
	h.mu.Lock()
	h.entries = append(h.entries, entries...)
	h.mu.Unlock()
	return nil
}

// TestBuffered_Overflow tests buffer overflow handling.
//
// Strategy: use a blockingBatchHandler whose HandleBatch blocks on a gate
// channel.  We send one entry and flush so the worker goroutine enters
// HandleBatch and stays there.  While the worker is blocked it cannot read
// more entries from the channel, so we can reliably fill it to capacity and
// then confirm the next Handle call returns an overflow error.
func TestBuffered_Overflow(t *testing.T) {
	gate := make(chan struct{})
	handler := &blockingBatchHandler{gate: gate}

	overflowCalled := int32(0)
	b := NewBuffered(handler,
		WithMaxBuffer(5),
		WithBatchSize(100),
		WithFlushInterval(time.Hour), // never auto-flush
		WithOverflowHandler(func(n int) {
			atomic.AddInt32(&overflowCalled, 1)
		}),
	)

	maxBuffer := b.Config().MaxBuffer // actual channel capacity after floor

	// Seed one entry and flush so the worker goroutine calls HandleBatch and
	// blocks on the gate, leaving it unable to drain the channel further.
	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "seed"})
	b.Flush()

	// Give the worker time to enter HandleBatch and block on the gate.
	time.Sleep(20 * time.Millisecond)

	// Fill the channel to capacity.  The worker is stuck, so every slot stays
	// occupied.  We have already used 1 slot for the seed entry that the worker
	// has dequeued, so we can enqueue maxBuffer entries.
	for i := 0; i < maxBuffer; i++ {
		err := b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: fmt.Sprintf("fill %d", i)})
		if err != nil {
			// Unblock the worker before failing so Close() can complete.
			close(gate)
			t.Fatalf("unexpected error filling slot %d: %v", i, err)
		}
	}

	// Channel is now full; the next Handle must overflow.
	err := b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "overflow"})

	// Unblock the worker so Close() can drain and finish cleanly.
	close(gate)

	if err == nil {
		t.Fatal("expected overflow error")
	}
	if atomic.LoadInt32(&overflowCalled) == 0 {
		t.Fatal("overflow handler should have been called")
	}

	b.Close()
}

// TestBuffered_OverflowWithFlushTrigger tests overflow triggering flush.
// WithMaxBuffer(3) + WithBatchSize(100) triggers the floor: MaxBuffer becomes
// 1000.  Filling only 3 slots and then adding a 4th always succeeds because
// the channel is nowhere near full.
func TestBuffered_OverflowWithFlushTrigger(t *testing.T) {
	handler := &mockHandlerBuffered{}
	handler.delay = 10 * time.Millisecond

	b := NewBuffered(handler,
		WithMaxBuffer(3),
		WithBatchSize(100),
	)

	// Fill a few slots (well within channel capacity after floor adjustment)
	for i := 0; i < 3; i++ {
		b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: fmt.Sprintf("fill %d", i)})
	}

	// Should succeed: channel has plenty of room
	err := b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "should succeed"})
	if err != nil {
		t.Fatalf("expected success after flush trigger: %v", err)
	}

	b.Close()
}

// TestBuffered_HandlerError tests error handling from underlying handler
func TestBuffered_HandlerError(t *testing.T) {
	handler := &mockHandlerBuffered{}
	handler.err = errors.New("handler failed")

	var errBuf bytes.Buffer
	b := NewBuffered(handler,
		WithBatchSize(1),
		WithErrorOutput(&errBuf),
	)

	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})

	// Give time for async processing
	time.Sleep(50 * time.Millisecond)
	b.Close()

	errOutput := errBuf.String()
	if !strings.Contains(errOutput, "handler failed") {
		t.Fatalf("expected error in output, got: %s", errOutput)
	}
}

// TestBuffered_CloseFlushesRemaining tests that Close flushes pending entries
func TestBuffered_CloseFlushesRemaining(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler, WithBatchSize(100))

	// Send entries without triggering flush
	for i := 0; i < 10; i++ {
		b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: fmt.Sprintf("msg %d", i)})
	}

	if handler.CallCount() != 0 {
		t.Fatal("should not have flushed yet")
	}

	b.Close()

	if handler.CallCount() != 1 {
		t.Fatalf("expected flush on close, got %d calls", handler.CallCount())
	}

	entries := handler.Entries()
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(entries))
	}
}

// TestBuffered_DoubleClose tests that double close is safe
func TestBuffered_CloseIdempotent(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler)

	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})

	err1 := b.Close()
	err2 := b.Close()

	if err1 != nil {
		t.Fatalf("first close failed: %v", err1)
	}
	// Second close should not panic, error is handler-dependent
	_ = err2

	if handler.CallCount() != 1 {
		t.Fatalf("expected 1 call, got %d", handler.CallCount())
	}
}

// TestBuffered_HandlerClose tests that underlying handler is closed
func TestBuffered_HandlerClose(t *testing.T) {
	closeHandler := &closeableHandler{}
	b := NewBuffered(closeHandler)

	b.Close()

	if !closeHandler.closed {
		t.Fatal("underlying handler should have been closed")
	}
}

type closeableHandler struct {
	closed bool
	mu     sync.Mutex
}

func (c *closeableHandler) Handle(e *lx.Entry) error {
	return nil
}

func (c *closeableHandler) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

// TestBuffered_ConcurrentAccess tests thread safety
func TestBuffered_ConcurrentAccess(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler, WithBatchSize(10))

	var wg sync.WaitGroup
	numGoroutines := 50
	entriesPerGoroutine := 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < entriesPerGoroutine; j++ {
				entry := &lx.Entry{
					Level:   lx.LevelInfo,
					Message: fmt.Sprintf("g%d-e%d", id, j),
				}
				if err := b.Handle(entry); err != nil {
					t.Errorf("Handle failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()
	b.Close()

	totalEntries := numGoroutines * entriesPerGoroutine
	entries := handler.Entries()
	if len(entries) != totalEntries {
		t.Fatalf("expected %d entries, got %d", totalEntries, len(entries))
	}
}

// TestBuffered_CloneEntry tests that entries are properly cloned
func TestBuffered_CloneEntry(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler, WithBatchSize(1))

	original := &lx.Entry{
		Level:     lx.LevelInfo,
		Message:   "original",
		Namespace: "test",
		Fields:    lx.Fields{{Key: "key1", Value: "value1"}, {Key: "key2", Value: "value2"}},
		Stack:     []byte("stack trace"),
	}

	b.Handle(original)

	// Modify original
	original.Message = "modified"
	original.Fields[0].Value = "modified"
	original.Stack[0] = 'X'

	time.Sleep(50 * time.Millisecond)
	b.Close()

	entries := handler.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	cloned := entries[0]
	if cloned.Message != "original" {
		t.Fatal("entry was not cloned - message modified")
	}
	if cloned.Fields[0].Value != "value1" {
		t.Fatal("entry was not cloned - fields modified")
	}
	if string(cloned.Stack) != "stack trace" {
		t.Fatal("entry was not cloned - stack modified")
	}
}

// TestBuffered_Config returns correct config
func TestBuffered_Config(t *testing.T) {
	handler := &mockHandlerBuffered{}
	b := NewBuffered(handler, WithBatchSize(42))

	cfg := b.Config()
	if cfg.BatchSize != 42 {
		t.Fatalf("expected batch size 42, got %d", cfg.BatchSize)
	}

	b.Close()
}

// TestBuffered_DefaultErrorOutput tests default error output
func TestBuffered_DefaultErrorOutput(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	handler := &mockHandlerBuffered{}
	handler.err = errors.New("test error")

	b := NewBuffered(handler, WithBatchSize(1), WithErrorOutput(nil))
	b.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})

	time.Sleep(50 * time.Millisecond)
	b.Close()

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)

	// Should have written to stderr
	if buf.Len() == 0 {
		t.Fatal("expected error output to stderr")
	}
}

// BenchmarkBuffered_Handle benchmarks entry handling
func BenchmarkBuffered_Handle(b *testing.B) {
	handler := &mockHandlerBuffered{}
	buf := NewBuffered(handler, WithBatchSize(1000), WithMaxBuffer(10000))

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "benchmark",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf.Handle(entry)
		}
	})

	buf.Close()
}

// BenchmarkBuffered_Flush benchmarks flush performance
func BenchmarkBuffered_Flush(b *testing.B) {
	for i := 0; i < b.N; i++ {
		handler := &mockHandlerBuffered{}
		buf := NewBuffered(handler, WithBatchSize(100))

		for j := 0; j < 100; j++ {
			buf.Handle(&lx.Entry{Level: lx.LevelInfo, Message: "test"})
		}

		buf.Close()
	}
}
