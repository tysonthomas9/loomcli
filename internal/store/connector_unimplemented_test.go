package store

import (
	"context"
	"errors"
	"testing"
)

// The placeholders must fail closed: every method errors with
// errors.ErrUnsupported until the real backends land (CV2/CV3).
func TestUnimplementedConnectorStoresFailClosed(t *testing.T) {
	ctx := context.Background()
	cs := UnimplementedConnectorStore{Backend: "test"}
	gs := UnimplementedConnectorGrantStore{Backend: "test"}
	as := UnimplementedConnectorAuditStore{Backend: "test"}

	tests := []struct {
		name string
		call func() error
	}{
		{"connector create", func() error { _, err := cs.Create(ctx, ConnectorCreate{}); return err }},
		{"connector get", func() error { _, err := cs.Get(ctx, "ws", "c"); return err }},
		{"connector list", func() error { _, err := cs.List(ctx, "ws", ConnectorFilter{}); return err }},
		{"resolve inbound secret", func() error { _, err := cs.ResolveInboundSecret(ctx, "ws", "c"); return err }},
		{"resolve outbound credential", func() error { _, err := cs.ResolveOutboundCredentialSealed(ctx, "ws", "c"); return err }},
		{"rotate secrets", func() error { _, err := cs.RotateSecrets(ctx, "ws", "c", ConnectorSecretRotation{}); return err }},
		{"grant create", func() error { _, err := gs.Create(ctx, ConnectorGrantCreate{}); return err }},
		{"grant revoke", func() error { return gs.Revoke(ctx, "ws", "g") }},
		{"grant list by binding", func() error { _, err := gs.ListByBinding(ctx, "ws", "b"); return err }},
		{"grant list by connector", func() error { _, err := gs.ListByConnector(ctx, "ws", "c"); return err }},
		{"audit append", func() error { return as.Append(ctx, nil) }},
		{"audit list by run", func() error { _, err := as.ListByRun(ctx, "ws", "r", ConnectorCallFilter{}); return err }},
		{"audit list by binding", func() error { _, err := as.ListByBinding(ctx, "ws", "b", ConnectorCallFilter{}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if !errors.Is(err, errors.ErrUnsupported) {
				t.Fatalf("err = %v, want errors.Is(errors.ErrUnsupported)", err)
			}
		})
	}
}
