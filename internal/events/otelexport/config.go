package otelexport

import "time"

// Config holds OpenTelemetry export configuration.
// It is shared by the platform runtime and short-lived command processes.
type Config struct {
	Enabled         bool          `yaml:"enabled,omitempty"`
	Endpoint        string        `yaml:"endpoint,omitempty"`
	Protocol        string        `yaml:"protocol,omitempty"`
	ServiceName     string        `yaml:"service_name,omitempty"`
	SampleRate      float64       `yaml:"sample_rate,omitempty"`
	FlushInterval   time.Duration `yaml:"-"` // computed from FlushIntervalMs
	FlushIntervalMs int           `yaml:"flush_interval_ms,omitempty"`
	Traces          *bool         `yaml:"traces,omitempty"`
	Metrics         *bool         `yaml:"metrics,omitempty"`
}

// TracesEnabled returns whether trace export is enabled.
func (c Config) TracesEnabled() bool {
	return c.Traces == nil || *c.Traces
}

// MetricsEnabled returns whether metrics export is enabled.
func (c Config) MetricsEnabled() bool {
	return c.Metrics == nil || *c.Metrics
}

// Resolved returns a copy with defaults applied and computed fields filled in.
func (c Config) Resolved() Config {
	out := c
	if out.Endpoint == "" {
		out.Endpoint = "http://localhost:4318"
	}
	if out.ServiceName == "" {
		out.ServiceName = "loomcli"
	}
	if out.SampleRate <= 0 || out.SampleRate > 1.0 {
		out.SampleRate = 1.0
	}
	if out.FlushIntervalMs <= 0 {
		out.FlushIntervalMs = 60000
	}
	out.FlushInterval = time.Duration(out.FlushIntervalMs) * time.Millisecond
	return out
}
