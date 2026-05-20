package tracing

import (
	"context"
	"strings"
	"testing"
)

func TestInitDisabledAndValidationBranches(t *testing.T) {
	shutdown, provider, err := Init(context.Background(), Config{})
	if err != nil {
		t.Fatalf("Init disabled: %v", err)
	}
	if provider() != nil {
		t.Fatal("disabled provider should be nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("disabled shutdown: %v", err)
	}

	if _, _, err := Init(context.Background(), Config{Endpoint: "localhost:4318"}); err == nil || !strings.Contains(err.Error(), "ServiceName required") {
		t.Fatalf("Init missing service err = %v", err)
	}
}

func TestInitEnabledBuildsProviderWithoutCollectorConnection(t *testing.T) {
	shutdown, provider, err := Init(context.Background(), Config{
		ServiceName:    "loom-test",
		ServiceVersion: "test",
		Environment:    "test",
		Endpoint:       "http://127.0.0.1:1",
		Insecure:       true,
		AlwaysOn:       true,
		Sync:           true,
	})
	if err != nil {
		t.Fatalf("Init enabled: %v", err)
	}
	if provider == nil || provider() == nil {
		t.Fatal("enabled provider should be non-nil")
	}
	if shutdown == nil {
		t.Fatal("enabled shutdown should be non-nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("enabled shutdown without spans: %v", err)
	}
}

func TestBuildResourceSamplerAndProviderOptions(t *testing.T) {
	res, err := buildResource(context.Background(), Config{ServiceName: "loom-test"})
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if res == nil {
		t.Fatal("buildResource returned nil resource")
	}

	if got := stripScheme("https://collector:4318/"); got != "collector:4318" {
		t.Fatalf("stripScheme https = %q", got)
	}
	if got := stripScheme("http://collector:4318/v1/traces"); got != "collector:4318/v1/traces" {
		t.Fatalf("stripScheme http = %q", got)
	}
	if got := strDefault("  ", "fallback"); got != "fallback" {
		t.Fatalf("strDefault blank = %q", got)
	}
	if got := strDefault("value", "fallback"); got != "value" {
		t.Fatalf("strDefault value = %q", got)
	}

	cases := []Config{
		{AlwaysOn: true},
		{SampleRate: -1},
		{SampleRate: 0.25},
		{SampleRate: 2},
	}
	for _, cfg := range cases {
		if sampler := buildSampler(cfg); sampler == nil {
			t.Fatalf("buildSampler(%+v) returned nil", cfg)
		}
	}

	opts := buildProviderOptions(nil, res, Config{Sync: true, AlwaysOn: true})
	if len(opts) == 0 {
		t.Fatal("sync provider options empty")
	}
	opts = buildProviderOptions(nil, res, Config{Sync: false, SampleRate: 0.5})
	if len(opts) == 0 {
		t.Fatal("batch provider options empty")
	}

}
