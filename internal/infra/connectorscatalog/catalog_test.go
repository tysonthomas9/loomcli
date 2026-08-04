package connectorscatalog_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorscatalog"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

type recordingSealer struct {
	plaintext []byte
	aad       []byte
}

func (sealer *recordingSealer) Seal(plaintext, aad []byte) ([]byte, error) {
	sealer.plaintext = append([]byte(nil), plaintext...)
	sealer.aad = append([]byte(nil), aad...)
	return []byte("sealed-rotated"), nil
}

func TestManagementOwnsRedactedCatalogGrantAndAuditQueries(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	backend := memstore.New()
	now := time.Now().UTC()
	adapter, err := connectorscatalog.New(
		backend.Connectors(), backend.ConnectorGrants(), backend.ConnectorCalls(),
	)
	if err != nil {
		t.Fatalf("compose adapter: %v", err)
	}
	sealer := &recordingSealer{}
	management, err := connectorsmodule.NewManagementWithSecrets(adapter, sealer, func() time.Time { return now })
	if err != nil {
		t.Fatalf("compose management: %v", err)
	}

	created, err := management.CreateConnector(ctx, connectorsmodule.CreateConnectorCommand{
		WorkspaceKey: "WS", ConnectorID: "github-main",
		SourceKind: connectorsmodule.ConnectorSourceGitHub, DisplayName: "GitHub",
		InboundEndpointPath: "/hooks/github", InboundSecret: "do-not-expose",
		OutboundCredentialSealed: []byte("sealed-do-not-expose"),
	})
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if created.Status != connectorsmodule.ConnectorStatusActive || created.ConnectorID != "github-main" {
		t.Fatalf("created projection = %+v", created)
	}
	listed, err := management.ListConnectors(ctx, connectorsmodule.ListConnectorsQuery{
		WorkspaceKey: "WS",
		Filter: connectorsmodule.ConnectorFilter{
			SourceKind: connectorsmodule.ConnectorSourceGitHub,
			Status:     connectorsmodule.ConnectorStatusActive,
		},
	})
	if err != nil || len(listed) != 1 || listed[0].ConnectorID != created.ConnectorID {
		t.Fatalf("list connectors = %+v, %v", listed, err)
	}
	replacement := []byte("replacement-credential")
	rotated, err := management.RotateConnector(ctx, connectorsmodule.RotateConnectorCommand{
		WorkspaceKey: "WS", ConnectorID: "github-main", NewInboundSecret: "rotated-secret",
		NewCredential: replacement, InboundWindow: 30 * time.Minute,
	})
	if err != nil || rotated.RotatedAt == nil {
		t.Fatalf("rotate connector = %+v, %v", rotated, err)
	}
	if !bytes.Equal(replacement, make([]byte, len(replacement))) {
		t.Fatalf("plaintext replacement was not wiped: %q", replacement)
	}
	if string(sealer.plaintext) != "replacement-credential" ||
		string(sealer.aad) != "loom-connector-credential\x00WS\x00github-main" {
		t.Fatalf("sealer input plaintext=%q aad=%q", sealer.plaintext, sealer.aad)
	}
	secrets, err := backend.Connectors().ResolveInboundSecret(ctx, "WS", "github-main")
	if err != nil || secrets.Current != "rotated-secret" || secrets.Previous != "do-not-expose" ||
		!secrets.PreviousValidUntil.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("rotated inbound secrets = %+v, %v", secrets, err)
	}
	sealed, err := backend.Connectors().ResolveOutboundCredentialSealed(ctx, "WS", "github-main")
	if err != nil || string(sealed) != "sealed-rotated" {
		t.Fatalf("rotated sealed credential = %q, %v", sealed, err)
	}
	rotations, err := management.ListCalls(ctx, connectorsmodule.ListCallsQuery{
		WorkspaceKey: "WS", BindingID: connectorsmodule.RotationAuditBindingID,
	})
	if err != nil || len(rotations) != 1 || rotations[0].Action != connectorsmodule.RotationAuditAction ||
		strings.Contains(rotations[0].SanitizedSummary, "replacement-credential") {
		t.Fatalf("rotation audit = %+v, %v", rotations, err)
	}

	grant, err := management.CreateGrant(ctx, connectorsmodule.CreateGrantCommand{
		WorkspaceKey: "WS", GrantID: "grant-review-read", ConnectorID: "github-main",
		BindingID: "review", Action: "github.pulls.list", ResourcePattern: "repo:acme/widgets",
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	for _, query := range []connectorsmodule.ListGrantsQuery{
		{WorkspaceKey: "WS", BindingID: "review"},
		{WorkspaceKey: "WS", ConnectorID: "github-main"},
	} {
		grants, listErr := management.ListGrants(ctx, query)
		if listErr != nil || len(grants) != 1 || grants[0].GrantID != grant.GrantID {
			t.Fatalf("list grants %+v = %+v, %v", query, grants, listErr)
		}
	}

	call := &domain.ConnectorCallRecord{
		WorkspaceKey: "WS", RunID: "run-1", BindingID: "review", ConnectorID: "github-main",
		SourceKind: domain.ConnectorSourceGitHub, Action: "github.pulls.list", Seq: 1,
		Decision: domain.ConnectorCallGranted, OccurredAt: now,
	}
	call.CallID = domain.ConnectorCallID(call.RunID, call.Action, call.Seq)
	if err := backend.ConnectorCalls().Append(ctx, call); err != nil {
		t.Fatalf("append audit: %v", err)
	}
	calls, err := management.ListCalls(ctx, connectorsmodule.ListCallsQuery{
		WorkspaceKey: "WS", RunID: "run-1",
		Filter: connectorsmodule.ConnectorCallFilter{Decision: connectorsmodule.ConnectorCallGranted},
	})
	if err != nil || len(calls) != 1 || calls[0].CallID != call.CallID {
		t.Fatalf("list calls = %+v, %v", calls, err)
	}

	if err := management.RevokeGrant(ctx, connectorsmodule.RevokeGrantCommand{
		WorkspaceKey: "WS", GrantID: grant.GrantID,
	}); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	grants, err := management.ListGrants(ctx, connectorsmodule.ListGrantsQuery{
		WorkspaceKey: "WS", BindingID: "review",
	})
	if err != nil || len(grants) != 0 {
		t.Fatalf("list after revoke = %+v, %v", grants, err)
	}
	if err := management.RevokeGrant(ctx, connectorsmodule.RevokeGrantCommand{
		WorkspaceKey: "WS", GrantID: grant.GrantID,
	}); !errors.Is(err, connectorsmodule.ErrGrantRevoked) || !errors.Is(err, domain.ErrGrantRevoked) {
		t.Fatalf("second revoke error = %v", err)
	}
}

func TestManagementMapsNotFoundAndRejectsAmbiguousQueries(t *testing.T) {
	t.Parallel()
	backend := memstore.New()
	adapter, err := connectorscatalog.New(
		backend.Connectors(), backend.ConnectorGrants(), backend.ConnectorCalls(),
	)
	if err != nil {
		t.Fatal(err)
	}
	management, err := connectorsmodule.NewManagement(adapter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := management.GetConnector(t.Context(), connectorsmodule.GetConnectorQuery{
		WorkspaceKey: "WS", ConnectorID: "missing",
	}); !errors.Is(err, connectorsmodule.ErrNotFound) {
		t.Fatalf("get missing error = %v", err)
	}
	if _, err := management.ListGrants(t.Context(), connectorsmodule.ListGrantsQuery{
		WorkspaceKey: "WS", BindingID: "binding", ConnectorID: "connector",
	}); !errors.Is(err, connectorsmodule.ErrInvalid) {
		t.Fatalf("ambiguous grant query error = %v", err)
	}
	if _, err := management.ListCalls(t.Context(), connectorsmodule.ListCallsQuery{
		WorkspaceKey: "WS",
	}); !errors.Is(err, connectorsmodule.ErrInvalid) {
		t.Fatalf("missing call selector error = %v", err)
	}
}
