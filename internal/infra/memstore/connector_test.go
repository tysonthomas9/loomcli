package memstore

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

func connectorCreate(connectorID string) connectorsmodule.CreateConnectorMutation {
	return connectorsmodule.CreateConnectorMutation{
		WorkspaceKey:             "WS",
		ConnectorID:              connectorID,
		SourceKind:               connectorsmodule.ConnectorSourceGitHub,
		DisplayName:              "GitHub main",
		InboundEndpointPath:      "/hooks/" + connectorID,
		InboundSecret:            "inbound-secret-1",
		OutboundCredentialSealed: []byte("sealed-ciphertext"),
		CreatedBy:                "tyson",
	}
}

func TestConnectorCreate(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name    string
		mutate  func(*connectorsmodule.CreateConnectorMutation)
		wantErr error
	}{
		{name: "valid"},
		{
			name:    "missing connector id",
			mutate:  func(in *connectorsmodule.CreateConnectorMutation) { in.ConnectorID = "" },
			wantErr: connectorsmodule.ErrInvalid,
		},
		{
			name:    "unknown source kind",
			mutate:  func(in *connectorsmodule.CreateConnectorMutation) { in.SourceKind = "gitlab" },
			wantErr: connectorsmodule.ErrInvalid,
		},
		{
			name:    "unknown status",
			mutate:  func(in *connectorsmodule.CreateConnectorMutation) { in.Status = "paused" },
			wantErr: connectorsmodule.ErrInvalid,
		},
		{
			name:    "endpoint path without slash",
			mutate:  func(in *connectorsmodule.CreateConnectorMutation) { in.InboundEndpointPath = "hooks/gh" },
			wantErr: connectorsmodule.ErrInvalid,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			in := connectorCreate("gh-main")
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			conn, err := s.Connectors().CreateConnectorRecord(ctx, in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Create error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if conn.Status != connectorsmodule.ConnectorStatusActive {
				t.Errorf("default Status = %q, want active", conn.Status)
			}
		})
	}
}

func TestConnectorCreateDuplicate(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main"))
	if !errors.Is(err, connectorsmodule.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v, want connectorsmodule.ErrAlreadyExists", err)
	}

	// Multiple named connectors per source kind coexist under distinct ids.
	if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-staging")); err != nil {
		t.Fatalf("second named connector for same source kind: %v", err)
	}
}

func TestConnectorBindingSecretLifecycle(t *testing.T) {
	ctx := t.Context()
	s := newRouterTestStore(t)
	if _, err := s.TriggerBindings().Create(ctx, routerBindingCreate("binding-1")); err != nil {
		t.Fatalf("create trigger binding: %v", err)
	}
	management, err := connectorsmodule.NewManagement(s.Connectors())
	if err != nil {
		t.Fatalf("new connector management: %v", err)
	}
	if err := management.ConfigureBindingSecret(ctx, connectorsmodule.ConfigureBindingSecretCommand{
		WorkspaceKey: "WS",
		BindingID:    "binding-1",
		Secret:       "binding-secret",
	}); err != nil {
		t.Fatalf("configure binding secret: %v", err)
	}
	secret, err := s.TriggerBindings().ResolveWebhookSecret(ctx, "WS", "binding-1")
	if err != nil {
		t.Fatalf("resolve binding secret: %v", err)
	}
	if secret != "binding-secret" {
		t.Fatalf("resolved binding secret = %q, want binding-secret", secret)
	}
	publicBinding, err := s.TriggerBindings().Get(ctx, "WS", "binding-1")
	if err != nil {
		t.Fatalf("get public binding: %v", err)
	}
	if publicBinding.WebhookSecret != "" {
		t.Fatal("public binding leaked its configured secret")
	}
	if err := management.ConfigureBindingSecret(ctx, connectorsmodule.ConfigureBindingSecretCommand{
		WorkspaceKey: "WS",
		BindingID:    "missing",
		Secret:       "binding-secret",
	}); !errors.Is(err, connectorsmodule.ErrNotFound) {
		t.Fatalf("missing binding error = %v, want %v", err, connectorsmodule.ErrNotFound)
	}
}

func TestConnectorRedactionOnEveryPublicReadPath(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
		t.Fatalf("create: %v", err)
	}
	rotated, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
		NewInboundSecret: "inbound-secret-2",
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated.PreviousSecretValidUntil == nil {
		t.Errorf("Rotate dropped PreviousSecretValidUntil; rotation metadata must stay public")
	}

	got, err := s.Connectors().GetConnectorRecord(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PreviousSecretValidUntil == nil {
		t.Errorf("Get dropped PreviousSecretValidUntil; rotation window metadata should survive redaction")
	}

	listed, err := s.Connectors().ListConnectorRecords(ctx, "WS", connectorsmodule.ConnectorFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("list returned %d connectors, want 1", len(listed))
	}
}

func TestConnectorList(t *testing.T) {
	ctx := t.Context()
	s := New()

	slack := connectorCreate("slack-ops")
	slack.SourceKind = connectorsmodule.ConnectorSourceSlack
	disabled := connectorCreate("gh-disabled")
	disabled.Status = connectorsmodule.ConnectorStatusDisabled
	for _, in := range []connectorsmodule.CreateConnectorMutation{connectorCreate("gh-main"), slack, disabled} {
		if _, err := s.Connectors().CreateConnectorRecord(ctx, in); err != nil {
			t.Fatalf("create %s: %v", in.ConnectorID, err)
		}
	}

	tests := []struct {
		name    string
		filter  connectorsmodule.ConnectorFilter
		wantIDs []string
	}{
		{name: "all sorted by id", filter: connectorsmodule.ConnectorFilter{}, wantIDs: []string{"gh-disabled", "gh-main", "slack-ops"}},
		{name: "by source kind", filter: connectorsmodule.ConnectorFilter{SourceKind: connectorsmodule.ConnectorSourceGitHub}, wantIDs: []string{"gh-disabled", "gh-main"}},
		{name: "by status", filter: connectorsmodule.ConnectorFilter{Status: connectorsmodule.ConnectorStatusActive}, wantIDs: []string{"gh-main", "slack-ops"}},
		{name: "limit", filter: connectorsmodule.ConnectorFilter{Limit: 1}, wantIDs: []string{"gh-disabled"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listed, err := s.Connectors().ListConnectorRecords(ctx, "WS", tc.filter)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(listed) != len(tc.wantIDs) {
				t.Fatalf("got %d connectors, want %d", len(listed), len(tc.wantIDs))
			}
			for i, conn := range listed {
				if conn.ConnectorID != tc.wantIDs[i] {
					t.Errorf("listed[%d].ConnectorID = %q, want %q", i, conn.ConnectorID, tc.wantIDs[i])
				}
			}
		})
	}
}

func TestConnectorPrivilegedResolveRoundTrip(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
		t.Fatalf("create: %v", err)
	}

	secrets, err := s.Connectors().ResolveInboundSecretsRecord(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("resolve inbound: %v", err)
	}
	if secrets.Current != "inbound-secret-1" {
		t.Errorf("Current = %q, want inbound-secret-1", secrets.Current)
	}
	if secrets.Previous != "" || !secrets.PreviousValidUntil.IsZero() {
		t.Errorf("Previous = (%q, %v), want empty outside a rotation window", secrets.Previous, secrets.PreviousValidUntil)
	}

	sealed, err := s.Connectors().ResolveOutboundCredentialSealedRecord(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("resolve outbound: %v", err)
	}
	if !bytes.Equal(sealed, []byte("sealed-ciphertext")) {
		t.Errorf("sealed credential = %q, want sealed-ciphertext", sealed)
	}

	for name, call := range map[string]func() error{
		"Get": func() error { _, err := s.Connectors().GetConnectorRecord(ctx, "WS", "missing"); return err },
		"ResolveInboundSecret": func() error {
			_, err := s.Connectors().ResolveInboundSecretsRecord(ctx, "WS", "missing")
			return err
		},
		"ResolveOutboundCredentialSealed": func() error {
			_, err := s.Connectors().ResolveOutboundCredentialSealedRecord(ctx, "WS", "missing")
			return err
		},
		"workspace isolation": func() error { _, err := s.Connectors().GetConnectorRecord(ctx, "OTHER", "gh-main"); return err },
	} {
		if err := call(); !errors.Is(err, connectorsmodule.ErrNotFound) {
			t.Errorf("%s missing connector error = %v, want connectorsmodule.ErrNotFound", name, err)
		}
	}
}

func TestConnectorRotateSecrets(t *testing.T) {
	ctx := t.Context()

	t.Run("default overlap window", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		before := time.Now().UTC()
		if _, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret: "inbound-secret-2",
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		after := time.Now().UTC()

		secrets, err := s.Connectors().ResolveInboundSecretsRecord(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if secrets.Current != "inbound-secret-2" {
			t.Errorf("Current = %q, want inbound-secret-2", secrets.Current)
		}
		if secrets.Previous != "inbound-secret-1" {
			t.Errorf("Previous = %q, want demoted inbound-secret-1", secrets.Previous)
		}
		lo := before.Add(connectorsmodule.DefaultConnectorSecretOverlap)
		hi := after.Add(connectorsmodule.DefaultConnectorSecretOverlap)
		if secrets.PreviousValidUntil.Before(lo) || secrets.PreviousValidUntil.After(hi) {
			t.Errorf("PreviousValidUntil = %v, want within [%v, %v]", secrets.PreviousValidUntil, lo, hi)
		}
	})

	t.Run("expected generation is atomic", func(t *testing.T) {
		s := New()
		created, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main"))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret:  "winner",
			ExpectedUpdatedAt: created.UpdatedAt,
		}); err != nil {
			t.Fatalf("winner rotate: %v", err)
		}
		if _, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret:  "stale",
			ExpectedUpdatedAt: created.UpdatedAt,
		}); !errors.Is(err, connectorsmodule.ErrRotationConflict) {
			t.Fatalf("stale rotate = %v, want connectorsmodule.ErrRotationConflict", err)
		}
		secrets, err := s.Connectors().ResolveInboundSecretsRecord(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if secrets.Current != "winner" {
			t.Fatalf("stale rotation overwrote current secret: %q", secrets.Current)
		}
	})

	t.Run("window capped at max overlap", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		before := time.Now().UTC()
		if _, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret:         "inbound-secret-2",
			PreviousSecretValidUntil: before.Add(48 * time.Hour),
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		secrets, err := s.Connectors().ResolveInboundSecretsRecord(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		maxUntil := time.Now().UTC().Add(connectorsmodule.MaxConnectorSecretOverlap)
		if secrets.PreviousValidUntil.After(maxUntil) {
			t.Errorf("PreviousValidUntil = %v, want capped at <= %v", secrets.PreviousValidUntil, maxUntil)
		}
	})

	t.Run("expired window hides previous secret", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret:         "inbound-secret-2",
			PreviousSecretValidUntil: time.Now().UTC().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		secrets, err := s.Connectors().ResolveInboundSecretsRecord(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if secrets.Previous != "" {
			t.Errorf("Previous = %q after window expiry, want empty", secrets.Previous)
		}
	})

	t.Run("outbound credential replacement and retention", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		// Nil leaves the sealed credential in place.
		if _, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret: "inbound-secret-2",
		}); err != nil {
			t.Fatalf("rotate keep: %v", err)
		}
		sealed, err := s.Connectors().ResolveOutboundCredentialSealedRecord(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve after keep: %v", err)
		}
		if !bytes.Equal(sealed, []byte("sealed-ciphertext")) {
			t.Errorf("sealed after nil rotation = %q, want original retained", sealed)
		}
		// Non-nil replaces it (re-seal sweep path).
		if _, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret:            "inbound-secret-3",
			NewOutboundCredentialSealed: []byte("resealed-ciphertext"),
		}); err != nil {
			t.Fatalf("rotate replace: %v", err)
		}
		sealed, err = s.Connectors().ResolveOutboundCredentialSealedRecord(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve after replace: %v", err)
		}
		if !bytes.Equal(sealed, []byte("resealed-ciphertext")) {
			t.Errorf("sealed after rotation = %q, want resealed-ciphertext", sealed)
		}
	})

	t.Run("empty new secret rejected", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().CreateConnectorRecord(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "gh-main", connectorsmodule.RotateConnectorSecretsMutation{})
		if !errors.Is(err, connectorsmodule.ErrInvalid) {
			t.Fatalf("rotate without secret error = %v, want connectorsmodule.ErrInvalid", err)
		}
	})

	t.Run("missing connector", func(t *testing.T) {
		s := New()
		_, err := s.Connectors().RotateConnectorSecretsRecord(ctx, "WS", "missing", connectorsmodule.RotateConnectorSecretsMutation{
			NewInboundSecret: "inbound-secret-2",
		})
		if !errors.Is(err, connectorsmodule.ErrNotFound) {
			t.Fatalf("rotate missing error = %v, want connectorsmodule.ErrNotFound", err)
		}
	})
}

func grantCreate(grantID, bindingID string) connectorsmodule.CreateGrantMutation {
	return connectorsmodule.CreateGrantMutation{
		WorkspaceKey:    "WS",
		GrantID:         grantID,
		ConnectorID:     "gh-main",
		BindingID:       bindingID,
		Action:          "github.merge",
		ResourcePattern: "repo:octocat/hello",
	}
}

func TestConnectorGrantCreate(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name    string
		mutate  func(*connectorsmodule.CreateGrantMutation)
		wantErr error
	}{
		{name: "valid"},
		{
			name:    "missing binding id",
			mutate:  func(in *connectorsmodule.CreateGrantMutation) { in.BindingID = "" },
			wantErr: connectorsmodule.ErrInvalid,
		},
		{
			name:    "single segment action",
			mutate:  func(in *connectorsmodule.CreateGrantMutation) { in.Action = "merge" },
			wantErr: connectorsmodule.ErrInvalid,
		},
		{
			name:    "missing resource pattern",
			mutate:  func(in *connectorsmodule.CreateGrantMutation) { in.ResourcePattern = "" },
			wantErr: connectorsmodule.ErrInvalid,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			in := grantCreate("grant-1", "binding-1")
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			grant, err := s.Connectors().CreateManagementGrant(ctx, in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Create error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if grant.Revoked() {
				t.Errorf("new grant is revoked")
			}
		})
	}
}

func TestConnectorGrantRevokeFiltering(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Connectors().CreateManagementGrant(ctx, grantCreate("grant-1", "binding-1")); err != nil {
		t.Fatalf("create grant-1: %v", err)
	}
	if _, err := s.Connectors().CreateManagementGrant(ctx, grantCreate("grant-2", "binding-1")); err != nil {
		t.Fatalf("create grant-2: %v", err)
	}
	if _, err := s.Connectors().CreateManagementGrant(ctx, grantCreate("grant-1", "binding-1")); !errors.Is(err, connectorsmodule.ErrAlreadyExists) {
		t.Fatalf("duplicate grant error = %v, want connectorsmodule.ErrAlreadyExists", err)
	}

	if err := s.Connectors().RevokeGrantRecord(ctx, "WS", "grant-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.Connectors().RevokeGrantRecord(ctx, "WS", "grant-1"); !errors.Is(err, connectorsmodule.ErrGrantRevoked) {
		t.Fatalf("double revoke error = %v, want connectorsmodule.ErrGrantRevoked", err)
	}
	if err := s.Connectors().RevokeGrantRecord(ctx, "WS", "missing"); !errors.Is(err, connectorsmodule.ErrNotFound) {
		t.Fatalf("revoke missing error = %v, want connectorsmodule.ErrNotFound", err)
	}

	byBinding, err := s.Connectors().ListGrantRecordsByBinding(ctx, "WS", "binding-1")
	if err != nil {
		t.Fatalf("list by binding: %v", err)
	}
	if len(byBinding) != 1 || byBinding[0].GrantID != "grant-2" {
		t.Fatalf("ListByBinding after revoke = %+v, want only grant-2", byBinding)
	}

	byConnector, err := s.Connectors().ListGrantRecordsByConnector(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("list by connector: %v", err)
	}
	if len(byConnector) != 1 || byConnector[0].GrantID != "grant-2" {
		t.Fatalf("ListByConnector after revoke = %+v, want only grant-2", byConnector)
	}

	// Deny-by-default: a binding with no grants resolves to an empty set, not
	// an error — the enforcement layer turns absence into ErrGrantDenied.
	none, err := s.Connectors().ListGrantRecordsByBinding(ctx, "WS", "binding-without-grants")
	if err != nil {
		t.Fatalf("list unknown binding: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("unknown binding has %d grants, want 0", len(none))
	}
}

func connectorCallRecord(runID, action string, seq int) *connectorsmodule.ConnectorCallRecord {
	return &connectorsmodule.ConnectorCallRecord{
		WorkspaceKey: "WS",
		CallID:       connectorsmodule.ConnectorCallID(runID, action, seq),
		Seq:          seq,
		RunID:        runID,
		BindingID:    "binding-1",
		ConnectorID:  "gh-main",
		SourceKind:   connectorsmodule.ConnectorSourceGitHub,
		Action:       action,
		Resource:     "repo:octocat/hello",
		Decision:     connectorsmodule.ConnectorCallGranted,
		OccurredAt:   time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
	}
}

func TestConnectorAuditAppend(t *testing.T) {
	ctx := t.Context()
	s := New()

	if err := s.Connectors().AppendConnectorCallRecord(ctx, connectorCallRecord("run-1", "github.merge", 1)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Connectors().AppendConnectorCallRecord(ctx, connectorCallRecord("run-1", "github.merge", 1)); !errors.Is(err, connectorsmodule.ErrAlreadyExists) {
		t.Fatalf("duplicate append error = %v, want connectorsmodule.ErrAlreadyExists", err)
	}

	mismatched := connectorCallRecord("run-1", "github.merge", 2)
	mismatched.CallID = "run-1#github.merge#999"
	if err := s.Connectors().AppendConnectorCallRecord(ctx, mismatched); !errors.Is(err, connectorsmodule.ErrInvalid) {
		t.Fatalf("mismatched call id error = %v, want connectorsmodule.ErrInvalid", err)
	}

	denied := connectorCallRecord("run-1", "slack.chat.post_message", 2)
	denied.Decision = connectorsmodule.ConnectorCallDenied
	denied.OccurredAt = denied.OccurredAt.Add(time.Second)
	if err := s.Connectors().AppendConnectorCallRecord(ctx, denied); err != nil {
		t.Fatalf("append denied: %v", err)
	}
	otherRun := connectorCallRecord("run-2", "github.merge", 1)
	otherRun.BindingID = "binding-2"
	otherRun.OccurredAt = otherRun.OccurredAt.Add(2 * time.Second)
	if err := s.Connectors().AppendConnectorCallRecord(ctx, otherRun); err != nil {
		t.Fatalf("append other run: %v", err)
	}

	tests := []struct {
		name        string
		list        func() ([]*connectorsmodule.ConnectorCallRecord, error)
		wantCallIDs []string
	}{
		{
			name: "by run",
			list: func() ([]*connectorsmodule.ConnectorCallRecord, error) {
				return s.Connectors().ListCallRecordsByRun(ctx, "WS", "run-1", connectorsmodule.ConnectorCallFilter{})
			},
			wantCallIDs: []string{"run-1#github.merge#1", "run-1#slack.chat.post_message#2"},
		},
		{
			name: "by run with decision filter",
			list: func() ([]*connectorsmodule.ConnectorCallRecord, error) {
				return s.Connectors().ListCallRecordsByRun(ctx, "WS", "run-1", connectorsmodule.ConnectorCallFilter{Decision: connectorsmodule.ConnectorCallDenied})
			},
			wantCallIDs: []string{"run-1#slack.chat.post_message#2"},
		},
		{
			name: "by run with limit",
			list: func() ([]*connectorsmodule.ConnectorCallRecord, error) {
				return s.Connectors().ListCallRecordsByRun(ctx, "WS", "run-1", connectorsmodule.ConnectorCallFilter{Limit: 1})
			},
			wantCallIDs: []string{"run-1#github.merge#1"},
		},
		{
			name: "by binding",
			list: func() ([]*connectorsmodule.ConnectorCallRecord, error) {
				return s.Connectors().ListCallRecordsByBinding(ctx, "WS", "binding-2", connectorsmodule.ConnectorCallFilter{})
			},
			wantCallIDs: []string{"run-2#github.merge#1"},
		},
		{
			name: "workspace isolation",
			list: func() ([]*connectorsmodule.ConnectorCallRecord, error) {
				return s.Connectors().ListCallRecordsByRun(ctx, "OTHER", "run-1", connectorsmodule.ConnectorCallFilter{})
			},
			wantCallIDs: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			records, err := tc.list()
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(records) != len(tc.wantCallIDs) {
				t.Fatalf("got %d records, want %d", len(records), len(tc.wantCallIDs))
			}
			for i, rec := range records {
				if rec.CallID != tc.wantCallIDs[i] {
					t.Errorf("records[%d].CallID = %q, want %q", i, rec.CallID, tc.wantCallIDs[i])
				}
			}
		})
	}
}

func TestConnectorAuditConcurrentAppendSeqMonotonic(t *testing.T) {
	ctx := t.Context()
	s := New()
	const n = 64

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			if err := s.Connectors().AppendConnectorCallRecord(ctx, connectorCallRecord("run-1", "github.merge", seq)); err != nil {
				errs <- fmt.Errorf("seq %d: %w", seq, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent append: %v", err)
	}

	records, err := s.Connectors().ListCallRecordsByRun(ctx, "WS", "run-1", connectorsmodule.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != n {
		t.Fatalf("journal has %d records after %d concurrent appends, want %d", len(records), n, n)
	}
	for i, rec := range records {
		if rec.Seq != i+1 {
			t.Fatalf("records[%d].Seq = %d, want monotonic %d", i, rec.Seq, i+1)
		}
	}
}
