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
	MaxBuffer     int           // Maximum buffer size before applying backpressure (default: 1000)
	OnOverflow    func(int)     // Called when buffer reaches MaxBuffer (default: logs warning)
}

// BufferingOpt configures Buffered handler.
type BufferingOpt func(*Buffering)

// WithBatchSize sets the batch size for flushing.
func WithBatchSize(size int) BufferingOpt {
	return func(c *Buffering) {
		c.BatchSize = size
	}
}

// WithFlushInterval sets the maximum time between flushes.
func WithFlushInterval(d time.Duration) BufferingOpt {
	return func(c *Buffering) {
		c.FlushInterval = d
	}
}

// WithMaxBuffer sets the maximum buffer size before backpressure.
func WithMaxBuffer(size int) BufferingOpt {
	return func(c *Buffering) {
		c.MaxBuffer = size
	}
}

// WithOverflowHandler sets the overflow callback.
func WithOverflowHandler(fn func(int)) BufferingOpt {
	return func(c *Buffering) {
		c.OnOverflow = fn
	}
}

// Buffered wraps any Handler to provide buffering capabilities.
type Buffered[H lx.Handler] struct {
	handler      H
	config       *Buffering
	entries      chan *lx.Entry
	flushSignal  chan struct{}
	shutdown     chan struct{}
	shutdownOnce sync.Once
	wg           sync.WaitGroup
}

// NewBuffered creates a new buffered handler that wraps another handler.
func NewBuffered[H lx.Handler](handler H, opts ...BufferingOpt) *Buffered[H] {
	config := &Buffering{
		BatchSize:     100,
		FlushInterval: 10 * time.Second,
		MaxBuffer:     1000,
		OnOverflow: func(count int) {
			fmt.Fprintf(io.Discard, "log buffer overflow: %d entries\n", count)
		},
	}

	for _, opt := range opts {
		opt(config)
	}

	// Ensure sane values
	if config.BatchSize < 1 {
		config.BatchSize = 1
	}
	if config.MaxBuffer < config.BatchSize {
		config.MaxBuffer = config.BatchSize * 10
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = 10 * time.Second
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

// Handle implements lx.Handler interface.
func (b *Buffered[H]) Handle(e *lx.Entry) error {
	select {
	case b.entries <- e:
		return nil
	default:
		if b.config.OnOverflow != nil {
			b.config.OnOverflow(len(b.entries))
		}
		select {
		case b.flushSignal <- struct{}{}:
			return fmt.Errorf("log buffer overflow, triggering flush")
		default:
			return fmt.Errorf("log buffer overflow and flush already in progress")
		}
	}
}

// Flush triggers an immediate flush of buffered entries.
func (b *Buffered[H]) Flush() {
	select {
	case b.flushSignal <- struct{}{}:
	case <-time.After(100 * time.Millisecond):
		// Flush already pending
	}
}

// Close flushes any remaining entries and stops the worker.
func (b *Buffered[H]) Close() error {
	b.shutdownOnce.Do(func() {
		close(b.shutdown)
		b.wg.Wait()
		runtime.SetFinalizer(b, nil)
	})
	return nil
}

// Final ensures remaining entries are flushed during garbage collection.
func (b *Buffered[H]) Final() {
	b.Close()
}

func (b *Buffered[H]) Config() *Buffering {
	return b.config
}

// worker processes entries and handles flushing.
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
			b.drainRemaining() // Drain all entries from the channel
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
func (b *Buffered[H]) flushBatch(batch []*lx.Entry) {
	for _, entry := range batch {
		if err := b.handler.Handle(entry); err != nil {
			fmt.Fprintf(os.Stderr, "log flush error: %v\n", err) // Changed from io.Discard
		}
	}
}

// drainRemaining processes any remaining entries in the channel.
func (b *Buffered[H]) drainRemaining() {
	for {
		select {
		case entry := <-b.entries:
			if err := b.handler.Handle(entry); err != nil {
				fmt.Fprintf(os.Stderr, "log drain error: %v\n", err)
			}
		default:
			return
		}
	}
}
