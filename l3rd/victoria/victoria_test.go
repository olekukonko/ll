package victoria

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olekukonko/ll/lx"
)

// TestNew tests the creation of a new Victoria handler.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantErr bool
		check   func(*Victoria, *Config) bool
	}{
		{
			name:    "default configuration",
			opts:    []Option{},
			wantErr: false,
			check: func(v *Victoria, c *Config) bool {
				return c.AppName != "unknown" &&
					c.Environment == "production" &&
					c.Timeout == 5*time.Second &&
					c.DevMode == false
			},
		},
		{
			name: "custom configuration",
			opts: []Option{
				WithAppName("test-app"),
				WithEnvironment("testing"),
				WithVersion("1.2.3"),
				WithHostname("test-host"),
				WithTimeout(10 * time.Second),
				WithRetry(3),
			},
			wantErr: false,
			check: func(v *Victoria, c *Config) bool {
				return c.AppName == "test-app" &&
					c.Environment == "testing" &&
					c.Version == "1.2.3" &&
					c.Hostname == "test-host" &&
					c.Timeout == 10*time.Second &&
					c.RetryCount == 3
			},
		},
		{
			name: "field mapping configuration",
			opts: []Option{
				WithFieldMapping("user_id", "userId"),
				WithFieldMapping("req_id", "requestId"),
			},
			wantErr: false,
			check: func(v *Victoria, c *Config) bool {
				return c.FieldMap["user_id"] == "userId" &&
					c.FieldMap["req_id"] == "requestId"
			},
		},
		{
			name: "stream keys configuration",
			opts: []Option{
				WithStreamKeys("app", "env", "level"),
			},
			wantErr: false,
			check: func(v *Victoria, c *Config) bool {
				return len(c.StreamKeys) == 3 &&
					c.StreamKeys[0] == "app" &&
					c.StreamKeys[1] == "env" &&
					c.StreamKeys[2] == "level"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			opts := append(tt.opts, WithURL(server.URL))

			v, err := New(opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil {
				defer v.Close()
				if !tt.check(v, v.config) {
					t.Errorf("configuration check failed")
				}
			}
		})
	}
}

// TestHandle tests the Handle method for processing log entries.
func TestHandle(t *testing.T) {
	tests := []struct {
		name        string
		entry       *lx.Entry
		config      []Option
		expectError bool
		validate    func([]byte) bool
	}{
		{
			name: "basic info log",
			entry: &lx.Entry{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Level:     lx.LevelInfo,
				Message:   "test message",
				Namespace: "test.ns",
				Fields:    nil,
			},
			config:      []Option{},
			expectError: false,
			validate: func(data []byte) bool {
				var line map[string]interface{}
				if err := json.Unmarshal(data, &line); err != nil {
					return false
				}
				return line["msg"] == "test message" &&
					line["level"] == "info" &&
					line["ns"] == "test.ns"
			},
		},
		{
			name: "error log with stack trace",
			entry: &lx.Entry{
				Timestamp: time.Now(),
				Level:     lx.LevelError,
				Message:   "error occurred",
				Namespace: "app.error",
				Stack:     []byte("goroutine 1 [running]:\nmain.main()\n\tmain.go:10"),
				Fields:    nil,
			},
			config:      []Option{},
			expectError: false,
			validate: func(data []byte) bool {
				var line map[string]interface{}
				if err := json.Unmarshal(data, &line); err != nil {
					return false
				}
				return line["level"] == "error" &&
					strings.Contains(line["stack"].(string), "goroutine")
			},
		},
		{
			name: "log with custom fields",
			entry: &lx.Entry{
				Timestamp: time.Now(),
				Level:     lx.LevelWarn,
				Message:   "warning with fields",
				Namespace: "app.warn",
				Fields: []lx.Field{
					{Key: "user_id", Value: "12345"},
					{Key: "requestId", Value: "req-abc"},
					{Key: "duration", Value: 150},
				},
			},
			config: []Option{
				WithFieldMapping("user_id", "userId"),
			},
			expectError: false,
			validate: func(data []byte) bool {
				var line map[string]interface{}
				if err := json.Unmarshal(data, &line); err != nil {
					return false
				}
				return line["userId"] == "12345" && // Should be mapped
					line["requestId"] == "req-abc" &&
					line["duration"].(float64) == 150
			},
		},
		{
			name: "dev mode adds debug info",
			entry: &lx.Entry{
				Timestamp: time.Now(),
				Level:     lx.LevelDebug,
				Message:   "debug message",
				Namespace: "app.debug",
				Fields:    nil,
			},
			config: []Option{
				WithDevMode(true),
			},
			expectError: false,
			validate: func(data []byte) bool {
				var line map[string]interface{}
				if err := json.Unmarshal(data, &line); err != nil {
					return false
				}
				return line["_handler"] == "ll/victoria"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedData []byte
			var requestCount int

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				data, _ := io.ReadAll(r.Body)
				receivedData = data
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			config := append(tt.config, WithURL(server.URL))
			v, err := New(config...)
			if err != nil {
				t.Fatalf("failed to create handler: %v", err)
			}
			defer v.Close()

			err = v.Handle(tt.entry)
			if (err != nil) != tt.expectError {
				t.Errorf("Handle() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if !tt.expectError && requestCount == 0 {
				t.Error("no request was made to the test server")
				return
			}

			if requestCount > 0 && tt.validate != nil {
				if !tt.validate(receivedData) {
					t.Error("data validation failed")
					t.Logf("received data: %s", string(receivedData))
				}
			}
		})
	}
}

// TestRetry tests the retry functionality.
func TestRetry(t *testing.T) {
	tests := []struct {
		name        string
		retryCount  int
		failTimes   int
		expectError bool
	}{
		{
			name:        "no retry on first failure",
			retryCount:  0,
			failTimes:   1,
			expectError: true,
		},
		{
			name:        "retry succeeds on second attempt",
			retryCount:  3,
			failTimes:   1,
			expectError: false,
		},
		{
			name:        "retry fails after all attempts",
			retryCount:  2,
			failTimes:   3,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attemptCount := 0

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attemptCount++
				if attemptCount <= tt.failTimes {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("server error"))
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			v, err := New(
				WithURL(server.URL),
				WithRetry(tt.retryCount),
				WithTimeout(100*time.Millisecond),
			)
			if err != nil {
				t.Fatalf("failed to create handler: %v", err)
			}
			defer v.Close()

			entry := &lx.Entry{
				Timestamp: time.Now(),
				Level:     lx.LevelInfo,
				Message:   "test retry",
				Namespace: "test.retry",
				Fields:    nil,
			}

			err = v.Handle(entry)
			if (err != nil) != tt.expectError {
				t.Errorf("Handle() error = %v, expectError %v", err, tt.expectError)
			}

			expectedAttempts := tt.failTimes + 1
			if !tt.expectError && attemptCount != expectedAttempts {
				t.Errorf("expected %d attempts, got %d", expectedAttempts, attemptCount)
			}
		})
	}
}

// TestPing tests the Ping method for connectivity verification.
func TestPing(t *testing.T) {
	tests := []struct {
		name        string
		serverFunc  http.HandlerFunc
		devMode     bool
		expectError bool
	}{
		{
			name: "successful ping",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			devMode:     true,
			expectError: false,
		},
		{
			name: "ping fails with server error",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			devMode:     true,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverFunc))
			defer server.Close()

			v, err := New(
				WithURL(server.URL),
				WithDevMode(tt.devMode),
				WithTimeout(100*time.Millisecond),
			)

			if tt.devMode {
				if (err != nil) != tt.expectError {
					t.Errorf("New() error = %v, expectError %v", err, tt.expectError)
				}
			} else {
				if err != nil {
					t.Fatalf("failed to create handler: %v", err)
				}
				defer v.Close()

				err = v.Ping()
				if (err != nil) != tt.expectError {
					t.Errorf("Ping() error = %v, expectError %v", err, tt.expectError)
				}
			}
		})
	}
}

// TestClose tests the graceful shutdown functionality.
func TestClose(t *testing.T) {
	tests := []struct {
		name          string
		numLogs       int
		expectFlushed int
	}{
		{
			name:          "close with no pending logs",
			numLogs:       0,
			expectFlushed: 0,
		},
		{
			name:          "close without batching",
			numLogs:       3,
			expectFlushed: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var flushedLogs []string
			var mu sync.Mutex

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				for _, line := range lines {
					if line != "" {
						var logEntry map[string]interface{}
						if err := json.Unmarshal([]byte(line), &logEntry); err == nil {
							flushedLogs = append(flushedLogs, logEntry["msg"].(string))
						}
					}
				}
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			v, err := New(
				WithURL(server.URL),
			)
			if err != nil {
				t.Fatalf("failed to create handler: %v", err)
			}

			for i := 0; i < tt.numLogs; i++ {
				entry := &lx.Entry{
					Timestamp: time.Now(),
					Level:     lx.LevelInfo,
					Message:   fmt.Sprintf("log %d", i),
					Namespace: "test.close",
					Fields:    nil,
				}
				if err := v.Handle(entry); err != nil {
					t.Errorf("Handle() error = %v", err)
				}
			}

			if err := v.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}

			mu.Lock()
			actualFlushed := len(flushedLogs)
			mu.Unlock()

			if actualFlushed != tt.expectFlushed {
				t.Errorf("expected %d logs flushed, got %d", tt.expectFlushed, actualFlushed)
			}
		})
	}
}

// TestConcurrentHandling tests thread-safe concurrent logging.
func TestConcurrentHandling(t *testing.T) {
	const numGoroutines = 10
	const logsPerGoroutine = 100

	var receivedLogs []string
	var mu sync.Mutex
	totalExpected := numGoroutines * logsPerGoroutine

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		for _, line := range lines {
			if line != "" {
				var logEntry map[string]interface{}
				if err := json.Unmarshal([]byte(line), &logEntry); err == nil {
					receivedLogs = append(receivedLogs, logEntry["msg"].(string))
				}
			}
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	v, err := New(
		WithURL(server.URL),
	)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	defer v.Close()

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < logsPerGoroutine; j++ {
				entry := &lx.Entry{
					Timestamp: time.Now(),
					Level:     lx.LevelInfo,
					Message:   fmt.Sprintf("goroutine-%d-log-%d", goroutineID, j),
					Namespace: "test.concurrent",
					Fields:    nil,
				}
				if err := v.Handle(entry); err != nil {
					t.Errorf("Handle() error from goroutine %d: %v", goroutineID, err)
				}
			}
		}(i)
	}

	wg.Wait()

	if err := v.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}

	mu.Lock()
	actualCount := len(receivedLogs)
	mu.Unlock()

	if actualCount != totalExpected {
		t.Errorf("expected %d logs, received %d", totalExpected, actualCount)
	}
}
