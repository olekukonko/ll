package ll

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/assert"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Reset defaultLogger before running tests to prevent state leakage
	defaultLogger = &Logger{
		enabled:     defaultEnabled,
		level:       LevelDebug,
		namespaces:  sync.Map{},
		context:     make(map[string]interface{}),
		style:       FlatPath,
		rateLimits:  make(map[Level]*rateLimit),
		sampleRates: make(map[Level]float64),
	}
	os.Exit(m.Run())
}

func TestLoggerConfiguration(t *testing.T) {
	logger := New("test")
	logger.Enable()

	// Test Enable/Disable
	logger.Disable()
	logger.Info("Should not log")
	assert.False(t, logger.enabled)
	logger.Enable()
	assert.True(t, logger.enabled)

	// Test SetLevel
	logger.SetLevel(LevelWarn)
	assert.Equal(t, LevelWarn, logger.level)
	logger.Info("Should not log")
	logger.Warn("Should log")

	// Test SetStyle
	logger.SetStyle(NestedPath)
	assert.Equal(t, NestedPath, logger.style)
	logger.SetStyle(FlatPath)
	assert.Equal(t, FlatPath, logger.style)
}

func TestLoggingMethods(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("test")
	logger.Enable()
	logger.SetHandler(NewTextHandler(buf))
	logger.SetLevel(LevelDebug)

	// Test Debug
	buf.Reset()
	logger.Fields("key", "value").Debug("Debug message")
	assert.Contains(t, buf.String(), "[test] DEBUG: Debug message [key=value]")

	// Test Info
	buf.Reset()
	logger.Fields("key", "value").Info("Info message")
	assert.Contains(t, buf.String(), "[test] INFO: Info message [key=value]")

	// Test Warn
	buf.Reset()
	logger.Fields("key", "value").Warn("Warn message")
	assert.Contains(t, buf.String(), "[test] WARN: Warn message [key=value]")

	// Test Error
	buf.Reset()
	logger.Fields("key", "value").Error("Error message")
	assert.Contains(t, buf.String(), "[test] ERROR: Error message [key=value]")

	// Test Stack
	buf.Reset()
	logger.Fields("key", "value").Stack("Error with stack")
	output := buf.String()
	assert.Contains(t, output, "[test] ERROR: Error with stack")
	assert.Contains(t, output, "key=value")
	assert.Contains(t, output, "stack=")
}

func TestBuilderFields(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("test")
	logger.Enable()
	logger.SetHandler(NewTextHandler(buf))

	// Test variadic Fields
	buf.Reset()
	logger.Fields("k1", "v1", "k2", "v2", "k3", 123).Info("Test variadic")
	assert.Contains(t, buf.String(), "[test] INFO: Test variadic [k1=v1 k2=v2 k3=123]")

	// Test uneven Fields pairs
	buf.Reset()
	logger.Fields("k1", "v1", "k2").Info("Test uneven")
	assert.Contains(t, buf.String(), "[test] INFO: Test uneven [error=uneven key-value pairs in Fields: [k2] k1=v1]")

	// Test non-string key
	buf.Reset()
	logger.Fields("k1", "v1", 42, "v2").Info("Test non-string")
	assert.Contains(t, buf.String(), "[test] INFO: Test non-string [error=non-string key in Fields: 42 k1=v1]")
}

func TestHandlers(t *testing.T) {
	// Test TextHandler
	t.Run("TextHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test")
		logger.Enable()
		logger.SetHandler(NewTextHandler(buf))
		logger.Fields("key", "value").Info("Test text")
		assert.Contains(t, buf.String(), "[test] INFO: Test text [key=value]")
	})

	// Test JSONHandler
	t.Run("JSONHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test")
		logger.Enable()
		logger.SetHandler(NewJSONHandler(buf))
		logger.Fields("key", "value").Info("Test JSON")
		var data map[string]interface{}
		err := json.Unmarshal(buf.Bytes(), &data)
		assert.NoError(t, err)
		assert.Equal(t, "INFO", data["level"])
		assert.Equal(t, "Test JSON", data["message"])
		assert.Equal(t, "test", data["namespace"])
		assert.Equal(t, "value", data["key"])
	})

	// Test ColorizedHandler
	t.Run("ColorizedHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test")
		logger.Enable()
		logger.SetHandler(NewColorizedHandler(buf))
		logger.Fields("key", "value").Info("Test color")
		assert.Contains(t, buf.String(), "[test] INFO: Test color [key=value]")
	})

	// Test SlogHandler
	t.Run("SlogHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test")
		logger.Enable()
		logger.SetHandler(NewSlogHandler(slog.NewTextHandler(buf, nil)))
		logger.Fields("key", "value").Info("Test slog")
		assert.Contains(t, buf.String(), "level=INFO")
		assert.Contains(t, buf.String(), "msg=\"Test slog\"")
		assert.Contains(t, buf.String(), "namespace=test")
		assert.Contains(t, buf.String(), "key=value")
	})

	// Test MultiHandler
	t.Run("MultiHandler", func(t *testing.T) {
		buf1 := &bytes.Buffer{}
		buf2 := &bytes.Buffer{}
		logger := New("test")
		logger.Enable()
		logger.SetHandler(NewMultiHandler(
			NewTextHandler(buf1),
			NewJSONHandler(buf2),
		))
		logger.Fields("key", "value").Info("Test multi")
		assert.Contains(t, buf1.String(), "[test] INFO: Test multi [key=value]")
		var data map[string]interface{}
		err := json.Unmarshal(buf2.Bytes(), &data)
		assert.NoError(t, err)
		assert.Equal(t, "Test multi", data["message"])
	})
}

func TestNamespaces(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("parent")
	logger.Enable()
	logger.SetHandler(NewTextHandler(buf))

	// Test child namespace
	child := logger.Namespace("child")
	child.Enable()
	buf.Reset()
	child.Info("Child log")
	assert.Contains(t, buf.String(), "[parent/child] INFO: Child log")

	// Test NestedPath style
	logger.SetStyle(NestedPath)
	child.SetStyle(NestedPath)
	buf.Reset()
	child.Info("Nested log")
	assert.Contains(t, buf.String(), "[parent] -> [child] : INFO: Nested log")

	// Test Enable/DisableNamespace
	logger.DisableNamespace("parent/child")
	// Debug namespace state before logging
	enabled, ok := child.namespaces.Load("parent/child")
	fmt.Printf("Namespace parent/child before logging: ok=%v, enabled=%v\n", ok, enabled)
	buf.Reset()
	child.Info("Should not log")
	// Debug namespace state after logging
	enabled, ok = child.namespaces.Load("parent/child")
	fmt.Printf("Namespace parent/child after logging: ok=%v, enabled=%v\n", ok, enabled)
	assert.Empty(t, buf.String(), "Should be empty, but was %s", buf.String())

	logger.EnableNamespace("parent/child")
	child.Enable()
	buf.Reset()
	child.Info("Should log")
	assert.Contains(t, buf.String(), "[parent] -> [child] : INFO: Should log")
}

func TestRateLimiting(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("test")
	logger.Enable()
	logger.SetHandler(NewTextHandler(buf))
	logger.SetRateLimit(LevelInfo, 2, time.Second)

	// Test within limit
	buf.Reset()
	logger.Info("Log 1")
	logger.Info("Log 2")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 2, "Expected exactly 2 logs")
	assert.Contains(t, buf.String(), "Log 1")
	assert.Contains(t, buf.String(), "Log 2")

	// Test exceeding limit
	buf.Reset()
	logger.Info("Log 3") // Should not log
	assert.Empty(t, buf.String(), "Log 3 should not appear due to rate limit")

	// Test reset after interval
	time.Sleep(time.Second)
	buf.Reset()
	logger.Info("Log 4")
	assert.Contains(t, buf.String(), "Log 4")
}

func TestSampling(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("test")
	logger.Enable()
	logger.SetHandler(NewTextHandler(buf))
	logger.SetSampling(LevelInfo, 0.0) // Never log

	buf.Reset()
	logger.Info("Should not log")
	assert.Empty(t, buf.String())

	logger.SetSampling(LevelInfo, 1.0) // Always log
	buf.Reset()
	logger.Info("Should log")
	assert.Contains(t, buf.String(), "[test] INFO: Should log")
}

func TestConditionalLogging(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("test")
	logger.Enable()
	logger.SetHandler(NewTextHandler(buf))

	// Test false condition
	buf.Reset()
	logger.If(false).Info("Should not log")
	assert.Empty(t, buf.String())

	// Test true condition
	buf.Reset()
	logger.If(true).Fields("key", "value").Info("Should log")
	assert.Contains(t, buf.String(), "[test] INFO: Should log [key=value]")
}

// failingWriter simulates a failing io.Writer
type failingWriter struct{}

func (w *failingWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("write failed")
}

func TestHandlerErrors(t *testing.T) {

	// Test single TextHandler first
	buf := &bytes.Buffer{}
	logger := New("test")
	logger.Enable()
	logger.SetLevel(LevelDebug) // Ensure level allows Info
	logger.SetHandler(NewTextHandler(buf))
	logger.Info("Test single handler")
	// fmt.Printf("Single handler output: %s\n", buf.String())
	assert.Contains(t, buf.String(), "[test] INFO: Test single handler")

	// Test MultiHandler with a failing TextHandler
	buf.Reset()
	logger.SetHandler(NewMultiHandler(
		NewTextHandler(buf),              // Success
		NewTextHandler(&failingWriter{}), // Failure
	))
	logger.Info("Test multi error")
	// fmt.Printf("Multi handler output: %s\n", buf.String())
	assert.Contains(t, buf.String(), "[test] INFO: Test multi error")
}
