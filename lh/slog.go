package lh

import (
	"context"
	"github.com/olekukonko/ll/lx"
	"log/slog"
	"time"
)

// SlogHandler adapts the standard library’s slog.Handler for compatibility with this package.
type SlogHandler struct {
	slogHandler slog.Handler // Underlying slog handler
}

// NewSlogHandler creates a new SlogHandler wrapping the provided slog.Handler.
func NewSlogHandler(h slog.Handler) *SlogHandler {
	return &SlogHandler{slogHandler: h}
}

// Handle implements the Handler interface, converting the log entry to an slog.Record.
// It maps entry fields to slog attributes and delegates to the underlying slog.Handler.
func (h *SlogHandler) Handle(e *lx.Entry) error {
	// Create attributes for standard fields
	attrs := make([]slog.Attr, 0, len(e.Fields)+3)
	attrs = append(attrs,
		slog.String("timestamp", e.Timestamp.Format(time.RFC3339Nano)),
		slog.String("level", e.Level.String()),
		slog.String("namespace", e.Namespace),
	)
	// Add custom fields as attributes
	for k, v := range e.Fields {
		attrs = append(attrs, slog.Any(k, v))
	}
	// Create an slog.Record with the entry’s timestamp, level, and message
	record := slog.NewRecord(e.Timestamp, slog.Level(e.Level), e.Message, 0)
	record.AddAttrs(attrs...)
	// Delegate to the underlying slog.Handler
	return h.slogHandler.Handle(context.Background(), record)
}
