package lh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/olekukonko/ll/lx"
)

// mockWriteCloser is a test double for io.WriteCloser
type mockWriteCloser struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	closed   bool
	writeErr error
	closeErr error
}

func (m *mockWriteCloser) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, errors.New("write to closed writer")
	}
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	return m.buf.Write(p)
}

func (m *mockWriteCloser) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeErr != nil {
		return m.closeErr
	}
	m.closed = true
	return nil
}

func (m *mockWriteCloser) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

// mockHandler implements both lx.Handler and lx.Outputter
type mockHandler struct {
	mu        sync.Mutex
	output    io.Writer
	entries   []*lx.Entry
	handleErr error
}

func (m *mockHandler) Handle(e *lx.Entry) error {
	if m.handleErr != nil {
		return m.handleErr
	}
	m.mu.Lock()
	m.entries = append(m.entries, e)
	m.mu.Unlock()

	if m.output != nil {
		data := NewJSONHandler(m.output).Handle(e)
		_ = data
	}
	return nil
}

func (m *mockHandler) Output(w io.Writer) {
	m.mu.Lock()
	m.output = w
	m.mu.Unlock()
}

func (m *mockHandler) Entries() []*lx.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*lx.Entry, len(m.entries))
	copy(out, m.entries)
	return out
}

// TestNewRotating_Basic tests basic creation and initial open
func TestNewRotating_Basic(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	if r.out == nil {
		t.Fatal("expected out to be set")
	}
	if r.out.writtenBytes() != 0 {
		t.Fatalf("expected initial written to be 0, got %d", r.out.writtenBytes())
	}
}

// TestNewRotating_OpenError tests error handling when Open fails
func TestNewRotating_OpenError(t *testing.T) {
	handler := &mockHandler{}
	expectedErr := errors.New("open failed")

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return nil, expectedErr
		},
	}

	_, err := NewRotating(handler, 1024, src)
	if err != expectedErr {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

// TestNewRotating_ExistingFileSize tests that existing file size is preserved
func TestNewRotating_ExistingFileSize(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 500, nil
		},
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	if r.out.writtenBytes() != 500 {
		t.Fatalf("expected initial written to be 500, got %d", r.out.writtenBytes())
	}
}

// TestRotating_Handle_BelowMaxSize tests writing below rotation threshold
func TestRotating_Handle_BelowMaxSize(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	openCount := int32(0)
	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			atomic.AddInt32(&openCount, 1)
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
		Rotate: func() error {
			t.Fatal("Rotate should not be called")
			return nil
		},
	}

	r, err := NewRotating(handler, 1000, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "test message",
	}

	if err := r.Handle(entry); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	if atomic.LoadInt32(&openCount) != 1 {
		t.Fatalf("expected Open to be called once, got %d", openCount)
	}
}

// TestRotating_Handle_TriggersRotation tests rotation when maxSize exceeded
func TestRotating_Handle_TriggersRotation(t *testing.T) {
	var outputs []*mockWriteCloser
	handler := &mockHandler{}

	rotateCount := int32(0)
	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			m := &mockWriteCloser{}
			outputs = append(outputs, m)
			return m, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
		Rotate: func() error {
			atomic.AddInt32(&rotateCount, 1)
			return nil
		},
	}

	r, err := NewRotating(handler, 100, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	for i := 0; i < 10; i++ {
		entry := &lx.Entry{
			Level:   lx.LevelInfo,
			Message: strings.Repeat("x", 20),
		}
		if err := r.Handle(entry); err != nil {
			t.Fatalf("Handle failed: %v", err)
		}
	}

	if atomic.LoadInt32(&rotateCount) < 1 {
		t.Fatal("expected Rotate to be called at least once")
	}
	if len(outputs) < 2 {
		t.Fatalf("expected multiple outputs, got %d", len(outputs))
	}
}

// TestRotating_Handle_RotateError tests error handling when Rotate fails
func TestRotating_Handle_RotateError(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	rotateErr := errors.New("rotate failed")
	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 1000, nil
		},
		Rotate: func() error {
			return rotateErr
		},
	}

	r, err := NewRotating(handler, 100, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "test",
	}

	err = r.Handle(entry)
	if err != rotateErr {
		t.Fatalf("expected error %v, got %v", rotateErr, err)
	}
}

// TestRotating_Handle_OpenErrorDuringRotation tests error when reopening after rotation
func TestRotating_Handle_OpenErrorDuringRotation(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	openCount := 0
	openErr := errors.New("reopen failed")
	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			openCount++
			if openCount == 1 {
				return mockOut, nil
			}
			return nil, openErr
		},
		Size: func() (int64, error) {
			return 1000, nil
		},
		Rotate: func() error {
			return nil
		},
	}

	r, err := NewRotating(handler, 100, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "test",
	}

	err = r.Handle(entry)
	if err != openErr {
		t.Fatalf("expected error %v, got %v", openErr, err)
	}
}

// TestRotating_Handle_HandlerError tests that handler errors are propagated
func TestRotating_Handle_HandlerError(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}
	handler.handleErr = errors.New("handler failed")

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "test",
	}

	err = r.Handle(entry)
	if err != handler.handleErr {
		t.Fatalf("expected error %v, got %v", handler.handleErr, err)
	}
}

// TestRotating_Close tests proper cleanup
func TestRotating_Close(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if !mockOut.closed {
		t.Fatal("expected underlying writer to be closed")
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Double close failed: %v", err)
	}
}

// TestRotating_CloseError tests error propagation from close
func TestRotating_CloseError(t *testing.T) {
	mockOut := &mockWriteCloser{}
	mockOut.closeErr = errors.New("close failed")
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}

	err = r.Close()
	if err != mockOut.closeErr {
		t.Fatalf("expected error %v, got %v", mockOut.closeErr, err)
	}
}

// TestRotating_Written tests the Written() method
func TestRotating_Written(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 100, nil
		},
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	if r.Written() != 100 {
		t.Fatalf("expected Written() to return 100, got %d", r.Written())
	}
}

// TestRotating_Written_NilOutput tests Written() when output is nil
func TestRotating_Written_NilOutput(t *testing.T) {
	handler := &mockHandler{}

	r := &Rotating[*mockHandler]{
		maxSize: 1024,
		handler: handler,
		out:     nil,
	}

	if r.Written() != 0 {
		t.Fatalf("expected Written() to return 0 when out is nil, got %d", r.Written())
	}
}

// TestRotating_DisabledRotation tests that maxSize <= 0 disables rotation
func TestRotating_DisabledRotation(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	rotateCalled := false
	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 999999, nil
		},
		Rotate: func() error {
			rotateCalled = true
			return nil
		},
	}

	r, err := NewRotating(handler, 0, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: strings.Repeat("x", 1000),
	}

	if err := r.Handle(entry); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	if rotateCalled {
		t.Fatal("Rotate should not be called when maxSize <= 0")
	}
}

// TestRotating_NoOpenCallback tests behavior when Open is nil
func TestRotating_NoOpenCallback(t *testing.T) {
	handler := &mockHandler{}

	src := RotateSource{
		Open: nil,
		Size: func() (int64, error) {
			return 0, nil
		},
	}

	_, err := NewRotating(handler, 1024, src)
	if err == nil {
		t.Fatal("expected error when Open is nil")
	}
}

// TestRotating_NoSizeCallback tests rotation when Size is nil
func TestRotating_NoSizeCallback(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: nil,
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	if r.out.writtenBytes() != 0 {
		t.Fatalf("expected written to be 0 when Size is nil, got %d", r.out.writtenBytes())
	}
}

// TestRotating_SizeError tests handling of Size() error
func TestRotating_SizeError(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, errors.New("stat failed")
		},
	}

	r, err := NewRotating(handler, 1024, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	if r.out.writtenBytes() != 0 {
		t.Fatalf("expected written to be 0 on Size error, got %d", r.out.writtenBytes())
	}
}

// TestRotating_ConcurrentAccess tests thread safety
func TestRotating_ConcurrentAccess(t *testing.T) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
	}

	r, err := NewRotating(handler, 100000, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	var wg sync.WaitGroup
	numGoroutines := 100
	numEntries := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numEntries; j++ {
				entry := &lx.Entry{
					Level:   lx.LevelInfo,
					Message: fmt.Sprintf("goroutine %d entry %d", id, j),
				}
				if err := r.Handle(entry); err != nil {
					t.Errorf("Handle failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	entries := handler.Entries()
	expected := numGoroutines * numEntries
	if len(entries) != expected {
		t.Fatalf("expected %d entries, got %d", expected, len(entries))
	}

	written := r.Written()
	if written <= 0 {
		t.Fatalf("expected positive written bytes, got %d", written)
	}
}

// TestRotating_ConcurrentWithRotation tests thread safety during rotation
func TestRotating_ConcurrentWithRotation(t *testing.T) {
	var outputs []*mockWriteCloser
	var mu sync.Mutex
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			mu.Lock()
			defer mu.Unlock()
			m := &mockWriteCloser{}
			outputs = append(outputs, m)
			return m, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
		Rotate: func() error {
			return nil
		},
	}

	r, err := NewRotating(handler, 500, src)
	if err != nil {
		t.Fatalf("NewRotating failed: %v", err)
	}
	defer r.Close()

	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				entry := &lx.Entry{
					Level:   lx.LevelInfo,
					Message: strings.Repeat(fmt.Sprintf("g%d-e%d", id, j), 50),
				}
				if err := r.Handle(entry); err != nil {
					t.Errorf("Handle failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	mu.Lock()
	numOutputs := len(outputs)
	mu.Unlock()

	if numOutputs < 2 {
		t.Fatalf("expected multiple outputs due to rotation, got %d", numOutputs)
	}
}

// TestTrackingWriter_WriteError tests that errors don't affect written count
func TestTrackingWriter_WriteError(t *testing.T) {
	mockOut := &mockWriteCloser{}
	mockOut.writeErr = errors.New("write failed")

	tw := &trackingWriter{
		WriteCloser: mockOut,
		written:     0,
	}

	n, err := tw.Write([]byte("test"))
	if err != mockOut.writeErr {
		t.Fatalf("expected error %v, got %v", mockOut.writeErr, err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written on error, got %d", n)
	}
	if tw.writtenBytes() != 0 {
		t.Fatalf("expected written to remain 0 on error, got %d", tw.writtenBytes())
	}
}

// TestTrackingWriter_PartialWrite tests partial write handling
func TestTrackingWriter_PartialWrite(t *testing.T) {
	partialWriter := &partialWriteCloser{
		maxWrite: 5,
	}

	tw := &trackingWriter{
		WriteCloser: partialWriter,
		written:     0,
	}

	data := []byte("hello world")
	n, err := tw.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes written, got %d", n)
	}
	if tw.writtenBytes() != 5 {
		t.Fatalf("expected written to be 5, got %d", tw.writtenBytes())
	}
}

// partialWriteCloser simulates a writer that only writes partial data
type partialWriteCloser struct {
	buf      bytes.Buffer
	maxWrite int
	closed   bool
}

func (p *partialWriteCloser) Write(data []byte) (n int, err error) {
	if p.closed {
		return 0, errors.New("closed")
	}
	toWrite := len(data)
	if toWrite > p.maxWrite {
		toWrite = p.maxWrite
	}
	return p.buf.Write(data[:toWrite])
}

func (p *partialWriteCloser) Close() error {
	p.closed = true
	return nil
}

// BenchmarkRotating_Handle benchmarks the Handle method
func BenchmarkRotating_Handle(b *testing.B) {
	mockOut := &mockWriteCloser{}
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			return mockOut, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
	}

	r, _ := NewRotating(handler, 1<<30, src)
	defer r.Close()

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: "benchmark message",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r.Handle(entry)
		}
	})
}

// BenchmarkRotating_HandleWithRotation benchmarks rotation overhead
func BenchmarkRotating_HandleWithRotation(b *testing.B) {
	var outputs []io.WriteCloser
	handler := &mockHandler{}

	src := RotateSource{
		Open: func() (io.WriteCloser, error) {
			m := &mockWriteCloser{}
			outputs = append(outputs, m)
			return m, nil
		},
		Size: func() (int64, error) {
			return 0, nil
		},
		Rotate: func() error {
			return nil
		},
	}

	r, _ := NewRotating(handler, 1000, src)
	defer r.Close()

	entry := &lx.Entry{
		Level:   lx.LevelInfo,
		Message: strings.Repeat("x", 100),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Handle(entry)
	}
}
