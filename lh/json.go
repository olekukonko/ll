package lh

import (
	"encoding/json"
	"github.com/olekukonko/ll/lx"
	"io"
	"time"
)

// JSONHandler outputs logs in JSON format for structured logging.
type JSONHandler struct {
	writer  io.Writer // Destination for log output
	timeFmt string    // Timestamp format (e.g., RFC3339Nano)
}

// NewJSONHandler creates a new JSONHandler with the specified writer and optional timestamp format.
// If timeFmt is empty, it defaults to time.RFC3339Nano.
func NewJSONHandler(w io.Writer, timeFmt string) *JSONHandler {
	if timeFmt == "" {
		timeFmt = time.RFC3339Nano
	}
	return &JSONHandler{writer: w, timeFmt: timeFmt}
}

// Handle implements the Handler interface, serializing the log entry to JSON.
// It includes timestamp, level, message, namespace, and fields in the output.
func (h *JSONHandler) Handle(e *lx.Entry) error {
	// Create a map with standard log fields
	entry := map[string]interface{}{
		"timestamp": e.Timestamp.Format(h.timeFmt),
		"level":     e.Level.String(),
		"message":   e.Message,
		"namespace": e.Namespace,
	}
	// Add custom fields
	for k, v := range e.Fields {
		entry[k] = v
	}
	// Marshal to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	// Append newline for readability
	data = append(data, '\n')
	// Write the JSON data to the writer
	_, err = h.writer.Write(data)
	return err
}
