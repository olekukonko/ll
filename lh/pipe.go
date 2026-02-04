package lh

import (
	"time"

	"github.com/olekukonko/ll/lx"
)

// Pipe chains multiple handler wrappers together, applying them from left to right.
// The wrappers are composed such that the first wrapper in the list becomes
// the innermost layer, and the last wrapper becomes the outermost layer.
//
// Usage pattern: Pipe(baseHandler, wrapper1, wrapper2, wrapper3)
// Result: wrapper3(wrapper2(wrapper1(baseHandler)))
//
// This enables clean, declarative construction of handler middleware chains.
//
// Example - building a processing pipeline:
//
//	base := lx.NewJSONHandler(os.Stdout)
//	handler := lh.Pipe(base,
//	    lh.NewDedup(2*time.Second),    // 1. Deduplicate first
//	    lh.NewRateLimit(10, time.Second), // 2. Then rate limit
//	    lh.AddTimestamp(),              // 3. Then add timestamps
//	)
//	logger := lx.NewLogger(handler)
//
// In this example, logs flow: Dedup → RateLimit → AddTimestamp → JSONHandler
func Pipe(h lx.Handler, wraps ...lx.Wrap) lx.Handler {
	for _, w := range wraps {
		if w != nil {
			h = w(h)
		}
	}
	return h
}

func PipeDedup(ttl time.Duration, opts ...DedupOpt[lx.Handler]) lx.Wrap {
	return func(next lx.Handler) lx.Handler {
		return NewDedup(next, ttl, opts...)
	}
}

func PipeBuffer(opts ...BufferingOpt) lx.Wrap {
	return func(next lx.Handler) lx.Handler {
		return NewBuffered(next, opts...)
	}
}

func PipeRotate(
	maxSizeBytes int64,
	src RotateSource,
) lx.Wrap {
	return func(next lx.Handler) lx.Handler {
		h, ok := next.(interface {
			lx.HandlerOutputter
			lx.Outputter
		})
		if !ok {
			panic("PipeRotate requires handler with SetOutput(io.Writer)")
		}

		r, err := NewRotating(h, maxSizeBytes, src)
		if err != nil {
			panic(err)
		}
		return r
	}
}
