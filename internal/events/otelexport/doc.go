// Package otelexport publishes loom's domain events as OpenTelemetry traces
// and metrics.
//
// Exporter adapts the event stream onto OTel providers; WithTracerProvider and
// WithMeterProvider inject them, which keeps the exporter testable and lets a
// host application own provider configuration. The Attr* keys define loom's
// resource attributes (agent, role, and the rest) so events from different
// processes aggregate on the same dimensions.
//
// This package is an outbound adapter: internal/events stays free of any
// OpenTelemetry dependency, and only this package knows the two vocabularies
// meet.
package otelexport
