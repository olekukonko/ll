package ll

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/olekukonko/ll/lh"
	"github.com/olekukonko/ll/lm"
	"github.com/olekukonko/ll/lx"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain sets up the test environment and runs the test suite.
// It resets the defaultLogger to a clean state to prevent state leakage between tests.
func TestMain(m *testing.M) {
	// Initialize defaultLogger with default values
	defaultLogger = &Logger{
		enabled:    lx.DefaultEnabled,
		level:      lx.LevelDebug,
		namespaces: defaultStore,
		context:    make(map[string]interface{}),
		style:      lx.FlatPath,
		handler:    nil,
		separator:  lx.Slash,
	}
	// Run tests and exit with the appropriate status code
	os.Exit(m.Run())
}

// TestLoggerConfiguration verifies the basic configuration methods of the Logger.
func TestLoggerConfiguration(t *testing.T) {
	// Create a new logger with namespace "test"
	logger := New("test").Enable()

	// Test Enable/Disable functionality
	logger = logger.Disable()
	logger.Info("Should not log") // Should be ignored since logger is disabled
	if logger.enabled {
		t.Errorf("Expected enabled=false, got %v", logger.enabled)
	}
	logger = logger.Enable()
	if !logger.enabled {
		t.Errorf("Expected enabled=true, got %v", logger.enabled)
	}

	// Test Level functionality
	logger = logger.Level(lx.LevelWarn)
	if logger.level != lx.LevelWarn {
		t.Errorf("Expected level=%v, got %v", lx.LevelWarn, logger.level)
	}
	logger.Info("Should not log") // Below Warn level, should be ignored
	logger.Warn("Should log")     // At Warn level, should be processed

	// Test Style functionality
	logger = logger.Style(lx.NestedPath)
	if logger.style != lx.NestedPath {
		t.Errorf("Expected style=%v, got %v", lx.NestedPath, logger.style)
	}
	logger = logger.Style(lx.FlatPath)
	if logger.style != lx.FlatPath {
		t.Errorf("Expected style=%v, got %v", lx.FlatPath, logger.style)
	}
}

// TestLoggingMethods verifies the core logging methods (Debug, Info, Warn, Error, Stack).
func TestLoggingMethods(t *testing.T) {
	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("test").Enable().Handler(lh.NewTextHandler(buf)).Level(lx.LevelDebug)

	// Test Debug logging
	buf.Reset()
	logger.Fields("key", "value").Debug("Debug message")
	if !strings.Contains(buf.String(), "[test] DEBUG: Debug message [key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] DEBUG: Debug message [key=value]")
	}

	// Test Info logging
	buf.Reset()
	logger.Fields("key", "value").Info("Info message")
	if !strings.Contains(buf.String(), "[test] INFO: Info message [key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Info message [key=value]")
	}

	// Test Warn logging
	buf.Reset()
	logger.Fields("key", "value").Warn("Warn message")
	if !strings.Contains(buf.String(), "[test] WARN: Warn message [key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] WARN: Warn message [key=value]")
	}

	// Test Error logging
	buf.Reset()
	logger.Fields("key", "value").Error("Error message")
	if !strings.Contains(buf.String(), "[test] ERROR: Error message [key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] ERROR: Error message [key=value]")
	}

	// Test Stack logging with stack trace
	buf.Reset()
	logger.Fields("key", "value").Stack("Error with stack")
	output := buf.String()
	if !strings.Contains(output, "[test] ERROR: Error with stack") {
		t.Errorf("Expected %q to contain %q", output, "[test] ERROR: Error with stack")
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("Expected %q to contain %q", output, "key=value")
	}
	if !strings.Contains(output, "stack=") {
		t.Errorf("Expected %q to contain %q", output, "stack=")
	}
}

// TestBuilderFields verifies the Fields and Field methods for adding metadata to logs.
func TestBuilderFields(t *testing.T) {
	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("test").Enable().Handler(lh.NewTextHandler(buf))

	// Test variadic Fields with multiple key-value pairs
	buf.Reset()
	logger.Fields("k1", "v1", "k2", "v2", "k3", 123).Info("Test variadic")
	if !strings.Contains(buf.String(), "[test] INFO: Test variadic [k1=v1 k2=v2 k3=123]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test variadic [k1=v1 k2=v2 k3=123]")
	}

	// Test map-based Field with a pre-constructed map
	buf.Reset()
	fields := map[string]interface{}{"k1": "v1", "k2": "v2", "k3": 123}
	logger.Field(fields).Info("Test map")
	if !strings.Contains(buf.String(), "[test] INFO: Test map [k1=v1 k2=v2 k3=123]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test map [k1=v1 k2=v2 k3=123]")
	}

	// Test variadic Fields with uneven key-value pairs
	buf.Reset()
	logger.Fields("k1", "v1", "k2").Info("Test uneven")
	if !strings.Contains(buf.String(), "[test] INFO: Test uneven [error=uneven key-value pairs in Fields: [k2] k1=v1]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test uneven [error=uneven key-value pairs in Fields: [k2] k1=v1]")
	}

	// Test variadic Fields with a non-string key
	buf.Reset()
	logger.Fields("k1", "v1", 42, "v2").Info("Test non-string")
	if !strings.Contains(buf.String(), "[test] INFO: Test non-string [error=non-string key in Fields: 42 k1=v1]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test non-string [error=non-string key in Fields: 42 k1=v1]")
	}
}

// TestHandlers verifies the behavior of all log handlers (Text, Colorized, JSON, Slog, Multi).
func TestHandlers(t *testing.T) {
	// Test TextHandler for plain text output
	t.Run("TextHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test").Enable().Handler(lh.NewTextHandler(buf))
		logger.Fields("key", "value").Info("Test text")
		if !strings.Contains(buf.String(), "[test] INFO: Test text [key=value]") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test text [key=value]")
		}
	})

	// Test ColorizedHandler for ANSI-colored output
	t.Run("ColorizedHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test").Enable().Handler(lh.NewColorizedHandler(buf))
		logger.Fields("key", "value").Info("Test color")
		// Check for namespace presence, ignoring ANSI codes
		if !strings.Contains(buf.String(), "[test]") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test color [key=value]")
		}
	})

	// Test JSONHandler for structured JSON output
	t.Run("JSONHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test").Enable().Handler(lh.NewJSONHandler(buf, ""))
		logger.Fields("key", "value").Info("Test JSON")
		// Parse JSON output and verify fields
		var data map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if data["level"] != "INFO" {
			t.Errorf("Expected level=%q, got %q", "INFO", data["level"])
		}
		if data["message"] != "Test JSON" {
			t.Errorf("Expected message=%q, got %q", "Test JSON", data["message"])
		}
		if data["namespace"] != "test" {
			t.Errorf("Expected namespace=%q, got %q", "test", data["namespace"])
		}
		if data["key"] != "value" {
			t.Errorf("Expected key=%q, got %q", "value", data["key"])
		}
	})

	// Test SlogHandler for compatibility with slog
	t.Run("SlogHandler", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test").Enable().Handler(lh.NewSlogHandler(slog.NewTextHandler(buf, nil)))
		logger.Fields("key", "value").Info("Test slog")
		output := buf.String()
		if !strings.Contains(output, "level=INFO") {
			t.Errorf("Expected %q to contain %q", output, "level=INFO")
		}
		if !strings.Contains(output, "msg=\"Test slog\"") {
			t.Errorf("Expected %q to contain %q", output, "msg=\"Test slog\"")
		}
		if !strings.Contains(output, "namespace=test") {
			t.Errorf("Expected %q to contain %q", output, "namespace=test")
		}
		if !strings.Contains(output, "key=value") {
			t.Errorf("Expected %q to contain %q", output, "key=value")
		}
	})

	// Test MultiHandler for combining multiple handlers
	t.Run("MultiHandler", func(t *testing.T) {
		buf1 := &bytes.Buffer{}
		buf2 := &bytes.Buffer{}
		logger := New("test").Enable().Handler(lh.NewMultiHandler(
			lh.NewTextHandler(buf1),
			lh.NewJSONHandler(buf2, ""),
		))
		logger.Fields("key", "value").Info("Test multi")
		// Verify TextHandler output
		if !strings.Contains(buf1.String(), "[test] INFO: Test multi [key=value]") {
			t.Errorf("Expected %q to contain %q", buf1.String(), "[test] INFO: Test multi [key=value]")
		}
		// Verify JSONHandler output
		var data map[string]interface{}
		if err := json.Unmarshal(buf2.Bytes(), &data); err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if data["message"] != "Test multi" {
			t.Errorf("Expected message=%q, got %q", "Test multi", data["message"])
		}
	})
}

// TestNamespaces verifies namespace-related functionality, including child namespaces and enable/disable behavior.
func TestNamespaces(t *testing.T) {
	// Debug: Verify Arrow constant
	fmt.Printf("Arrow constant: %q\n", lx.Arrow)

	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("parent").Enable().Handler(lh.NewTextHandler(buf))

	// Test child namespace creation and logging
	child := logger.Namespace("child").Enable()
	buf.Reset()
	child.Info("Child log")
	if !strings.Contains(buf.String(), "[parent/child] INFO: Child log") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[parent/child] INFO: Child log")
	}

	// Test NestedPath style formatting
	logger = logger.Style(lx.NestedPath)
	child = child.Style(lx.NestedPath)
	buf.Reset()
	child.Info("Nested log")
	expectedNested := "[parent]" + lx.Arrow + "[child]" + lx.Colon + lx.Space + "INFO: Nested log"
	if !strings.Contains(buf.String(), expectedNested) {
		t.Errorf("Expected %q to contain %q; got %q", buf.String(), expectedNested, buf.String())
	}

	// Test NamespaceEnable/NamespaceDisable to verify logging behavior
	logger = logger.NamespaceDisable("parent/child")
	// Debug namespace state before logging
	enabled, ok := child.namespaces.Load("parent/child")
	fmt.Printf("Namespace parent/child before logging: ok=%v, enabled=%v\n", ok, enabled)
	buf.Reset()
	child.Info("Should not log") // Should be ignored due to disabled namespace
	// Debug namespace state after logging
	enabled, ok = child.namespaces.Load("parent/child")
	fmt.Printf("Namespace parent/child after logging: ok=%v, enabled=%v\n", ok, enabled)
	if buf.String() != "" {
		t.Errorf("Expected empty buffer, got %q", buf.String())
	}

	// Re-enable namespace and verify logging
	logger = logger.NamespaceEnable("parent/child")
	child = child.Enable()
	buf.Reset()
	child.Info("Should log")
	expectedShouldLog := "[parent]" + lx.Arrow + "[child]" + lx.Colon + lx.Space + "INFO: Should log"
	if !strings.Contains(buf.String(), expectedShouldLog) {
		t.Errorf("Expected %q to contain %q; got %q", buf.String(), expectedShouldLog, buf.String())
	}
}

// TestSharedNamespaces verifies namespace state sharing between parent and child loggers.
func TestSharedNamespaces(t *testing.T) {
	// Create a fresh parent logger
	parent := New("parent").Enable().Handler(lh.NewTextHandler(os.Stdout))

	// Disable the child namespace
	parent = parent.NamespaceDisable("parent/child")

	// Create a child logger
	child := parent.Namespace("child").Enable()

	// Set up a buffer for capturing child logger output
	buf := &bytes.Buffer{}
	child = child.Handler(lh.NewTextHandler(buf))

	// Verify logging is disabled
	child.Info("Should not log")
	if buf.String() != "" {
		t.Errorf("Expected no output from disabled namespace, got: %q", buf.String())
	}

	// Enable the namespace and verify logging
	parent = parent.NamespaceEnable("parent/child")
	buf.Reset()
	child.Info("Should log")
	if !strings.Contains(buf.String(), "Should log") {
		t.Errorf("Expected log output from enabled namespace, got: %q", buf.String())
	}
}

// TestRateLimiting verifies rate-limiting functionality for a log level.
func TestRateLimiting(t *testing.T) {
	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("test").Enable().Handler(lh.NewTextHandler(buf))
	logger.Use(lm.NewRateLimiter(lx.LevelInfo, 2, time.Second))

	// Test logging within the rate limit (2 logs allowed)
	buf.Reset()
	logger.Info("Log 1")
	logger.Info("Log 2")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Errorf("Expected %d logs, got %d", 2, len(lines))
	}
	if !strings.Contains(buf.String(), "Log 1") {
		t.Errorf("Expected %q to contain %q", buf.String(), "Log 1")
	}
	if !strings.Contains(buf.String(), "Log 2") {
		t.Errorf("Expected %q to contain %q", buf.String(), "Log 2")
	}

	// Test exceeding the rate limit
	buf.Reset()
	logger.Info("Log 3") // Should be blocked
	if buf.String() != "" {
		t.Errorf("Expected empty buffer, got %q", buf.String())
	}

	// Test logging after the interval resets
	time.Sleep(time.Second)
	buf.Reset()
	logger.Info("Log 4")
	if !strings.Contains(buf.String(), "Log 4") {
		t.Errorf("Expected %q to contain %q", buf.String(), "Log 4")
	}
}

// TestSampling verifies sampling functionality for a log level.
func TestSampling(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("test").Enable().Handler(lh.NewTextHandler(buf)).Clear() // Clear middleware
	logger.Use(lm.NewSampling(lx.LevelInfo, 0.0))                          // Never log

	// Test logging with 0.0 sampling rate
	buf.Reset()
	logger.Info("Should not log")
	if buf.String() != "" {
		t.Errorf("Expected empty buffer, got %q", buf.String())
	}

	// Test logging with 1.0 sampling rate
	logger = New("test").Enable().Handler(lh.NewTextHandler(buf)).Clear() // Fresh logger
	logger.Use(lm.NewSampling(lx.LevelInfo, 1.0))                         // Always log
	buf.Reset()
	logger.Info("Should log")
	if !strings.Contains(buf.String(), "[test] INFO: Should log") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Should log")
	}
}

// TestConditionalLogging verifies conditional logging using the If method.
func TestConditionalLogging(t *testing.T) {
	// Reset defaultLogger to ensure clean state
	defaultLogger = &Logger{
		enabled:    true,
		level:      lx.LevelDebug,
		namespaces: defaultStore,
		context:    make(map[string]interface{}),
		style:      lx.FlatPath,
		handler:    nil,
		separator:  lx.Slash,
	}

	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("VC").Enable().Handler(lh.NewTextHandler(buf)).Level(lx.LevelDebug)

	// Test false condition with variadic Fields
	buf.Reset()
	logger.If(false).Fields("key", "value").Info("Should not log")
	if buf.String() != "" {
		t.Errorf("Expected empty buffer, got %q", buf.String())
	}

	// Test true condition with variadic Fields
	buf.Reset()
	logger.If(true).Fields("key", "value").Info("Should log")
	if !strings.Contains(buf.String(), "[VC] INFO: Should log [key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[VC] INFO: Should log [key=value]")
	}

	// Test false condition with map-based Field
	buf.Reset()
	fields := map[string]interface{}{"key": "value"}
	logger.If(false).Field(fields).Info("Should not log")
	if buf.String() != "" {
		t.Errorf("Expected empty buffer, got %q", buf.String())
	}

	// Test true condition with map-based Field
	buf.Reset()
	logger.If(true).Field(fields).Info("Should log")
	if !strings.Contains(buf.String(), "[VC] INFO: Should log [key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[VC] INFO: Should log [key=value]")
	}

	// Test variadic Fields with uneven pairs under true condition
	buf.Reset()
	logger.If(true).Fields("key", "value", "odd").Info("Test uneven")
	if !strings.Contains(buf.String(), "[VC] INFO: Test uneven [error=uneven key-value pairs in Fields: [odd] key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[VC] INFO: Test uneven [error=uneven key-value pairs in Fields: [odd] key=value]")
	}

	// Test variadic Fields with non-string key under true condition
	buf.Reset()
	logger.If(true).Fields("key", "value", 42, "value2").Info("Test non-string")
	if !strings.Contains(buf.String(), "[VC] INFO: Test non-string [error=non-string key in Fields: 42 key=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[VC] INFO: Test non-string [error=non-string key in Fields: 42 key=value]")
	}

	// Test Conditional Stack logging with stack trace
	t.Run("ConditionalStack", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := New("test/app").Enable().Style(lx.NestedPath).Handler(lh.NewTextHandler(buf)).Level(lx.LevelDebug).Prefix("ERR: ").Indent(1)
		logger = logger.Context(map[string]interface{}{"ctx": "value"})
		logger.If(true).Stack("error occurred: %v", "timeout")
		expectedStack := "[test]" + lx.Arrow + "[app]" + lx.Colon + lx.Space + "ERROR:   ERR: error occurred: timeout [ctx=value stack="
		if !strings.Contains(buf.String(), expectedStack) {
			t.Errorf("Expected %q to contain %q; got %q", buf.String(), expectedStack, buf.String())
		}
		buf.Reset()
		logger.If(false).Stack("should not log: %v", "timeout")
		if buf.String() != "" {
			t.Errorf("Expected empty buffer, got %q", buf.String())
		}
	})
}

// TestMiddleware verifies the Use method for adding middleware to process log entries.
func TestMiddleware(t *testing.T) {
	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("test").Enable().Handler(lh.NewTextHandler(buf)).Level(lx.LevelDebug)

	// Test middleware that adds a field
	logger = logger.Use(Middle(func(e *lx.Entry) error {
		if e.Fields == nil {
			e.Fields = make(map[string]interface{})
		}
		e.Fields["extra"] = "value"
		return nil
	})).Logger()

	buf.Reset()
	logger.Info("Test with extra field")
	if !strings.Contains(buf.String(), "[test] INFO: Test with extra field [extra=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test with extra field [extra=value]")
	}

	// Test middleware that filters logs by level
	logger = logger.Use(Middle(func(e *lx.Entry) error {
		if e.Level >= lx.LevelWarn {
			return nil
		}
		return fmt.Errorf("level too low")
	})).Logger()
	buf.Reset()
	logger.Info("Should not log") // Below Warn level, should be ignored
	if buf.String() != "" {
		t.Errorf("Expected empty buffer, got %q", buf.String())
	}
	buf.Reset()
	logger.Warn("Should log")
	if !strings.Contains(buf.String(), "[test] WARN: Should log [extra=value]") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] WARN: Should log [extra=value]")
	}

	// Test middleware that skips all logs
	logger = logger.Use(Middle(func(e *lx.Entry) error {
		return fmt.Errorf("skip all")
	})).Logger()
	buf.Reset()
	logger.Warn("Should not log") // Should be ignored by middleware
	if buf.String() != "" {
		t.Errorf("Expected empty buffer, got %q", buf.String())
	}
}

// TestClone verifies the Clone method for creating a logger with the same namespace and isolated context.
func TestClone(t *testing.T) {
	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("app").Enable().Handler(lh.NewTextHandler(buf)).Level(lx.LevelInfo).Style(lx.NestedPath)

	// Test namespace preservation
	t.Run("Namespace", func(t *testing.T) {
		clone := logger.Clone()
		if clone.currentPath != "app" {
			t.Errorf("Expected clone namespace %q, got %q", "app", clone.currentPath)
		}
	})

	// Test configuration inheritance
	t.Run("Configuration", func(t *testing.T) {
		clone := logger.Clone()
		if !clone.Enabled() {
			t.Errorf("Expected clone enabled=true, got %v", clone.Enabled())
		}
		if clone.GetLevel() != lx.LevelInfo {
			t.Errorf("Expected clone level=%v, got %v", lx.LevelInfo, clone.GetLevel())
		}
		if clone.style != lx.NestedPath {
			t.Errorf("Expected clone style=%v, got %v", lx.NestedPath, clone.style)
		}
	})

	// Test context isolation
	t.Run("ContextIsolation", func(t *testing.T) {
		// Update parent logger with context
		logger = logger.Context(map[string]interface{}{"parent": "value"})
		clone := logger.Clone()
		buf.Reset()
		clone.Fields("clone", "value").Info("Clone message")
		expected := "[app]" + lx.Colon + lx.Space + "INFO: Clone message [clone=value]"
		if !strings.Contains(buf.String(), expected) {
			t.Errorf("Expected %q to contain %q; got %q", buf.String(), expected, buf.String())
		}
		if strings.Contains(buf.String(), "parent=value") {
			t.Errorf("Expected %q not to contain %q", buf.String(), "parent=value")
		}
	})

	// Test parent context preservation
	t.Run("ParentContext", func(t *testing.T) {
		buf.Reset()
		logger.Info("Parent message")
		expected := "[app]" + lx.Colon + lx.Space + "INFO: Parent message [parent=value]"
		if !strings.Contains(buf.String(), expected) {
			t.Errorf("Expected %q to contain %q; got %q", buf.String(), expected, buf.String())
		}
		if strings.Contains(buf.String(), "clone=value") {
			t.Errorf("Expected %q not to contain %q", buf.String(), "clone=value")
		}
	})
}

// TestPrefix verifies the Prefix method for prepending a string to log messages.
func TestPrefix(t *testing.T) {
	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("app").Enable().Handler(lh.NewTextHandler(buf)).Level(lx.LevelInfo)

	// Test setting a prefix
	t.Run("SetPrefix", func(t *testing.T) {
		logger = logger.Prefix("INFO: ")
		buf.Reset()
		logger.Info("Test message")
		if !strings.Contains(buf.String(), "[app] INFO: INFO: Test message") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[app] INFO: INFO: Test message")
		}
	})

	// Test updating the prefix
	t.Run("UpdatePrefix", func(t *testing.T) {
		logger = logger.Prefix("DEBUG: ")
		buf.Reset()
		logger.Info("Another message")
		if !strings.Contains(buf.String(), "[app] INFO: DEBUG: Another message") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[app] INFO: DEBUG: Another message")
		}
	})

	// Test removing the prefix
	t.Run("RemovePrefix", func(t *testing.T) {
		logger = logger.Prefix("")
		buf.Reset()
		logger.Info("No prefix")
		if !strings.Contains(buf.String(), "[app] INFO: No prefix") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[app] INFO: No prefix")
		}
	})
}

// TestIndent verifies the Indent method for adding double spaces to log messages.
func TestIndent(t *testing.T) {
	// Set up a logger with a buffer for capturing output
	buf := &bytes.Buffer{}
	logger := New("app").Enable().Handler(lh.NewTextHandler(buf)).Level(lx.LevelInfo)

	// Test setting indentation to 2 (4 spaces)
	t.Run("SetIndent", func(t *testing.T) {
		logger = logger.Indent(2)
		buf.Reset()
		logger.Info("Test message")
		if !strings.Contains(buf.String(), "[app] INFO:     Test message") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[app] INFO:     Test message")
		}
	})

	// Test updating indentation to 1 (2 spaces)
	t.Run("UpdateIndent", func(t *testing.T) {
		logger = logger.Indent(1)
		buf.Reset()
		logger.Info("Another message")
		if !strings.Contains(buf.String(), "[app] INFO:   Another message") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[app] INFO:   Another message")
		}
	})

	// Test removing indentation
	t.Run("RemoveIndent", func(t *testing.T) {
		logger = logger.Indent(0)
		buf.Reset()
		logger.Info("No indent")
		if !strings.Contains(buf.String(), "[app] INFO: No indent") {
			t.Errorf("Expected %q to contain %q", buf.String(), "[app] INFO: No indent")
		}
	})
}

// failingWriter is a test writer that always fails to write, used to simulate handler errors.
type failingWriter struct{}

// Write implements io.Writer, always returning an error.
func (w *failingWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("write failed")
}

// TestHandlerErrors verifies handler behavior when errors occur.
func TestHandlerErrors(t *testing.T) {
	// Reset defaultLogger to ensure clean state
	defaultLogger = &Logger{
		enabled:    true,
		level:      lx.LevelDebug,
		namespaces: defaultStore,
		context:    make(map[string]interface{}),
		style:      lx.FlatPath,
		handler:    nil,
		separator:  lx.Slash,
	}

	// Test single TextHandler
	buf := &bytes.Buffer{}
	logger := New("test").Enable().Level(lx.LevelDebug).Handler(lh.NewTextHandler(buf))

	logger.Info("Test single handler")
	if !strings.Contains(buf.String(), "[test] INFO: Test single handler") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test single handler")
	}

	// Test MultiHandler with a failing TextHandler
	buf.Reset()
	logger = logger.Handler(lh.NewMultiHandler(
		lh.NewTextHandler(buf),
		lh.NewTextHandler(&failingWriter{}),
	))
	logger.Info("Test multi error")
	if !strings.Contains(buf.String(), "[test] INFO: Test multi error") {
		t.Errorf("Expected %q to contain %q", buf.String(), "[test] INFO: Test multi error")
	}
}

// TestNamespaceToggle verifies the NamespaceEnable and NamespaceDisable methods.
func TestNamespaceToggle(t *testing.T) {
	// Create a logger and test namespace toggling
	logger := New("test")
	logger = logger.NamespaceEnable("parent/child")
	if enabled, ok := logger.namespaces.Load("parent/child"); !ok || !enabled.(bool) {
		t.Error("parent/child should be enabled")
	}
	logger = logger.NamespaceDisable("parent/child")
	if enabled, ok := logger.namespaces.Load("parent/child"); !ok || enabled.(bool) {
		t.Error("parent/child should be disabled")
	}
}

// TestTextHandler verifies the TextHandler’s output format.
func TestTextHandler(t *testing.T) {
	// Create a buffer and TextHandler
	var buf bytes.Buffer
	h := lh.NewTextHandler(&buf)
	// Create a test log entry
	e := &lx.Entry{
		Timestamp: time.Now(),
		Level:     lx.LevelInfo,
		Message:   "test",
		Namespace: "",
		Fields:    map[string]interface{}{"key": 1},
	}
	// Process the entry
	if err := h.Handle(e); err != nil {
		t.Errorf("Handle failed: %v", err)
	}
	// Verify the output format
	if !strings.Contains(buf.String(), "INFO: test [key=1]") {
		t.Errorf("Unexpected output: %s", buf.String())
	}
}
