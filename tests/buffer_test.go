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
	"testing"
	"time"

	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lx"
)

// errorWriter is an io.Writer that always returns an error
type errorWriter struct {
	err error
}

func (w *errorWriter) Write(p []byte) (n int, err error) {
	return 0, w.err
}

func TestBufferedHandler(t *testing.T) {
	t.Run("BasicFunctionality", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(2), lh.WithFlushInterval(100*time.Millisecond))
		defer handler.Close()

		handler.Handle(&lx.Entry{Message: "test1"})
		handler.Handle(&lx.Entry{Message: "test2"})

		// Wait for batch flush
		time.Sleep(150 * time.Millisecond)
		output := buf.String()
		if !strings.Contains(output, "test1") || !strings.Contains(output, "test2") {
			t.Errorf("Expected both messages in output, got: %q", output)
		}
	})

	t.Run("PeriodicFlushing", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100), lh.WithFlushInterval(50*time.Millisecond))
		defer handler.Close()

		handler.Handle(&lx.Entry{Message: "test"})

		// Should flush after interval even though batch size not reached
		time.Sleep(75 * time.Millisecond)
		if !strings.Contains(buf.String(), "test") {
			t.Error("Expected message to be flushed after interval")
		}
	})

	t.Run("OverflowHandling", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		var overflowCalled bool
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(2),
			lh.WithMaxBuffer(2),
			lh.WithOverflowHandler(func(int) { overflowCalled = true }),
		)
		defer handler.Close()

		// Fill buffer
		handler.Handle(&lx.Entry{Message: "test1"})
		handler.Handle(&lx.Entry{Message: "test2"})

		// This should trigger overflow
		err := handler.Handle(&lx.Entry{Message: "test3"})
		if err == nil {
			t.Error("Expected error on overflow")
		}
		if !overflowCalled {
			t.Error("Expected overflow handler to be called")
		}
	})

	t.Run("ExplicitFlush", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100))
		defer handler.Close()

		handler.Handle(&lx.Entry{Message: "test"})
		handler.Flush()
		time.Sleep(10 * time.Millisecond) // Allow worker to process
		if !strings.Contains(buf.String(), "test") {
			t.Error("Expected message to be flushed after explicit flush")
		}
	})

	t.Run("ShutdownDrainsBuffer", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100))

		handler.Handle(&lx.Entry{Message: "test"})
		handler.Close()

		if !strings.Contains(buf.String(), "test") {
			t.Error("Expected message to be flushed on shutdown")
		}
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100), lh.WithFlushInterval(10*time.Millisecond), lh.WithMaxBuffer(1000))
		defer handler.Close()

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				handler.Handle(&lx.Entry{Message: fmt.Sprintf("test%d", i)})
			}(i)
		}
		wg.Wait()
		handler.Flush()
		time.Sleep(50 * time.Millisecond) // Allow worker to process
		output := buf.String()
		t.Logf("Buffer output length: %d", len(output)) // Debug output
		for i := 0; i < 100; i++ {
			if !strings.Contains(output, fmt.Sprintf("test%d", i)) {
				t.Errorf("Missing message test%d in output", i)
			}
		}
	})

	t.Run("ErrorHandling", func(t *testing.T) {
		errWriter := &errorWriter{err: errors.New("write error")}
		textHandler := lh.NewTextHandler(errWriter)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(1))
		defer handler.Close()

		err := handler.Handle(&lx.Entry{Message: "test"})
		if err != nil {
			t.Errorf("Unexpected error on Handle: %v", err)
		}

		// Wait for flush to occur
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("Finalizer", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(100))
		handler.Handle(&lx.Entry{Message: "test"})

		// Simulate GC (in real code this would happen automatically)
		runtime.SetFinalizer(handler, nil) // Remove the finalizer for test
		handler.Final()

		if !strings.Contains(buf.String(), "test") {
			t.Error("Expected message to be flushed by finalizer")
		}
	})
}

func TestBufferedHandlerOptions(t *testing.T) {
	t.Run("DefaultValues", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&bytes.Buffer{})
		handler := lh.NewBuffered(textHandler)
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
		textHandler := lh.NewTextHandler(&bytes.Buffer{})
		called := false
		handler := lh.NewBuffered(textHandler,
			lh.WithBatchSize(50),
			lh.WithFlushInterval(5*time.Second),
			lh.WithMaxBuffer(500),
			lh.WithOverflowHandler(func(int) { called = true }),
		)

		if handler.Config().BatchSize != 50 {
			t.Errorf("Expected BatchSize=50, got %d", handler.Config().BatchSize)
		}
		if handler.Config().FlushInterval != 5*time.Second {
			t.Errorf("Expected FlushInterval=5s, got %v", handler.Config().FlushInterval)
		}
		if handler.Config().MaxBuffer != 500 {
			t.Errorf("Expected MaxBuffer=500, got %d", handler.Config().MaxBuffer)
		}

		// Test overflow handler
		handler.Config().OnOverflow(1)
		if !called {
			t.Error("Expected overflow handler to be called")
		}
	})
}

func TestBufferedHandlerEdgeCases(t *testing.T) {
	t.Run("ZeroBatchSize", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&bytes.Buffer{})
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(0))
		if handler.Config().BatchSize != 1 {
			t.Errorf("Expected BatchSize to be adjusted to 1, got %d", handler.Config().BatchSize)
		}
	})

	t.Run("NegativeFlushInterval", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&bytes.Buffer{})
		handler := lh.NewBuffered(textHandler, lh.WithFlushInterval(-1*time.Second))
		if handler.Config().FlushInterval != 10*time.Second {
			t.Errorf("Expected FlushInterval to be adjusted to 10s, got %v", handler.Config().FlushInterval)
		}
	})

	t.Run("SmallMaxBuffer", func(t *testing.T) {
		textHandler := lh.NewTextHandler(&bytes.Buffer{})
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(10), lh.WithMaxBuffer(5))
		if handler.Config().MaxBuffer < handler.Config().BatchSize {
			t.Errorf("Expected MaxBuffer >= BatchSize, got %d < %d",
				handler.Config().MaxBuffer, handler.Config().BatchSize)
		}
	})
}

func TestBufferedHandlerIntegration(t *testing.T) {
	t.Run("WithTextHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		textHandler := lh.NewTextHandler(buf)
		handler := lh.NewBuffered(textHandler, lh.WithBatchSize(2), lh.WithFlushInterval(50*time.Millisecond))
		defer handler.Close()

		handler.Handle(&lx.Entry{Message: "message1"})
		handler.Handle(&lx.Entry{Message: "message2"})

		// Wait for flush
		time.Sleep(75 * time.Millisecond)
		output := buf.String()
		if !strings.Contains(output, "message1") || !strings.Contains(output, "message2") {
			t.Errorf("Expected both messages in output, got: %q", output)
		}
	})

	t.Run("WithJSONHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		jsonHandler := lh.NewJSONHandler(buf)
		handler := lh.NewBuffered(jsonHandler, lh.WithBatchSize(2))
		defer handler.Close()

		handler.Handle(&lx.Entry{Message: "message1"})
		handler.Handle(&lx.Entry{Message: "message2"})
		handler.Flush()
		time.Sleep(50 * time.Millisecond) // Allow worker to process
		var count int
		dec := json.NewDecoder(buf)
		for {
			var entry map[string]interface{}
			if err := dec.Decode(&entry); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			count++
		}
		t.Logf("JSON entry count: %d", count) // Debug output
		if count != 2 {
			t.Errorf("Expected 2 JSON entries, got %d", count)
		}
	})

	t.Run("WithMultiHandler", func(t *testing.T) {
		buf1 := &bytes.Buffer{}
		buf2 := &bytes.Buffer{}
		multiHandler := lh.NewMultiHandler(
			lh.NewTextHandler(buf1),
			lh.NewJSONHandler(buf2),
		)
		handler := lh.NewBuffered(multiHandler, lh.WithBatchSize(3))
		defer handler.Close()

		handler.Handle(&lx.Entry{Message: "test"})
		handler.Handle(&lx.Entry{Message: "test"})
		handler.Handle(&lx.Entry{Message: "test"})
		handler.Flush()
		time.Sleep(10 * time.Millisecond) // Allow worker to process

		textOutput := buf1.String()
		t.Logf("Text output: %q", textOutput)
		if strings.Count(textOutput, "test") != 3 {
			t.Error("Expected 3 messages in text output")
		}

		var count int
		dec := json.NewDecoder(buf2)
		for dec.More() {
			var entry map[string]interface{}
			if err := dec.Decode(&entry); err != nil {
				t.Fatal(err)
			}
			count++
		}
		t.Logf("JSON entry count: %d", count)
		if count != 3 {
			t.Errorf("Expected 3 JSON entries, got %d", count)
		}
	})

	t.Run("ErrorLogging", func(t *testing.T) {
		errWriter := &errorWriter{err: errors.New("write error")}
		textHandler := lh.NewTextHandler(errWriter)

		// Set up stderr capture before creating the handler
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		handler := lh.NewBuffered(textHandler)

		var errOutput bytes.Buffer
		errChan := make(chan struct{})
		go func() {
			defer close(errChan)
			io.Copy(&errOutput, r)
		}()

		handler.Handle(&lx.Entry{Message: "message"})
		handler.Flush()

		// Give time for the flush to occur
		time.Sleep(50 * time.Millisecond)

		// Clean up
		handler.Close()
		w.Close()
		os.Stderr = oldStderr
		<-errChan

		if !strings.Contains(errOutput.String(), "write error") {
			t.Errorf("Expected error to be logged to stderr, got: %q", errOutput.String())
		}
	})
}
