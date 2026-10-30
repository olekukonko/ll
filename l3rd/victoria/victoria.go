package victoria

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	// BatchSize is the number of log lines sent per HTTP request when batching
	// is enabled. Zero (the default) keeps the original behaviour: every entry
	// is sent in its own request, synchronously, from the caller's goroutine.
	//
	// Set it with WithBatching(size, wait). Enabling batching moves delivery (and
	// retries) to a background goroutine, so logging no longer blocks on the
	// network.
	BatchSize int

	// BatchWait is the maximum time a partial batch waits before being sent.
	// Default: 0 (no batching). Only meaningful when BatchSize > 0.
	BatchWait time.Duration

	// OnError, when set, receives asynchronous delivery errors (batch sends).
	// Synchronous sends still return their error to the caller.
	OnError func(error)

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
	url    string       // Cached ingestion URL (endpoint + query parameters)
	mu     sync.Mutex   // Mutex for thread-safe operations

	// Batching state. All of it stays zero when batching is disabled, which
	// keeps the default (synchronous) code path identical to previous versions.
	batchCh   chan map[string]interface{}
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
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

	// Cache the ingestion URL once instead of rebuilding it for every request.
	v.url = v.requestURL()

	// Start the background sender only when batching was requested. With batching
	// disabled nothing changes: Handle sends synchronously and Close is a no-op.
	if config.BatchSize > 0 {
		if config.BatchWait <= 0 {
			config.BatchWait = time.Second
		}
		v.batchCh = make(chan map[string]interface{}, config.BatchSize*4)
		v.done = make(chan struct{})
		v.wg.Add(1)
		go v.batchWorker()
	}

	// Verify connectivity in development mode
	if config.DevMode {
		if err := v.Ping(); err != nil {
			return nil, fmt.Errorf("VictoriaLogs ping failed: %w", err)
		}
	}

	return v, nil
}

// ErrBufferFull is returned by Handle when the batching queue is full. Entries
// are rejected rather than blocking the caller or growing memory without bound.
var ErrBufferFull = errors.New("victoria: log buffer full")

// ErrClosed is returned by Handle when the handler has been closed.
var ErrClosed = errors.New("victoria: handler closed")

// report forwards an asynchronous error to the configured handler, if any.
func (v *Victoria) report(err error) {
	if err != nil && v.config != nil && v.config.OnError != nil {
		v.config.OnError(err)
	}
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
	// Batching disabled (default): unchanged synchronous behaviour.
	if v.batchCh == nil {
		return v.sendSingle(e)
	}

	line := v.buildLine(e)
	// Handle never blocks on the network while batching: if the queue is full
	// (or the handler has been closed) the entry is rejected instead, so a slow
	// VictoriaLogs endpoint cannot stall the caller.
	select {
	case <-v.done:
		v.report(ErrClosed)
		return ErrClosed
	default:
	}
	select {
	case v.batchCh <- line:
		return nil
	default:
		v.report(ErrBufferFull)
		return ErrBufferFull
	}
}

// batchWorker drains the queue into batches and sends them.
func (v *Victoria) batchWorker() {
	defer v.wg.Done()

	ticker := time.NewTicker(v.config.BatchWait)
	defer ticker.Stop()

	batch := make([]map[string]interface{}, 0, v.config.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		v.report(v.sendBatchWithRetry(batch))
		// Reset length, keep capacity.
		batch = batch[:0]
	}

	for {
		select {
		case line := <-v.batchCh:
			batch = append(batch, line)
			if len(batch) >= v.config.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-v.done:
			// Drain whatever is queued, then flush and exit.
			for {
				select {
				case line := <-v.batchCh:
					batch = append(batch, line)
					if len(batch) >= v.config.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
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
		if isClientError(err) {
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
	return v.post(append(b, '\n'))
}

// requestURL returns the ingestion URL including the VictoriaLogs query
// parameters. It is computed once at construction and cached in v.url.
func (v *Victoria) requestURL() string {
	if v.url != "" {
		return v.url
	}
	streamFields := strings.Join(v.config.StreamKeys, ",")
	return fmt.Sprintf("%s?_msg_field=msg&_time_field=ts&_stream_fields=%s",
		v.endpointURL(), streamFields)
}

// statusError reports a non-2xx response from VictoriaLogs.
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("VictoriaLogs rejected (status %d): %s", e.code, e.body)
}

// post sends an already encoded NDJSON payload.
func (v *Victoria) post(data []byte) error {
	// Create request with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), v.config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", v.requestURL(), bytes.NewReader(data))
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
		return &statusError{code: resp.StatusCode, body: string(body)}
	}

	return nil
}

// encodeBatch encodes log lines as a single NDJSON payload (one JSON object per
// line), which VictoriaLogs accepts in a single request.
func encodeBatch(lines []map[string]interface{}) ([]byte, error) {
	buf := make([]byte, 0, len(lines)*128)
	for _, line := range lines {
		b, err := json.Marshal(line)
		if err != nil {
			return nil, fmt.Errorf("marshal VictoriaLogs line: %w", err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	return buf, nil
}

// sendBatchWithRetry sends several log lines in one request, retrying with the
// same backoff policy as sendWithRetry. It is only used by the batch worker, so
// retries never block a logging caller.
func (v *Victoria) sendBatchWithRetry(lines []map[string]interface{}) error {
	payload, err := encodeBatch(lines)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt <= v.config.RetryCount; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 0ms, 100ms, 400ms, 900ms...
			backoff := time.Duration(attempt*attempt*100) * time.Millisecond
			time.Sleep(backoff)
		}
		err := v.post(payload)
		if err == nil {
			return nil
		}
		lastErr = err
		if isClientError(err) {
			break
		}
	}
	return lastErr
}

// isClientError reports whether err is a 4xx response (a configuration issue
// that will not be fixed by retrying).
func isClientError(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code >= 400 && se.code < 500
	}
	// Fallback for wrapped/opaque errors.
	return strings.Contains(err.Error(), "rejected (status 4") ||
		strings.Contains(err.Error(), "rejected: 4") ||
		strings.Contains(err.Error(), "status 4")
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
	v.closeOnce.Do(func() {
		if v.done != nil {
			close(v.done)
			v.wg.Wait()
		}
	})
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
