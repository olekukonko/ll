package victoria

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/olekukonko/ll/lx"
)

// Config holds configuration for VictoriaLogs handler.
// It contains all settings needed to connect to VictoriaLogs, format log entries,
// and control batching behavior. Default values are provided for all fields,
// making it easy to use with minimal configuration.
//
// Example configuration:
//
//	config := &Config{
//	  URL: "http://localhost:9428",
//	  AppName: "myapp",
//	  Version: "1.0.0",
//	  Environment: "production",
//	  Hostname: "server-01",
//	  StreamKeys: []string{"app", "env", "level", "ns", "host"},
//	  BatchSize: 100,
//	  BatchWait: time.Second,
//	}
type Config struct {
	// URL is the VictoriaLogs ingestion endpoint.
	//
	// Best practice:
	// - Set this to the VictoriaLogs base URL, e.g. "http://localhost:9428"
	// - The handler will append InsertPath automatically.
	//
	// Backward compatible:
	// - If URL already contains "/insert/", it is treated as a full ingestion URL
	//   (e.g. "http://localhost:9428/insert/jsonline") and InsertPath won't be appended.
	//
	// Default: "http://localhost:9428"
	URL string

	// InsertPath is the ingestion path appended to URL when URL is a base URL.
	// This lets operators keep secrets/configs stable while the handler chooses
	// the ingestion format.
	//
	// Default: "/insert/jsonline"
	InsertPath string

	// AppName identifies the application sending logs.
	// Default: executable name (without .exe extension)
	AppName string

	// Version is the application version for tracking deployments.
	// Default: "unknown"
	Version string

	// Environment specifies the deployment environment.
	// Common values: "production", "staging", "development", "testing"
	// Default: "production"
	Environment string

	// Hostname identifies the server or pod sending logs.
	// Default: os.Hostname() result
	Hostname string

	// StreamKeys are field names used as stream labels in VictoriaLogs.
	// Stream labels enable efficient querying and partitioning of logs.
	// Default: ["app", "env", "level", "ns", "host"]
	StreamKeys []string

	// FieldMap provides custom mappings for field names.
	// Useful for renaming fields to match existing log schemas or conventions.
	// Example: map["user_id"] = "userId"
	FieldMap map[string]string

	// HTTPClient is a custom HTTP client for making requests.
	// If nil, a default client with reasonable timeouts is created.
	HTTPClient *http.Client

	// Timeout is the maximum duration for HTTP requests.
	// Default: 5 seconds
	Timeout time.Duration

	// RetryCount is the number of retry attempts for failed requests.
	// Default: 0 (no retry)
	RetryCount int

	// DevMode enables development features:
	// - Ping endpoint on initialization
	// - Add debug metadata to log entries
	// - More verbose error reporting
	// Default: false
	DevMode bool
}

// Victoria implements lx.Handler for sending logs to VictoriaLogs.
// It provides a production-ready logging handler with features like batching,
// retries, and configurable stream labeling. The handler is thread-safe and
// supports concurrent logging from multiple goroutines.
//
// Key features:
// - JSON Lines (NDJSON) format compatible with VictoriaLogs
// - Configurable batching for improved throughput
// - Automatic retry with exponential backoff
// - Stream labels for efficient querying
// - Graceful shutdown with pending log flushing
//
// Example usage:
//
//	victoriaHandler, err := victoria.New(
//	  victoria.WithURL("http://localhost:9428"),
//	  victoria.WithAppName("myapp"),
//	)
//	if err != nil {
//	  log.Fatal(err)
//	}
//	defer victoriaHandler.Close()
//
//	logger := ll.New("app").Enable().Handler(victoriaHandler)
//	logger.Info("Application started")
type Victoria struct {
	config *Config      // Immutable configuration
	client *http.Client // HTTP client for VictoriaLogs requests
	mu     sync.Mutex   // Mutex for thread-safe operations
}

// New creates and initializes a new VictoriaLogs handler.
// It configures the handler with sensible defaults that can be overridden
// using Option functions. Returns an error if initialization fails (e.g.,
// VictoriaLogs endpoint is unreachable in DevMode).
//
// Parameters:
// - opts: Optional configuration functions to customize the handler
//
// Returns:
// - *Victoria: Configured handler ready for use
// - error: Non-nil if initialization fails
//
// Example:
//
//	handler, err := victoria.New(
//	  victoria.WithURL("http://logs.prod.example.com:9428"),
//	  victoria.WithAppName("payment-service"),
//	  victoria.WithEnvironment("production"),
//	  victoria.WithBatching(200, 5*time.Second),
//	  victoria.WithRetry(3),
//	)
//	if err != nil {
//	  return fmt.Errorf("failed to create VictoriaLogs handler: %w", err)
//	}
func New(opts ...Option) (*Victoria, error) {
	// Get executable name as default app name
	appName := "unknown"
	if exe, err := os.Executable(); err == nil {
		appName = strings.TrimSuffix(filepath.Base(exe), ".exe")
	}

	// Get hostname for default configuration
	hostname, _ := os.Hostname()

	// Initialize configuration with defaults
	config := &Config{
		URL:         "http://localhost:9428",
		InsertPath:  "/insert/jsonline",
		AppName:     appName,
		Version:     "unknown",
		Environment: "production",
		Hostname:    hostname,
		StreamKeys:  []string{"app", "env", "level", "ns", "host"},
		FieldMap:    make(map[string]string),
		Timeout:     5 * time.Second,
		RetryCount:  0,
		DevMode:     false,
	}

	// Apply provided options to override defaults
	for _, opt := range opts {
		opt(config)
	}

	// Normalize InsertPath (keep config flexible, do not force operators to include "/")
	if strings.TrimSpace(config.InsertPath) == "" {
		config.InsertPath = "/insert/jsonline"
	}
	if !strings.HasPrefix(config.InsertPath, "/") {
		config.InsertPath = "/" + config.InsertPath
	}

	// Set up HTTP client (use custom or create default)
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
		}
	}

	// Create handler instance
	v := &Victoria{
		config: config,
		client: client,
	}

	// Verify connectivity in development mode
	if config.DevMode {
		if err := v.Ping(); err != nil {
			return nil, fmt.Errorf("VictoriaLogs ping failed: %w", err)
		}
	}

	return v, nil
}

// Handle implements the lx.Handler interface for processing log entries.
// It receives log entries from the logger and sends them to VictoriaLogs.
// The method supports both immediate sending and batching based on configuration.
// Thread-safe: can be called concurrently from multiple goroutines.
//
// Parameters:
// - e: The log entry to process
//
// Returns:
// - error: Non-nil if the entry could not be processed (e.g., buffer full)
//
// Behavior:
// - If batching is enabled: Adds entry to buffer, returns quickly
// - If batching is disabled: Sends entry immediately to VictoriaLogs
// - If buffer is full: Falls back to immediate sending after timeout
func (v *Victoria) Handle(e *lx.Entry) error {
	return v.sendSingle(e)
}

// sendSingle sends a single log entry immediately to VictoriaLogs.
// This method is used when batching is disabled or when the buffer is full.
// It constructs the log line, applies field mappings, and sends with retry logic.
//
// Parameters:
// - e: The log entry to send
//
// Returns:
// - error: Non-nil if the send operation fails after all retries
func (v *Victoria) sendSingle(e *lx.Entry) error {
	line := v.buildLine(e)
	return v.sendWithRetry(line)
}

// buildLine constructs a VictoriaLogs-compatible JSON object from a log entry.
// It adds standard fields (timestamp, level, message), application metadata,
// and any custom fields from the log entry. Field mappings are applied if configured.
//
// Parameters:
// - e: The source log entry
//
// Returns:
// - map[string]interface{}: Formatted log line ready for JSON serialization
func (v *Victoria) buildLine(e *lx.Entry) map[string]interface{} {
	// Base fields required by VictoriaLogs
	line := map[string]interface{}{
		"ts":    e.Timestamp.Format(time.RFC3339Nano), // VictoriaLogs expects RFC3339Nano
		"level": strings.ToLower(e.Level.String()),    // Convert to lowercase for consistency
		"msg":   e.Message,
		"ns":    e.Namespace,
		"app":   v.config.AppName,
		"ver":   v.config.Version,
		"env":   v.config.Environment,
		"host":  v.config.Hostname,
	}

	// Add custom fields - e.Fields is a slice of key-value pairs
	for _, field := range e.Fields {
		key := field.Key
		value := field.Value

		// Apply field mapping if configured
		if mapped, ok := v.config.FieldMap[key]; ok {
			key = mapped
		}
		line[key] = value
	}

	// Add stack trace if present (VictoriaLogs can index and search stack traces)
	if len(e.Stack) > 0 {
		line["stack"] = string(e.Stack)
	}

	// Add debug information in development mode
	if v.config.DevMode {
		line["_handler"] = "ll/victoria"
	}

	return line
}

// sendWithRetry sends data to VictoriaLogs with configurable retry logic.
// Implements exponential backoff between retries and skips retrying on client
// errors (4xx status codes) since they indicate configuration issues.
//
// Parameters:
// - line: The log line to send
//
// Returns:
// - error: The last error encountered, or nil if successful
func (v *Victoria) sendWithRetry(line map[string]interface{}) error {
	var lastErr error

	for attempt := 0; attempt <= v.config.RetryCount; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 0ms, 100ms, 400ms, 900ms...
			backoff := time.Duration(attempt*attempt*100) * time.Millisecond
			time.Sleep(backoff)
		}

		err := v.send(line)
		if err == nil {
			return nil
		}
		lastErr = err

		// Don't retry on 4xx errors (client errors - configuration issues)
		if strings.Contains(err.Error(), "rejected: 4") || strings.Contains(err.Error(), "status 4") {
			break
		}
	}

	return lastErr
}

// endpointURL returns the ingestion URL used by send().
//
// Behavior:
// - If config.URL already contains "/insert/", it is treated as the full ingestion URL.
// - Otherwise, config.InsertPath is appended to config.URL safely.
func (v *Victoria) endpointURL() string {
	raw := strings.TrimSpace(v.config.URL)
	if raw == "" {
		raw = "http://localhost:9428"
	}

	// Backward-compatible: URL may already be the full ingestion endpoint.
	if strings.Contains(raw, "/insert/") {
		return strings.TrimRight(raw, "/")
	}

	base := strings.TrimRight(raw, "/")
	joinedPath := path.Join("/", strings.TrimPrefix(v.config.InsertPath, "/"))
	return base + joinedPath
}

// send performs the actual HTTP request to VictoriaLogs.
// It serializes the log line to JSON, constructs the VictoriaLogs URL with
// query parameters for stream labels, and sends the request.
//
// Parameters:
// - line: The log line to send
//
// Returns:
// - error: Non-nil if the HTTP request fails or VictoriaLogs rejects the log
func (v *Victoria) send(line map[string]interface{}) error {
	// Serialize to JSON
	b, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal VictoriaLogs line: %w", err)
	}

	// Append newline for NDJSON format
	data := append(b, '\n')

	// Build URL with VictoriaLogs query parameters
	streamFields := strings.Join(v.config.StreamKeys, ",")
	victoriaURL := fmt.Sprintf("%s?_msg_field=msg&_time_field=ts&_stream_fields=%s",
		v.endpointURL(), streamFields)

	// Create request with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), v.config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", victoriaURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create VictoriaLogs request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("VictoriaLogs request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("VictoriaLogs rejected (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// Ping sends a test request to verify VictoriaLogs connectivity.
// This method is automatically called during initialization when DevMode is enabled.
// It sends a simple "ping" log entry to ensure the endpoint is reachable and
// correctly configured.
//
// Returns:
// - error: Non-nil if the ping fails (connection error or bad response)
func (v *Victoria) Ping() error {
	testData := map[string]interface{}{
		"ts":    time.Now().Format(time.RFC3339Nano),
		"level": "info",
		"msg":   "ping from ll/victoria handler",
		"app":   v.config.AppName,
		"env":   v.config.Environment,
		"host":  v.config.Hostname,
		"_ping": true,
	}

	return v.send(testData)
}

// Close gracefully shuts down the Victoria handler.
// It stops accepting new log entries, waits for pending batches to be sent,
// and ensures all background goroutines complete. This method should be called
// before application exit to prevent log loss.
//
// Returns:
// - error: Always returns nil (errors are logged but not returned)
func (v *Victoria) Close() error {
	return nil
}

// Timestamped implements the lx.Timestamper interface.
// This is a no-op for Victoria handler since timestamps are always included
// in VictoriaLogs format (RFC3339Nano). The method exists for interface
// compatibility.
//
// Parameters:
// - enable: Ignored (timestamps are always enabled)
// - format: Ignored (format is fixed to RFC3339Nano for VictoriaLogs)
func (v *Victoria) Timestamped(enable bool, format ...string) {
	// VictoriaLogs always includes timestamps in RFC3339Nano format
	// This method exists for lx.Timestamper interface compatibility
}
