package cmdstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// installInMemorySpanExporter swaps the global TracerProvider for an
// in-memory recorder for the duration of the test (same pattern as
// internal/cli/git_runner_tracing_test.go).
func installInMemorySpanExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
	})
	return exp
}

// TestTracedConnectorStores_SpansEmitted_NoSecretsInTraceOutput drives every
// connector sub-store method through the tracing wrapper and asserts:
//
//  1. each method emits its service.Store.<Sub>.<Method> span, and
//  2. NO span carries the inbound secrets or sealed credential anywhere in
//     its name, attributes, status, or events — the resolve paths are
//     privileged and their results must never reach trace output.
func TestTracedConnectorStores_SpansEmitted_NoSecretsInTraceOutput(t *testing.T) {
	exp := installInMemorySpanExporter(t)
	ctx := context.Background()

	const (
		inboundSecret  = "whsec-current-SECRET"
		rotatedSecret  = "whsec-rotated-SECRET"
		sealedOutbound = "sealed-ciphertext-SECRET"
	)

	wrapped := WrapStoreWithTracing(memstore.New())
	connectors := wrapped.Connectors()
	if _, err := connectors.Create(ctx, store.ConnectorCreate{
		WorkspaceKey:             "WS",
		ConnectorID:              "gh-main",
		SourceKind:               domain.ConnectorSourceGitHub,
		InboundSecret:            inboundSecret,
		OutboundCredentialSealed: []byte(sealedOutbound),
	}); err != nil {
		t.Fatalf("Create connector: %v", err)
	}
	if _, err := connectors.Get(ctx, "WS", "gh-main"); err != nil {
		t.Fatalf("Get connector: %v", err)
	}
	if _, err := connectors.List(ctx, "WS", store.ConnectorFilter{}); err != nil {
		t.Fatalf("List connectors: %v", err)
	}
	secrets, err := connectors.ResolveInboundSecret(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("ResolveInboundSecret: %v", err)
	}
	if secrets.Current != inboundSecret {
		t.Fatalf("ResolveInboundSecret passthrough = %+v", secrets)
	}
	sealed, err := connectors.ResolveOutboundCredentialSealed(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
	}
	if string(sealed) != sealedOutbound {
		t.Fatalf("ResolveOutboundCredentialSealed passthrough = %q", sealed)
	}
	if _, err := connectors.RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
		NewInboundSecret: rotatedSecret,
	}); err != nil {
		t.Fatalf("RotateSecrets: %v", err)
	}

	grants := wrapped.ConnectorGrants()
	if _, err := grants.Create(ctx, store.ConnectorGrantCreate{
		WorkspaceKey:    "WS",
		GrantID:         "grant-1",
		ConnectorID:     "gh-main",
		BindingID:       "binding-1",
		Action:          "github.merge",
		ResourcePattern: "repo:octocat/hello",
	}); err != nil {
		t.Fatalf("Create grant: %v", err)
	}
	if _, err := grants.ListByBinding(ctx, "WS", "binding-1"); err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	if _, err := grants.ListByConnector(ctx, "WS", "gh-main"); err != nil {
		t.Fatalf("ListByConnector: %v", err)
	}
	if err := grants.Revoke(ctx, "WS", "grant-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Double-revoke exercises the record-error branch in the wrapper.
	_ = grants.Revoke(ctx, "WS", "grant-1")

	calls := wrapped.ConnectorCalls()
	if err := calls.Append(ctx, &domain.ConnectorCallRecord{
		WorkspaceKey: "WS",
		CallID:       domain.ConnectorCallID("run-1", "github.merge", 1),
		Seq:          1,
		RunID:        "run-1",
		BindingID:    "binding-1",
		ConnectorID:  "gh-main",
		SourceKind:   domain.ConnectorSourceGitHub,
		Action:       "github.merge",
		Decision:     domain.ConnectorCallGranted,
		OccurredAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("audit Append: %v", err)
	}
	if _, err := calls.ListByRun(ctx, "WS", "run-1", store.ConnectorCallFilter{}); err != nil {
		t.Fatalf("audit ListByRun: %v", err)
	}
	if _, err := calls.ListByBinding(ctx, "WS", "binding-1", store.ConnectorCallFilter{}); err != nil {
		t.Fatalf("audit ListByBinding: %v", err)
	}

	spans := exp.GetSpans()
	wantSpans := []string{
		"service.Store.Connectors.Create",
		"service.Store.Connectors.Get",
		"service.Store.Connectors.List",
		"service.Store.Connectors.ResolveInboundSecret",
		"service.Store.Connectors.ResolveOutboundCredentialSealed",
		"service.Store.Connectors.RotateSecrets",
		"service.Store.ConnectorGrants.Create",
		"service.Store.ConnectorGrants.ListByBinding",
		"service.Store.ConnectorGrants.ListByConnector",
		"service.Store.ConnectorGrants.Revoke",
		"service.Store.ConnectorCalls.Append",
		"service.Store.ConnectorCalls.ListByRun",
		"service.Store.ConnectorCalls.ListByBinding",
	}
	got := make(map[string]int, len(spans))
	for _, span := range spans {
		got[span.Name]++
	}
	for _, name := range wantSpans {
		if got[name] == 0 {
			t.Errorf("missing span %q (got %v)", name, got)
		}
	}
	if got["service.Store.ConnectorGrants.Revoke"] != 2 {
		t.Errorf("Revoke spans = %d, want 2 (success + error branch)", got["service.Store.ConnectorGrants.Revoke"])
	}

	// Redaction sweep: no secret material anywhere in any span's name,
	// attributes, status description, or events.
	for _, span := range spans {
		var sb strings.Builder
		sb.WriteString(span.Name)
		sb.WriteString(span.Status.Description)
		for _, attr := range span.Attributes {
			sb.WriteString(string(attr.Key))
			sb.WriteString(attr.Value.Emit())
		}
		for _, ev := range span.Events {
			sb.WriteString(ev.Name)
			for _, attr := range ev.Attributes {
				sb.WriteString(string(attr.Key))
				sb.WriteString(attr.Value.Emit())
			}
		}
		dump := sb.String()
		for _, secret := range []string{inboundSecret, rotatedSecret, sealedOutbound} {
			if strings.Contains(dump, secret) {
				t.Errorf("span %q leaks secret %q in trace output:\n%s", span.Name, secret, dump)
			}
		}
	}

	// Sanity: error branch recorded a low-cardinality status, not the
	// underlying message verbatim with IDs/secrets.
	for _, span := range spans {
		if span.Name == "service.Store.ConnectorGrants.Revoke" && span.Status.Description != "" {
			if span.Status.Description != "unknown" && !isKnownStoreErrReason(span.Status.Description) {
				t.Errorf("Revoke error status = %q, want low-cardinality reason", span.Status.Description)
			}
		}
	}
}

func isKnownStoreErrReason(reason string) bool {
	switch reason {
	case "not_found", "already_exists", "invalid", "conflict":
		return true
	}
	return false
}
