// buffer_test.go
package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lx"
)

// errorWriter is an io.Writer that always returns an error.
type errorWriter struct {
	err error
}

func (w *errorWriter) Write(p []byte) (n int, err error) { return 0, w.err }

// safeBuffer wraps bytes.Buffer with a mutex so tests can safely read while
// background goroutines are writing.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitUntil polls until condition is true or timeout elapses.
// Helps avoid brittle sleeps while not requiring internal acks from the handler.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestBufferedHandler(t *testing.T) {
	t.Run("BasicFunctionality", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(2),
			lh.WithFlushInterval(100*time.Millisecond),
		)

		_ = handler.Handle(&lx.Entry{Message: "test1"})
		_ = handler.Handle(&lx.Entry{Message: "test2"})

		// Give the worker some time to flush (interval-based or batch-based).
		ok := waitUntil(500*time.Millisecond, func() bool {
			out := buf.String()
			return strings.Contains(out, "test1") && strings.Contains(out, "test2")
		})

		_ = handler.Close()

		output := buf.String()
		if !ok {
			t.Fatalf("Expected both messages in output, got: %q", output)
		}
	})

	t.Run("PeriodicFlushing", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(100),
			lh.WithFlushInterval(50*time.Millisecond),
		)

		_ = handler.Handle(&lx.Entry{Message: "test"})

		ok := waitUntil(500*time.Millisecond, func() bool {
			return strings.Contains(buf.String(), "test")
		})

		_ = handler.Close()

		if !ok {
			t.Fatalf("Expected message to be flushed after interval, got: %q", buf.String())
		}
	})

	t.Run("OverflowHandling", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)

		var overflowCalled atomic.Bool
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(2),
			lh.WithMaxBuffer(2),
			lh.WithOverflowHandler(func(int) { overflowCalled.Store(true) }),
		)
		defer handler.Close()

		_ = handler.Handle(&lx.Entry{Message: "test1"})
		_ = handler.Handle(&lx.Entry{Message: "test2"})

		err := handler.Handle(&lx.Entry{Message: "test3"})
		if err == nil {
			t.Fatal("Expected error on overflow")
		}
		if !overflowCalled.Load() {
			t.Fatal("Expected overflow handler to be called")
		}
	})

	t.Run("ExplicitFlush", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100))
		defer handler.Close()

		_ = handler.Handle(&lx.Entry{Message: "test"})
		handler.Flush()

		ok := waitUntil(500*time.Millisecond, func() bool {
			return strings.Contains(buf.String(), "test")
		})

		if !ok {
			t.Fatalf("Expected message to be flushed after explicit flush, got: %q", buf.String())
		}
	})

	t.Run("ShutdownDrainsBuffer", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100))

		_ = handler.Handle(&lx.Entry{Message: "test"})
		_ = handler.Close()

		if !strings.Contains(buf.String(), "test") {
			t.Fatalf("Expected message to be flushed on shutdown, got: %q", buf.String())
		}
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(100),
			lh.WithFlushInterval(10*time.Millisecond),
			lh.WithMaxBuffer(2000),
		)

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = handler.Handle(&lx.Entry{Message: fmt.Sprintf("test%d", i)})
			}(i)
		}
		wg.Wait()

		handler.Flush()
		_ = handler.Close() // Stop worker; should drain/flush remaining.

		output := buf.String()
		for i := 0; i < 100; i++ {
			if !strings.Contains(output, fmt.Sprintf("test%d", i)) {
				t.Fatalf("Missing message test%d in output", i)
			}
		}
	})

	t.Run("ErrorHandling", func(t *testing.T) {
		errWriter := &errorWriter{err: errors.New("write error")}
		textHandler := lh.NewTextHandler(errWriter)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(1))
		defer handler.Close()

		// Buffered handler should accept the entry; write error occurs during flush.
		if err := handler.Handle(&lx.Entry{Message: "test"}); err != nil {
			t.Fatalf("Unexpected error on Handle: %v", err)
		}

		handler.Flush()
		// No assertion here; this test is to ensure it doesn't race/panic.
		_ = waitUntil(300*time.Millisecond, func() bool { return true })
	})

	t.Run("Finalizer", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100))

		_ = handler.Handle(&lx.Entry{Message: "test"})

		// Ensure our test isn't relying on GC timing; call Final directly.
		runtime.SetFinalizer(handler, nil)
		handler.Final() // Calls Close()

		if !strings.Contains(buf.String(), "test") {
			t.Fatalf("Expected message to be flushed by finalizer, got: %q", buf.String())
		}
	})
}

func TestBufferedHandlerOptions(t *testing.T) {
	t.Run("DefaultValues", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&safeBuffer{})
		handler := lh.NewBuffered(textHandler)
		defer handler.Close()

		if handler.Config().BatchSize != 100 {
			t.Errorf("Expected default BatchSize=100, got %d", handler.Config().BatchSize)
		}
		if handler.Config().FlushInterval != 10*time.Second {
			t.Errorf("Expected default FlushInterval=10s, got %v", handler.Config().FlushInterval)
		}
		if handler.Config().MaxBuffer != 1000 {
			t.Errorf("Expected default MaxBuffer=1000, got %d", handler.Config().MaxBuffer)
		}
	})

	t.Run("CustomOptions", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&safeBuffer{})

		var called atomic.Bool
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(50),
			lh.WithFlushInterval(5*time.Second),
			lh.WithMaxBuffer(500),
			lh.WithOverflowHandler(func(int) { called.Store(true) }),
		)
		defer handler.Close()

		if handler.Config().BatchSize != 50 {
			t.Errorf("Expected BatchSize=50, got %d", handler.Config().BatchSize)
		}
		if handler.Config().FlushInterval != 5*time.Second {
			t.Errorf("Expected FlushInterval=5s, got %v", handler.Config().FlushInterval)
		}
		if handler.Config().MaxBuffer != 500 {
			t.Errorf("Expected MaxBuffer=500, got %d", handler.Config().MaxBuffer)
		}

		handler.Config().OnOverflow(1)
		if !called.Load() {
			t.Error("Expected overflow handler to be called")
		}
	})
}

func TestBufferedHandlerEdgeCases(t *testing.T) {
	t.Run("ZeroBatchSize", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&safeBuffer{})
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(0))
		defer handler.Close()

		if handler.Config().BatchSize != 1 {
			t.Errorf("Expected BatchSize to be adjusted to 1, got %d", handler.Config().BatchSize)
		}
	})

	t.Run("NegativeFlushInterval", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&safeBuffer{})
		handler := lh.NewBuffered(textHandler, lh.WithFlushInterval(-1*time.Second))
		defer handler.Close()

		if handler.Config().FlushInterval != 10*time.Second {
			t.Errorf("Expected FlushInterval to be adjusted to 10s, got %v", handler.Config().FlushInterval)
		}
	})

	t.Run("SmallMaxBuffer", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&safeBuffer{})
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(10),
			lh.WithMaxBuffer(5),
		)
		defer handler.Close()

		if handler.Config().MaxBuffer < handler.Config().BatchSize {
			t.Errorf("Expected MaxBuffer >= BatchSize, got %d < %d",
				handler.Config().MaxBuffer, handler.Config().BatchSize)
		}
	})
}

func TestBufferedHandlerIntegration(t *testing.T) {
	t.Run("WithTextHandler", func(t *testing.T) {
		buf := &safeBuffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(2),
			lh.WithFlushInterval(50*time.Millisecond),
		)

		_ = handler.Handle(&lx.Entry{Message: "message1"})
		_ = handler.Handle(&lx.Entry{Message: "message2"})

		ok := waitUntil(500*time.Millisecond, func() bool {
			out := buf.String()
			return strings.Contains(out, "message1") && strings.Contains(out, "message2")
		})

		_ = handler.Close()

		if !ok {
			t.Fatalf("Expected both messages in output, got: %q", buf.String())
		}
	})

	t.Run("WithJSONHandler", func(t *testing.T) {
		buf := &safeBuffer{}
		jsonHandler := lh.NewJSONHandler(buf)
		handler := lh.NewBuffered(jsonHandler, lh.WithBatchSize(2))

		_ = handler.Handle(&lx.Entry{Message: "message1"})
		_ = handler.Handle(&lx.Entry{Message: "message2"})
		handler.Flush()

		_ = handler.Close() // Ensure no concurrent writes during decode.

		dec := json.NewDecoder(strings.NewReader(buf.String()))
		count := 0
		for {
			var entry map[string]interface{}
			if err := dec.Decode(&entry); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 2 {
			t.Fatalf("Expected 2 JSON entries, got %d", count)
		}
	})

	t.Run("WithMultiHandler", func(t *testing.T) {
		buf1 := &safeBuffer{}
		buf2 := &safeBuffer{}
		multiHandler := lh.NewMultiHandler(
			lh.NewTextHandler(buf1),
			lh.NewJSONHandler(buf2),
		)
		handler := lh.NewBuffered(multiHandler, lh.WithBatchSize(3))

		_ = handler.Handle(&lx.Entry{Message: "test"})
		_ = handler.Handle(&lx.Entry{Message: "test"})
		_ = handler.Handle(&lx.Entry{Message: "test"})
		handler.Flush()

		_ = handler.Close()

		textOutput := buf1.String()
		if strings.Count(textOutput, "test") != 3 {
			t.Fatalf("Expected 3 messages in text output, got: %q", textOutput)
		}

		// Decode JSON safely after Close.
		dec := json.NewDecoder(strings.NewReader(buf2.String()))
		count := 0
		for {
			var entry map[string]interface{}
			if err := dec.Decode(&entry); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 3 {
			t.Fatalf("Expected 3 JSON entries, got %d", count)
		}
	})

	t.Run("ErrorLogging", func(t *testing.T) {
		errWriter := &errorWriter{err: errors.New("write error")}
		textHandler := lh.NewTextHandler(errWriter)

		oldStderr := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr = w

		handler := lh.NewBuffered(textHandler)

		var errOutput bytes.Buffer
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(&errOutput, r)
		}()

		_ = handler.Handle(&lx.Entry{Message: "message"})
		handler.Flush()

		// Close handler first to stop worker; then close pipe to end copy.
		_ = handler.Close()
		_ = w.Close()
		os.Stderr = oldStderr
		<-done

		if !strings.Contains(errOutput.String(), "write error") {
			t.Fatalf("Expected error to be logged to stderr, got: %q", errOutput.String())
		}
	})
}
