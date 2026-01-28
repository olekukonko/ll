package victoria

import (
	"net/http"
	"time"
)

// Option is a function that modifies Config.
// Used with the New() constructor for flexible configuration.
// Multiple options can be chained together.
//
// Example:
//
//	handler, err := victoria.New(
//	  victoria.WithURL("http://logs.example.com"),
//	  victoria.WithAppName("api-server"),
//	  victoria.WithEnvironment("production"),
//	  victoria.WithBatching(100, 2*time.Second),
//	)
type Option func(*Config)

// WithURL sets the VictoriaLogs endpoint URL.
// The URL should point to VictoriaLogs' JSON ingestion endpoint.
// Default: "http://localhost:9428/insert/jsonline"
//
// Example:
//
//	victoria.WithURL("http://victoria-logs.prod.svc:9428/insert/jsonline")
func WithURL(url string) Option {
	return func(c *Config) {
		c.URL = url
	}
}

// WithAppName sets the application name for log identification.
// This appears in the "app" field of all log entries and is used as
// a stream label for efficient querying in VictoriaLogs.
// Default: executable name (without extension)
//
// Example:
//
//	victoria.WithAppName("order-service")
func WithAppName(name string) Option {
	return func(c *Config) {
		c.AppName = name
	}
}

// WithVersion sets the application version.
// Useful for tracking deployments and version-specific issues.
// Default: "unknown"
//
// Example:
//
//	victoria.WithVersion("2.1.0")
func WithVersion(version string) Option {
	return func(c *Config) {
		c.Version = version
	}
}

// WithEnvironment sets the deployment environment.
// Common values: "production", "staging", "development", "testing"
// This field is used as a stream label for environment-based filtering.
// Default: "production"
//
// Example:
//
//	victoria.WithEnvironment("staging")
func WithEnvironment(env string) Option {
	return func(c *Config) {
		c.Environment = env
	}
}

// WithHostname sets the hostname for log entries.
// Useful in distributed systems to identify which server or pod generated the log.
// Default: os.Hostname() result
//
// Example:
//
//	victoria.WithHostname("web-server-01")
func WithHostname(hostname string) Option {
	return func(c *Config) {
		c.Hostname = hostname
	}
}

// WithStreamKeys sets the fields to use as stream labels in VictoriaLogs.
// Stream labels enable efficient partitioning and querying of logs.
// Common stream keys: ["app", "env", "level", "ns", "host"]
// Default: ["app", "env", "level", "ns", "host"]
//
// Example:
//
//	// Use application and environment as primary stream labels
//	victoria.WithStreamKeys("app", "env", "level")
func WithStreamKeys(keys ...string) Option {
	return func(c *Config) {
		c.StreamKeys = keys
	}
}

// WithFieldMapping adds a custom field name mapping.
// Useful for renaming fields to match existing log schemas or conventions.
// The mapping is applied to all log entries processed by the handler.
//
// Example:
//
//	// Rename "user_id" field to "userId"
//	victoria.WithFieldMapping("user_id", "userId")
func WithFieldMapping(from, to string) Option {
	return func(c *Config) {
		c.FieldMap[from] = to
	}
}

// WithHTTPClient sets a custom HTTP client for VictoriaLogs requests.
// Use this to customize timeouts, authentication, TLS configuration,
// or to use a proxy. If not set, a default client with reasonable
// timeouts is created.
//
// Example:
//
//	client := &http.Client{
//	  Timeout: 30 * time.Second,
//	  Transport: &http.Transport{
//	    TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
//	  },
//	}
//	victoria.WithHTTPClient(client)
func WithHTTPClient(client *http.Client) Option {
	return func(c *Config) {
		c.HTTPClient = client
	}
}

// WithTimeout sets the HTTP request timeout.
// This timeout applies to both connection establishment and request completion.
// Consider network latency and VictoriaLogs processing time when setting this.
// Default: 5 seconds
//
// Example:
//
//	// Allow more time for cross-region requests
//	victoria.WithTimeout(10 * time.Second)
func WithTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.Timeout = timeout
	}
}

// WithRetry sets the number of retry attempts for failed requests.
// Retries use exponential backoff (0ms, 100ms, 400ms, ...).
// Note: 4xx errors (client errors) are not retried.
// Default: 0 (no retry)
//
// Example:
//
//	// Retry up to 3 times on network failures
//	victoria.WithRetry(3)
func WithRetry(count int) Option {
	return func(c *Config) {
		c.RetryCount = count
	}
}

// WithDevMode enables development mode features.
// When enabled:
// - Handler pings VictoriaLogs endpoint on initialization
// - Debug metadata (_handler, _batch) is added to log entries
// - May enable more verbose error reporting
//
// Default: false (disabled)
//
// Example:
//
//	// Enable during development, disable in production
//	victoria.WithDevMode(os.Getenv("ENV") == "development")
func WithDevMode(enabled bool) Option {
	return func(c *Config) {
		c.DevMode = enabled
	}
}
