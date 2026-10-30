package victoria

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olekukonko/ll/lx"
)

// batchRecorder counts requests and the log lines received in them.
type batchRecorder struct {
	mu       sync.Mutex
	requests int
	lines    []string
}

func (b *batchRecorder) handler(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests++
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			b.lines = append(b.lines, line)
		}
	}
	b.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (b *batchRecorder) snapshot() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests, len(b.lines)
}

func newTestEntry(msg string) *lx.Entry {
	return &lx.Entry{
		Timestamp: time.Now(),
		Level:     lx.LevelInfo,
		Message:   msg,
		Namespace: "test.batch",
	}
}

// TestUnbatchedSendsOneRequestPerEntry guarantees the default (no WithBatching)
// behaviour is unchanged: one HTTP request per log entry, synchronously.
func TestUnbatchedSendsOneRequestPerEntry(t *testing.T) {
	rec := &batchRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	v, err := New(WithURL(srv.URL))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer v.Close()

	const n = 5
	for i := 0; i < n; i++ {
		if err := v.Handle(newTestEntry("sync")); err != nil {
			t.Fatalf("Handle() error: %v", err)
		}
	}

	reqs, lines := rec.snapshot()
	if reqs != n {
		t.Fatalf("expected %d requests without batching, got %d", n, reqs)
	}
	if lines != n {
		t.Fatalf("expected %d lines, got %d", n, lines)
	}
}

// TestBatchingFlushesOnSize verifies a full batch becomes a single request.
func TestBatchingFlushesOnSize(t *testing.T) {
	rec := &batchRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	v, err := New(WithURL(srv.URL), WithBatching(10, time.Hour))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer v.Close()

	for i := 0; i < 10; i++ {
		if err := v.Handle(newTestEntry("batched")); err != nil {
			t.Fatalf("Handle() error: %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if reqs, lines := rec.snapshot(); reqs == 1 && lines == 10 {
			return
		}
		if time.Now().After(deadline) {
			reqs, lines := rec.snapshot()
			t.Fatalf("expected 1 request with 10 lines, got %d requests / %d lines", reqs, lines)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestBatchingFlushesOnInterval verifies a partial batch is flushed by the timer.
func TestBatchingFlushesOnInterval(t *testing.T) {
	rec := &batchRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	v, err := New(WithURL(srv.URL), WithBatching(100, 20*time.Millisecond))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer v.Close()

	if err := v.Handle(newTestEntry("partial")); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, lines := rec.snapshot(); lines == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("partial batch was not flushed on the interval")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestBatchingFlushesOnClose verifies Close drains the queue.
func TestBatchingFlushesOnClose(t *testing.T) {
	rec := &batchRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	v, err := New(WithURL(srv.URL), WithBatching(1000, time.Hour))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := v.Handle(newTestEntry("pending")); err != nil {
			t.Fatalf("Handle() error: %v", err)
		}
	}
	if err := v.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if _, lines := rec.snapshot(); lines != 3 {
		t.Fatalf("expected 3 lines flushed on Close, got %d", lines)
	}
	// Close must be idempotent.
	if err := v.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}

// TestBatchingOverflowNeverBlocks verifies Handle rejects entries instead of
// blocking the caller when the queue is full.
func TestBatchingOverflowNeverBlocks(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release: // hold the worker so the queue fills up
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	v, err := New(WithURL(srv.URL), WithBatching(2, time.Hour))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	done := make(chan struct{})
	var rejected int
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if err := v.Handle(newTestEntry("overflow")); err != nil {
				rejected++
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		close(release)
		t.Fatalf("Handle() blocked while the queue was full")
	}
	close(release)
	if rejected == 0 {
		t.Fatalf("expected some entries to be rejected by the full queue")
	}
	_ = v.Close()
}

// TestBatchingConcurrentHandles exercises the queue under concurrency and
// verifies the accounting invariant: every accepted entry is delivered, and
// every rejected one is reported (nothing is silently lost).
func TestBatchingConcurrentHandles(t *testing.T) {
	rec := &batchRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	var rejected atomic.Int64
	v, err := New(
		WithURL(srv.URL),
		WithBatching(25, 10*time.Millisecond),
		WithErrorHandler(func(error) { rejected.Add(1) }),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	const goroutines = 8
	const perGoroutine = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = v.Handle(newTestEntry("concurrent"))
			}
		}()
	}
	wg.Wait()
	if err := v.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	total := goroutines * perGoroutine
	_, delivered := rec.snapshot()
	if dropped := int(rejected.Load()); delivered+dropped != total {
		t.Fatalf("accounting mismatch: delivered=%d rejected=%d total=%d", delivered, dropped, total)
	}
	if delivered == 0 {
		t.Fatal("no entries were delivered")
	}
}

// TestClientErrorNotRetried verifies 4xx responses short-circuit the retry loop.
func TestClientErrorNotRetried(t *testing.T) {
	var requests int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	v, err := New(WithURL(srv.URL), WithRetry(3))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := v.Handle(newTestEntry("bad")); err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("4xx must not be retried: got %d requests", requests)
	}
}
