//go:build windows

package syslog

import (
	"fmt"

	"github.com/olekukonko/ll/lx"
)

// Config holds configuration for Syslog handler (Windows stub).
type Config struct {
	Tag      string
	Facility int
	Priority int
	Network  string
	Addr     string
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
func WithFacility(facility int) Option {
	return func(c *Config) {
		c.Facility = facility
	}
}

// WithPriority sets the initial priority.
func WithPriority(priority int) Option {
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

// Syslog is a stub handler for Windows (syslog not supported).
type Syslog struct{}

// New creates a new Syslog handler - returns error on Windows.
func New(opts ...Option) (*Syslog, error) {
	return nil, fmt.Errorf("syslog is not supported on Windows")
}

// Handle implements the lx.Handler interface (stub).
func (h *Syslog) Handle(e *lx.Entry) error {
	return nil
}

// Close closes the connection (stub).
func (h *Syslog) Close() error {
	return nil
}

// Timestamped implements the lx.Timestamper interface (stub).
func (h *Syslog) Timestamped(enable bool, format ...string) {}
