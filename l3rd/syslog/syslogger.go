package syslog

import (
	"fmt"
	"log/syslog"
	"strings"

	"github.com/olekukonko/ll/lx"
)

// Config holds configuration for Syslog handler.
type Config struct {
	// Tag is the application identifier (max 32 chars, will be truncated if longer).
	// Default: "golang-app"
	Tag string

	// Facility is the syslog facility (e.g., syslog.LOG_USER, syslog.LOG_LOCAL0).
	// Default: syslog.LOG_USER
	Facility syslog.Priority

	// Priority is the optional initial priority (defaults to syslog.LOG_INFO).
	// Default: syslog.LOG_INFO
	Priority syslog.Priority

	// Network is the network protocol for remote syslog ("tcp", "tcp4", "tcp6", "udp", "udp4", "udp6").
	// If set, connects to remote syslog; otherwise, uses local.
	// Default: "" (local)
	Network string

	// Addr is the remote address for syslog (e.g., "logs.example.com:514").
	// Required if Network is set.
	// Default: ""
	Addr string
}

// Option is a function that modifies Config.
type Option func(*Config)

// WithTag sets the application tag/ident.
func WithTag(tag string) Option {
	return func(c *Config) {
		c.Tag = tag
	}
}

// WithFacility sets the syslog facility.
func WithFacility(facility syslog.Priority) Option {
	return func(c *Config) {
		c.Facility = facility
	}
}

// WithPriority sets the initial priority.
func WithPriority(priority syslog.Priority) Option {
	return func(c *Config) {
		c.Priority = priority
	}
}

// WithRemote sets the network and address for remote syslog.
func WithRemote(network, addr string) Option {
	return func(c *Config) {
		c.Network = network
		c.Addr = addr
	}
}

// Syslog is an lx.Handler that sends log entries to the system syslog daemon.
// It integrates with the local syslog service (via Unix sockets or network) and maps
// log levels to appropriate syslog priority levels. This handler is useful for
// applications that need to integrate with system monitoring tools or centralized
// log management solutions.
//
// The handler supports:
// - Automatic level mapping (lx.LevelType to syslog.Priority)
// - Configurable tag/ident for log identification
// - Both local and remote syslog destinations
// - Structured fields as part of log message
//
// Example:
//
//	handler, err := syslog.New(
//	  syslog.WithTag("myapp"),
//	  syslog.WithFacility(syslog.LOG_LOCAL0),
//	)
//	if err != nil {
//	  log.Fatal(err)
//	}
//	logger := ll.New("app").Enable().Handler(handler)
//	logger.Info("Application started") // Sent to syslog
type Syslog struct {
	writer *syslog.Writer // Underlying syslog writer
	tag    string         // Application tag/ident for syslog entries
}

// New creates a new Syslog handler based on the provided options.
// It connects to either local or remote syslog depending on configuration.
// Returns a configured Syslog or an error if connection fails.
//
// Example:
//
//	handler, err := syslog.New(
//	  syslog.WithTag("my-service"),
//	  syslog.WithFacility(syslog.LOG_LOCAL0),
//	  syslog.WithRemote("tcp", "logs.company.com:6514"),
//	)
//	if err != nil {
//	  return err
//	}
func New(opts ...Option) (*Syslog, error) {
	// Initialize default configuration
	config := &Config{
		Tag:      "golang-app",
		Facility: syslog.LOG_USER,
		Priority: syslog.LOG_INFO,
		Network:  "",
		Addr:     "",
	}

	// Apply provided options
	for _, opt := range opts {
		opt(config)
	}

	// Truncate tag to syslog limits (typically 32 chars)
	if len(config.Tag) > 32 {
		config.Tag = config.Tag[:32]
	}

	var writer *syslog.Writer
	var err error

	if config.Network != "" && config.Addr != "" {
		// Connect to remote syslog
		writer, err = syslog.Dial(config.Network, config.Addr, config.Facility|config.Priority, config.Tag)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to remote syslog at %s://%s: %w", config.Network, config.Addr, err)
		}
	} else {
		// Connect to local syslog
		writer, err = syslog.New(config.Facility|config.Priority, config.Tag)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to local syslog: %w", err)
		}
	}

	return &Syslog{
		writer: writer,
		tag:    config.Tag,
	}, nil
}

// Handle implements the lx.Handler interface for Syslog.
// It receives log entries, maps lx log levels to syslog priorities, formats the
// message with namespace and fields, and sends it to syslog. Thread-safe via the
// underlying syslog.Writer implementation.
//
// Returns nil on successful delivery, or an error if syslog write fails.
//
// Example (internal usage):
//
//	handler.Handle(&lx.Entry{Message: "error occurred", Level: lx.LevelError})
func (h *Syslog) Handle(e *lx.Entry) error {
	// Map lx level to syslog priority
	priority := h.mapLevelToPriority(e.Level)

	// Build formatted message
	message := h.formatMessage(e)

	// Write to syslog based on priority
	var err error
	switch priority {
	case syslog.LOG_EMERG:
		err = h.writer.Emerg(message)
	case syslog.LOG_ALERT:
		err = h.writer.Alert(message)
	case syslog.LOG_CRIT:
		err = h.writer.Crit(message)
	case syslog.LOG_ERR:
		err = h.writer.Err(message)
	case syslog.LOG_WARNING:
		err = h.writer.Warning(message)
	case syslog.LOG_NOTICE:
		err = h.writer.Notice(message)
	case syslog.LOG_INFO:
		err = h.writer.Info(message)
	case syslog.LOG_DEBUG:
		err = h.writer.Debug(message)
	default:
		err = h.writer.Info(message)
	}

	return err
}

// Close closes the connection to the syslog daemon.
// It should be called when the handler is no longer needed to release system resources.
// Returns nil if successful, or an error if the close operation fails.
func (h *Syslog) Close() error {
	return h.writer.Close()
}

// Timestamped implements the lx.Timestamper interface.
// This is a no-op for Syslog handler since timestamps are handled by syslog.
// The method exists for interface compatibility.
//
// Parameters:
// - enable: Ignored
// - format: Ignored
func (h *Syslog) Timestamped(enable bool, format ...string) {
	// Syslog handles timestamps internally
}

// mapLevelToPriority maps lx.LevelType to syslog.Priority.
// This mapping determines how log levels are represented in the syslog system,
// affecting filtering, routing, and alerting in log management tools.
func (h *Syslog) mapLevelToPriority(level lx.LevelType) syslog.Priority {
	switch level {
	case lx.LevelDebug:
		return syslog.LOG_DEBUG
	case lx.LevelInfo:
		return syslog.LOG_INFO
	case lx.LevelWarn:
		return syslog.LOG_WARNING
	case lx.LevelError, lx.LevelFatal:
		return syslog.LOG_ERR
	default:
		return syslog.LOG_INFO
	}
}

// formatMessage formats an lx.Entry into a string suitable for syslog.
// It includes the namespace, message, and structured fields in a readable format.
// Fields are appended as key=value pairs for easy parsing by log analysis tools.
func (h *Syslog) formatMessage(e *lx.Entry) string {
	var builder strings.Builder

	// Add namespace if present
	if e.Namespace != "" {
		builder.WriteString("[")
		builder.WriteString(e.Namespace)
		builder.WriteString("] ")
	}

	// Add main message
	builder.WriteString(e.Message)

	// Add fields if present
	if len(e.Fields) > 0 {
		builder.WriteString(" [")
		first := true
		for _, f := range e.Fields {
			if !first {
				builder.WriteString(" ")
			}
			builder.WriteString(f.Key)
			builder.WriteString("=")
			builder.WriteString(fmt.Sprint(f.Value))
			first = false
		}
		builder.WriteString("]")
	}

	// Add stack trace if present
	if len(e.Stack) > 0 {
		builder.WriteString("\nStack trace:\n")
		builder.Write(e.Stack)
	}

	return builder.String()
}
