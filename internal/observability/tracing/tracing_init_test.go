package tracing

import (
	"context"
	"testing"
)

func TestConfig_Enabled(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     bool
	}{
		{"empty", "", false},
		{"whitespace", "  ", false},
		{"set", "localhost:4318", true},
		{"trimmed", "  localhost:4318  ", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Config{Endpoint: tc.endpoint}).Enabled(); got != tc.want {
				t.Errorf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStripScheme(t *testing.T) {
	cases := map[string]string{
		"http://host:4318":  "host:4318",
		"https://host:4318": "host:4318",
		"host:4318":         "host:4318",
		"http://host:4318/": "host:4318",
		"host:4318/path/":   "host:4318/path",
		"":                  "",
	}
	for in, want := range cases {
		if got := stripScheme(in); got != want {
			t.Errorf("stripScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStrDefault(t *testing.T) {
	if got := strDefault("value", "fb"); got != "value" {
		t.Errorf("strDefault(value) = %q", got)
	}
	if got := strDefault("", "fb"); got != "fb" {
		t.Errorf("strDefault(empty) = %q", got)
	}
	if got := strDefault("   ", "fb"); got != "fb" {
		t.Errorf("strDefault(whitespace) = %q", got)
	}
}

func TestBuildSampler(t *testing.T) {
	// AlwaysOn path.
	if s := buildSampler(Config{AlwaysOn: true}); s == nil {
		t.Error("buildSampler(AlwaysOn) returned nil")
	}
	// Default (rate=0 → coerced to 1.0).
	if s := buildSampler(Config{}); s == nil {
		t.Error("buildSampler(default) returned nil")
	}
	// Clamped (>1.0 → coerced to 1.0).
	if s := buildSampler(Config{SampleRate: 2.0}); s == nil {
		t.Error("buildSampler(over) returned nil")
	}
	// Mid-range.
	if s := buildSampler(Config{SampleRate: 0.5}); s == nil {
		t.Error("buildSampler(mid) returned nil")
	}
}

func TestInit_Disabled(t *testing.T) {
	// No endpoint = disabled. Init installs the propagator and returns
	// no-op Shutdown / nil-provider.
	shutdown, provider, err := Init(context.Background(), Config{})
	if err != nil {
		t.Fatalf("Init(disabled): %v", err)
	}
	if shutdown == nil {
		t.Fatal("disabled Shutdown is nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("disabled Shutdown(): %v", err)
	}
	if provider == nil {
		t.Fatal("disabled Provider is nil")
	}
	if tp := provider(); tp != nil {
		t.Errorf("disabled provider() = %v, want nil", tp)
	}
}

func TestInit_EnabledMissingServiceName(t *testing.T) {
	_, _, err := Init(context.Background(), Config{Endpoint: "localhost:4318"})
	if err == nil {
		t.Fatal("expected error when ServiceName is empty")
	}
}
