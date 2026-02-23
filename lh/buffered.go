package lh

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/olekukonko/ll/lx"
)

// Buffering holds configuration for the Buffered handler.
type Buffering struct {
	BatchSize     int           // Flush when this many entries are buffered (default: 100)
	FlushInterval time.Duration // Maximum time between flushes (default: 10s)
	FlushTimeout  time.Duration // FlushTimeout specifies the duration to wait for a flush attempt to complete before timing out.
	MaxBuffer     int           // Maximum buffer size before applying backpressure (default: 1000)
	OnOverflow    func(int)     // Called when buffer reaches MaxBuffer (default: logs warning)
	ErrorOutput   io.Writer     // Destination for internal errors like flush failures (default: os.Stderr)

}

// BufferingOpt configures Buffered handler.
type BufferingOpt func(*Buffering)

// WithBatchSize sets the batch size for flushing.
// It specifies the number of log entries to buffer before flushing to the underlying handler.
// Example:
//
//	handler := NewBuffered(textHandler, WithBatchSize(50)) // Flush every 50 entries
func WithBatchSize(size int) BufferingOpt {
	return func(c *Buffering) {
		c.BatchSize = size
	}
}

// WithFlushInterval sets the maximum time between flushes.
// It defines the interval at which buffered entries are flushed, even if the batch size is not reached.
// Example:
//
//	handler := NewBuffered(textHandler, WithFlushInterval(5*time.Second)) // Flush every 5 seconds
func WithFlushInterval(d time.Duration) BufferingOpt {
	return func(c *Buffering) {
		c.FlushInterval = d
	}
}

// WithFlushTimeout sets the maximum time between flushes.
// It defines the interval at which buffered entries are flushed, even if the batch size is not reached.
// Example:
//
//	handler := NewBuffered(textHandler, WithFlushTimeout(100*time.Millisecond)) // Flush every 5 seconds
func WithFlushTimeout(d time.Duration) BufferingOpt {
	return func(c *Buffering) {
		c.FlushTimeout = d
	}
}

// WithMaxBuffer sets the maximum buffer size before backpressure.
// It limits the number of entries that can be queued in the channel, triggering overflow handling if exceeded.
// Example:
//
//	handler := NewBuffered(textHandler, WithMaxBuffer(500)) // Allow up to 500 buffered entries
func WithMaxBuffer(size int) BufferingOpt {
	return func(c *Buffering) {
		c.MaxBuffer = size
	}
}

// WithOverflowHandler sets the overflow callback.
// It specifies a function to call when the buffer reaches MaxBuffer, typically for logging or metrics.
// Example:
//
//	handler := NewBuffered(textHandler, WithOverflowHandler(func(n int) { fmt.Printf("Overflow: %d entries\n", n) }))
func WithOverflowHandler(fn func(int)) BufferingOpt {
	return func(c *Buffering) {
		c.OnOverflow = fn
	}
}

// WithErrorOutput sets the destination for internal errors (e.g., downstream handler failures).
// Defaults to os.Stderr if not set.
// Example:
//
//	// Redirect internal errors to a file or discard them
//	handler := NewBuffered(textHandler, WithErrorOutput(os.Stdout))
func WithErrorOutput(w io.Writer) BufferingOpt {
	return func(c *Buffering) {
		c.ErrorOutput = w
	}
}

// Buffered wraps any Handler to provide buffering capabilities.
// It buffers log entries in a channel and flushes them based on batch size, time interval, or explicit flush.
// The generic type H ensures compatibility with any lx.Handler implementation.
// Thread-safe via channels and sync primitives.
type Buffered[H lx.Handler] struct {
	handler      H              // Underlying handler to process log entries
	config       *Buffering     // Configuration for batching and flushing
	entries      chan *lx.Entry // Channel for buffering log entries
	flushSignal  chan struct{}  // Channel to trigger explicit flushes
	shutdown     chan struct{}  // Channel to signal worker shutdown
	shutdownOnce sync.Once      // Ensures Close is called only once
	wg           sync.WaitGroup // Waits for worker goroutine to finish
}

// NewBuffered creates a new buffered handler that wraps another handler.
// It initializes the handler with default or provided configuration options and starts a worker goroutine.
// Thread-safe via channel operations and finalizer for cleanup.
// Example:
//
//	textHandler := lh.NewTextHandler(os.Stdout)
//	buffered := NewBuffered(textHandler, WithBatchSize(50))
func NewBuffered[H lx.Handler](handler H, opts ...BufferingOpt) *Buffered[H] {
	config := &Buffering{
		BatchSize:     100,
		FlushInterval: 10 * time.Second,
		MaxBuffer:     1000,
		ErrorOutput:   os.Stderr,
		OnOverflow: func(count int) {
			fmt.Fprintf(io.Discard, "log buffer overflow: %d entries\n", count)
		},
	}

	for _, opt := range opts {
		opt(config)
	}

	if config.BatchSize < 1 {
		config.BatchSize = 1
	}
	if config.MaxBuffer < config.BatchSize {
		config.MaxBuffer = config.BatchSize * 10
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = 10 * time.Second
	}

	if config.FlushTimeout <= 0 {
		config.FlushTimeout = 100 * time.Millisecond
	}

	if config.ErrorOutput == nil {
		config.ErrorOutput = os.Stderr
	}

	b := &Buffered[H]{
		handler:     handler,
		config:      config,
		entries:     make(chan *lx.Entry, config.MaxBuffer),
		flushSignal: make(chan struct{}, 1),
		shutdown:    make(chan struct{}),
	}

	b.wg.Add(1)
	go b.worker()

	runtime.SetFinalizer(b, (*Buffered[H]).Final)
	return b
}

// cloneEntry creates a deep copy of an entry for safe asynchronous processing.
// The original entry belongs to the logger's pool and is reused immediately after Handle() returns.
func (b *Buffered[H]) cloneEntry(e *lx.Entry) *lx.Entry {
	entryCopy := &lx.Entry{
		Timestamp: e.Timestamp,
		Level:     e.Level,
		Message:   e.Message,
		Namespace: e.Namespace,
		Style:     e.Style,
		Class:     e.Class,
		Error:     e.Error,
		Id:        e.Id,
	}

	if len(e.Fields) > 0 {
		entryCopy.Fields = make(lx.Fields, len(e.Fields))
		copy(entryCopy.Fields, e.Fields)
	}

	if len(e.Stack) > 0 {
		entryCopy.Stack = make([]byte, len(e.Stack))
		copy(entryCopy.Stack, e.Stack)
	}

	return entryCopy
}

// Handle implements the lx.Handler interface.
// It buffers log entries in the entries channel or triggers a flush on overflow.
// Returns an error if the buffer is full and flush cannot be triggered.
// Thread-safe via non-blocking channel operations.
// Example:
//
//	buffered.Handle(&lx.Entry{Message: "test"}) // Buffers entry or triggers flush
func (b *Buffered[H]) Handle(e *lx.Entry) error {
	entryCopy := b.cloneEntry(e)

	select {
	case b.entries <- entryCopy:
		return nil
	default:
		if b.config.OnOverflow != nil {
			b.config.OnOverflow(len(b.entries))
		}

		select {
		case b.flushSignal <- struct{}{}:
			select {
			case b.entries <- entryCopy:
				return nil
			default:
				return fmt.Errorf("log buffer overflow, flush triggered but still full")
			}
		default:
			return fmt.Errorf("log buffer overflow and flush already in progress")
		}
	}
}

// Flush triggers an immediate flush of buffered entries.
// If a flush is already pending, it waits briefly and may exit without flushing.
// Thread-safe via non-blocking channel operations.
// Example:
//
//	buffered.Flush() // Flushes all buffered entries
func (b *Buffered[H]) Flush() {
	select {
	case b.flushSignal <- struct{}{}:
	case <-time.After(b.config.FlushTimeout):
	}
}

// Close flushes any remaining entries and stops the worker.
// It ensures shutdown is performed only once and waits for the worker to finish.
// If the underlying handler implements a Close() error method, it will be called to release resources.
// Thread-safe via sync.Once and WaitGroup.
// Returns any error from the underlying handler's Close, or nil.
// Example:
//
//	buffered.Close() // Flushes entries and stops worker
func (b *Buffered[H]) Close() error {
	var closeErr error
	b.shutdownOnce.Do(func() {
		close(b.shutdown)
		b.wg.Wait()
		runtime.SetFinalizer(b, nil)

		if closer, ok := any(b.handler).(interface{ Close() error }); ok {
			closeErr = closer.Close()
		}
	})
	return closeErr
}

// Final ensures remaining entries are flushed during garbage collection.
// It calls Close to flush entries and stop the worker.
// Used as a runtime finalizer to prevent log loss.
// Example (internal usage):
//
//	runtime.SetFinalizer(buffered, (*Buffered[H]).Final)
func (b *Buffered[H]) Final() {
	b.Close()
}

// Config returns the current configuration of the Buffered handler.
// It provides access to BatchSize, FlushInterval, MaxBuffer, and OnOverflow settings.
// Example:
//
//	config := buffered.Config() // Access configuration
func (b *Buffered[H]) Config() *Buffering {
	return b.config
}

// worker processes entries and handles flushing.
// It runs in a goroutine, buffering entries, flushing on batch size, timer, or explicit signal,
// and shutting down cleanly when signaled.
// Thread-safe via channel operations and WaitGroup.
func (b *Buffered[H]) worker() {
	defer b.wg.Done()
	batch := make([]*lx.Entry, 0, b.config.BatchSize)
	ticker := time.NewTicker(b.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case entry := <-b.entries:
			batch = append(batch, entry)
			if len(batch) >= b.config.BatchSize {
				b.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				b.flushBatch(batch)
				batch = batch[:0]
			}
		case <-b.flushSignal:
			if len(batch) > 0 {
				b.flushBatch(batch)
				batch = batch[:0]
			}
			b.drainRemaining()
		case <-b.shutdown:
			if len(batch) > 0 {
				b.flushBatch(batch)
			}
			b.drainRemaining()
			return
		}
	}
}

// flushBatch processes a batch of entries through the wrapped handler.
// It writes each entry to the underlying handler, logging any errors to the configured ErrorOutput.
// Example (internal usage):
//
//	b.flushBatch([]*lx.Entry{entry1, entry2})
func (b *Buffered[H]) flushBatch(batch []*lx.Entry) {
	for _, entry := range batch {
		if err := b.handler.Handle(entry); err != nil {
			if b.config.ErrorOutput != nil {
				fmt.Fprintf(b.config.ErrorOutput, "log flush error: %v\n", err)
			}
		}
	}
}

// drainRemaining processes any remaining entries in the channel.
// It flushes all entries from the entries channel to the underlying handler,
// logging any errors to the configured ErrorOutput. Used during flush or shutdown.
// Example (internal usage):
//
//	b.drainRemaining() // Flushes all pending entries
func (b *Buffered[H]) drainRemaining() {
	for {
		select {
		case entry := <-b.entries:
			if err := b.handler.Handle(entry); err != nil {
				if b.config.ErrorOutput != nil {
					fmt.Fprintf(b.config.ErrorOutput, "log drain error: %v\n", err)
				}
			}
		default:
			return
		}
	}
}
