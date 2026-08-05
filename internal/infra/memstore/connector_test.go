package memstore

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func connectorCreate(connectorID string) store.ConnectorCreate {
	return store.ConnectorCreate{
		WorkspaceKey:             "WS",
		ConnectorID:              connectorID,
		SourceKind:               domain.ConnectorSourceGitHub,
		DisplayName:              "GitHub main",
		InboundEndpointPath:      "/hooks/" + connectorID,
		InboundSecret:            "inbound-secret-1",
		OutboundCredentialSealed: []byte("sealed-ciphertext"),
		CreatedBy:                "tyson",
	}
}

func assertRedacted(t *testing.T, label string, conn *domain.Connector) {
	t.Helper()
	if conn.InboundSecret != "" {
		t.Errorf("%s: InboundSecret = %q, want empty", label, conn.InboundSecret)
	}
	if conn.PreviousInboundSecret != "" {
		t.Errorf("%s: PreviousInboundSecret = %q, want empty", label, conn.PreviousInboundSecret)
	}
	if conn.OutboundCredentialSealed != nil {
		t.Errorf("%s: OutboundCredentialSealed has %d bytes, want nil", label, len(conn.OutboundCredentialSealed))
	}
}

func TestConnectorCreate(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name    string
		mutate  func(*store.ConnectorCreate)
		wantErr error
	}{
		{name: "valid"},
		{
			name:    "missing connector id",
			mutate:  func(in *store.ConnectorCreate) { in.ConnectorID = "" },
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "unknown source kind",
			mutate:  func(in *store.ConnectorCreate) { in.SourceKind = "gitlab" },
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "unknown status",
			mutate:  func(in *store.ConnectorCreate) { in.Status = "paused" },
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "endpoint path without slash",
			mutate:  func(in *store.ConnectorCreate) { in.InboundEndpointPath = "hooks/gh" },
			wantErr: domain.ErrInvalid,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			in := connectorCreate("gh-main")
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			conn, err := s.Connectors().Create(ctx, in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Create error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if conn.Status != domain.ConnectorStatusActive {
				t.Errorf("default Status = %q, want active", conn.Status)
			}
			assertRedacted(t, "Create result", conn)
		})
	}
}

func TestConnectorCreateDuplicate(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.Connectors().Create(ctx, connectorCreate("gh-main"))
	if !errors.Is(err, domain.ErrConnectorExists) {
		t.Fatalf("duplicate create error = %v, want domain.ErrConnectorExists", err)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v, want to also match domain.ErrAlreadyExists", err)
	}

	// Multiple named connectors per source kind coexist under distinct ids.
	if _, err := s.Connectors().Create(ctx, connectorCreate("gh-staging")); err != nil {
		t.Fatalf("second named connector for same source kind: %v", err)
	}
}

func TestConnectorRedactionOnEveryPublicReadPath(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
		t.Fatalf("create: %v", err)
	}
	rotated, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
		NewInboundSecret: "inbound-secret-2",
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	assertRedacted(t, "RotateSecrets result", rotated)

	got, err := s.Connectors().Get(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertRedacted(t, "Get result", got)
	if got.PreviousSecretValidUntil == nil {
		t.Errorf("Get dropped PreviousSecretValidUntil; rotation window metadata should survive redaction")
	}

	listed, err := s.Connectors().List(ctx, "WS", store.ConnectorFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("list returned %d connectors, want 1", len(listed))
	}
	assertRedacted(t, "List result", listed[0])
}

func TestConnectorList(t *testing.T) {
	ctx := t.Context()
	s := New()

	slack := connectorCreate("slack-ops")
	slack.SourceKind = domain.ConnectorSourceSlack
	disabled := connectorCreate("gh-disabled")
	disabled.Status = domain.ConnectorStatusDisabled
	for _, in := range []store.ConnectorCreate{connectorCreate("gh-main"), slack, disabled} {
		if _, err := s.Connectors().Create(ctx, in); err != nil {
			t.Fatalf("create %s: %v", in.ConnectorID, err)
		}
	}

	tests := []struct {
		name    string
		filter  store.ConnectorFilter
		wantIDs []string
	}{
		{name: "all sorted by id", filter: store.ConnectorFilter{}, wantIDs: []string{"gh-disabled", "gh-main", "slack-ops"}},
		{name: "by source kind", filter: store.ConnectorFilter{SourceKind: domain.ConnectorSourceGitHub}, wantIDs: []string{"gh-disabled", "gh-main"}},
		{name: "by status", filter: store.ConnectorFilter{Status: domain.ConnectorStatusActive}, wantIDs: []string{"gh-main", "slack-ops"}},
		{name: "limit", filter: store.ConnectorFilter{Limit: 1}, wantIDs: []string{"gh-disabled"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listed, err := s.Connectors().List(ctx, "WS", tc.filter)
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

	if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
		t.Fatalf("create: %v", err)
	}

	secrets, err := s.Connectors().ResolveInboundSecret(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("resolve inbound: %v", err)
	}
	if secrets.Current != "inbound-secret-1" {
		t.Errorf("Current = %q, want inbound-secret-1", secrets.Current)
	}
	if secrets.Previous != "" || !secrets.PreviousValidUntil.IsZero() {
		t.Errorf("Previous = (%q, %v), want empty outside a rotation window", secrets.Previous, secrets.PreviousValidUntil)
	}

	sealed, err := s.Connectors().ResolveOutboundCredentialSealed(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("resolve outbound: %v", err)
	}
	if !bytes.Equal(sealed, []byte("sealed-ciphertext")) {
		t.Errorf("sealed credential = %q, want sealed-ciphertext", sealed)
	}

	for name, call := range map[string]func() error{
		"Get": func() error { _, err := s.Connectors().Get(ctx, "WS", "missing"); return err },
		"ResolveInboundSecret": func() error {
			_, err := s.Connectors().ResolveInboundSecret(ctx, "WS", "missing")
			return err
		},
		"ResolveOutboundCredentialSealed": func() error {
			_, err := s.Connectors().ResolveOutboundCredentialSealed(ctx, "WS", "missing")
			return err
		},
		"workspace isolation": func() error { _, err := s.Connectors().Get(ctx, "OTHER", "gh-main"); return err },
	} {
		if err := call(); !errors.Is(err, domain.ErrConnectorNotFound) {
			t.Errorf("%s missing connector error = %v, want domain.ErrConnectorNotFound", name, err)
		}
	}
}

func TestConnectorRotateSecrets(t *testing.T) {
	ctx := t.Context()

	t.Run("default overlap window", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		before := time.Now().UTC()
		if _, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret: "inbound-secret-2",
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		after := time.Now().UTC()

		secrets, err := s.Connectors().ResolveInboundSecret(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if secrets.Current != "inbound-secret-2" {
			t.Errorf("Current = %q, want inbound-secret-2", secrets.Current)
		}
		if secrets.Previous != "inbound-secret-1" {
			t.Errorf("Previous = %q, want demoted inbound-secret-1", secrets.Previous)
		}
		lo := before.Add(domain.DefaultConnectorSecretOverlap)
		hi := after.Add(domain.DefaultConnectorSecretOverlap)
		if secrets.PreviousValidUntil.Before(lo) || secrets.PreviousValidUntil.After(hi) {
			t.Errorf("PreviousValidUntil = %v, want within [%v, %v]", secrets.PreviousValidUntil, lo, hi)
		}
	})

	t.Run("expected generation is atomic", func(t *testing.T) {
		s := New()
		created, err := s.Connectors().Create(ctx, connectorCreate("gh-main"))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret:  "winner",
			ExpectedUpdatedAt: created.UpdatedAt,
		}); err != nil {
			t.Fatalf("winner rotate: %v", err)
		}
		if _, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret:  "stale",
			ExpectedUpdatedAt: created.UpdatedAt,
		}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("stale rotate = %v, want domain.ErrConflict", err)
		}
		secrets, err := s.Connectors().ResolveInboundSecret(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if secrets.Current != "winner" {
			t.Fatalf("stale rotation overwrote current secret: %q", secrets.Current)
		}
	})

	t.Run("window capped at max overlap", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		before := time.Now().UTC()
		if _, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret:         "inbound-secret-2",
			PreviousSecretValidUntil: before.Add(48 * time.Hour),
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		secrets, err := s.Connectors().ResolveInboundSecret(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		maxUntil := time.Now().UTC().Add(domain.MaxConnectorSecretOverlap)
		if secrets.PreviousValidUntil.After(maxUntil) {
			t.Errorf("PreviousValidUntil = %v, want capped at <= %v", secrets.PreviousValidUntil, maxUntil)
		}
	})

	t.Run("expired window hides previous secret", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret:         "inbound-secret-2",
			PreviousSecretValidUntil: time.Now().UTC().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		secrets, err := s.Connectors().ResolveInboundSecret(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if secrets.Previous != "" {
			t.Errorf("Previous = %q after window expiry, want empty", secrets.Previous)
		}
	})

	t.Run("outbound credential replacement and retention", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		// Nil leaves the sealed credential in place.
		if _, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret: "inbound-secret-2",
		}); err != nil {
			t.Fatalf("rotate keep: %v", err)
		}
		sealed, err := s.Connectors().ResolveOutboundCredentialSealed(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve after keep: %v", err)
		}
		if !bytes.Equal(sealed, []byte("sealed-ciphertext")) {
			t.Errorf("sealed after nil rotation = %q, want original retained", sealed)
		}
		// Non-nil replaces it (re-seal sweep path).
		if _, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{
			NewInboundSecret:            "inbound-secret-3",
			NewOutboundCredentialSealed: []byte("resealed-ciphertext"),
		}); err != nil {
			t.Fatalf("rotate replace: %v", err)
		}
		sealed, err = s.Connectors().ResolveOutboundCredentialSealed(ctx, "WS", "gh-main")
		if err != nil {
			t.Fatalf("resolve after replace: %v", err)
		}
		if !bytes.Equal(sealed, []byte("resealed-ciphertext")) {
			t.Errorf("sealed after rotation = %q, want resealed-ciphertext", sealed)
		}
	})

	t.Run("empty new secret rejected", func(t *testing.T) {
		s := New()
		if _, err := s.Connectors().Create(ctx, connectorCreate("gh-main")); err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err := s.Connectors().RotateSecrets(ctx, "WS", "gh-main", store.ConnectorSecretRotation{})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("rotate without secret error = %v, want domain.ErrInvalid", err)
		}
	})

	t.Run("missing connector", func(t *testing.T) {
		s := New()
		_, err := s.Connectors().RotateSecrets(ctx, "WS", "missing", store.ConnectorSecretRotation{
			NewInboundSecret: "inbound-secret-2",
		})
		if !errors.Is(err, domain.ErrConnectorNotFound) {
			t.Fatalf("rotate missing error = %v, want domain.ErrConnectorNotFound", err)
		}
	})
}

func grantCreate(grantID, bindingID string) store.ConnectorGrantCreate {
	return store.ConnectorGrantCreate{
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
		mutate  func(*store.ConnectorGrantCreate)
		wantErr error
	}{
		{name: "valid"},
		{
			name:    "missing binding id",
			mutate:  func(in *store.ConnectorGrantCreate) { in.BindingID = "" },
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "single segment action",
			mutate:  func(in *store.ConnectorGrantCreate) { in.Action = "merge" },
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "missing resource pattern",
			mutate:  func(in *store.ConnectorGrantCreate) { in.ResourcePattern = "" },
			wantErr: domain.ErrInvalid,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			in := grantCreate("grant-1", "binding-1")
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			grant, err := s.ConnectorGrants().Create(ctx, in)
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

	if _, err := s.ConnectorGrants().Create(ctx, grantCreate("grant-1", "binding-1")); err != nil {
		t.Fatalf("create grant-1: %v", err)
	}
	if _, err := s.ConnectorGrants().Create(ctx, grantCreate("grant-2", "binding-1")); err != nil {
		t.Fatalf("create grant-2: %v", err)
	}
	if _, err := s.ConnectorGrants().Create(ctx, grantCreate("grant-1", "binding-1")); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate grant error = %v, want domain.ErrAlreadyExists", err)
	}

	if err := s.ConnectorGrants().Revoke(ctx, "WS", "grant-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.ConnectorGrants().Revoke(ctx, "WS", "grant-1"); !errors.Is(err, domain.ErrGrantRevoked) {
		t.Fatalf("double revoke error = %v, want domain.ErrGrantRevoked", err)
	}
	if err := s.ConnectorGrants().Revoke(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoke missing error = %v, want domain.ErrNotFound", err)
	}

	byBinding, err := s.ConnectorGrants().ListByBinding(ctx, "WS", "binding-1")
	if err != nil {
		t.Fatalf("list by binding: %v", err)
	}
	if len(byBinding) != 1 || byBinding[0].GrantID != "grant-2" {
		t.Fatalf("ListByBinding after revoke = %+v, want only grant-2", byBinding)
	}

	byConnector, err := s.ConnectorGrants().ListByConnector(ctx, "WS", "gh-main")
	if err != nil {
		t.Fatalf("list by connector: %v", err)
	}
	if len(byConnector) != 1 || byConnector[0].GrantID != "grant-2" {
		t.Fatalf("ListByConnector after revoke = %+v, want only grant-2", byConnector)
	}

	// Deny-by-default: a binding with no grants resolves to an empty set, not
	// an error — the enforcement layer turns absence into ErrGrantDenied.
	none, err := s.ConnectorGrants().ListByBinding(ctx, "WS", "binding-without-grants")
	if err != nil {
		t.Fatalf("list unknown binding: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("unknown binding has %d grants, want 0", len(none))
	}
}

func connectorCallRecord(runID, action string, seq int) *domain.ConnectorCallRecord {
	return &domain.ConnectorCallRecord{
		WorkspaceKey: "WS",
		CallID:       domain.ConnectorCallID(runID, action, seq),
		Seq:          seq,
		RunID:        runID,
		BindingID:    "binding-1",
		ConnectorID:  "gh-main",
		SourceKind:   domain.ConnectorSourceGitHub,
		Action:       action,
		Resource:     "repo:octocat/hello",
		Decision:     domain.ConnectorCallGranted,
		OccurredAt:   time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
	}
}

func TestConnectorAuditAppend(t *testing.T) {
	ctx := t.Context()
	s := New()

	if err := s.ConnectorCalls().Append(ctx, connectorCallRecord("run-1", "github.merge", 1)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.ConnectorCalls().Append(ctx, connectorCallRecord("run-1", "github.merge", 1)); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate append error = %v, want domain.ErrAlreadyExists", err)
	}

	mismatched := connectorCallRecord("run-1", "github.merge", 2)
	mismatched.CallID = "run-1#github.merge#999"
	if err := s.ConnectorCalls().Append(ctx, mismatched); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("mismatched call id error = %v, want domain.ErrInvalid", err)
	}

	denied := connectorCallRecord("run-1", "slack.chat.post_message", 2)
	denied.Decision = domain.ConnectorCallDenied
	denied.OccurredAt = denied.OccurredAt.Add(time.Second)
	if err := s.ConnectorCalls().Append(ctx, denied); err != nil {
		t.Fatalf("append denied: %v", err)
	}
	otherRun := connectorCallRecord("run-2", "github.merge", 1)
	otherRun.BindingID = "binding-2"
	otherRun.OccurredAt = otherRun.OccurredAt.Add(2 * time.Second)
	if err := s.ConnectorCalls().Append(ctx, otherRun); err != nil {
		t.Fatalf("append other run: %v", err)
	}

	tests := []struct {
		name        string
		list        func() ([]*domain.ConnectorCallRecord, error)
		wantCallIDs []string
	}{
		{
			name: "by run",
			list: func() ([]*domain.ConnectorCallRecord, error) {
				return s.ConnectorCalls().ListByRun(ctx, "WS", "run-1", store.ConnectorCallFilter{})
			},
			wantCallIDs: []string{"run-1#github.merge#1", "run-1#slack.chat.post_message#2"},
		},
		{
			name: "by run with decision filter",
			list: func() ([]*domain.ConnectorCallRecord, error) {
				return s.ConnectorCalls().ListByRun(ctx, "WS", "run-1", store.ConnectorCallFilter{Decision: domain.ConnectorCallDenied})
			},
			wantCallIDs: []string{"run-1#slack.chat.post_message#2"},
		},
		{
			name: "by run with limit",
			list: func() ([]*domain.ConnectorCallRecord, error) {
				return s.ConnectorCalls().ListByRun(ctx, "WS", "run-1", store.ConnectorCallFilter{Limit: 1})
			},
			wantCallIDs: []string{"run-1#github.merge#1"},
		},
		{
			name: "by binding",
			list: func() ([]*domain.ConnectorCallRecord, error) {
				return s.ConnectorCalls().ListByBinding(ctx, "WS", "binding-2", store.ConnectorCallFilter{})
			},
			wantCallIDs: []string{"run-2#github.merge#1"},
		},
		{
			name: "workspace isolation",
			list: func() ([]*domain.ConnectorCallRecord, error) {
				return s.ConnectorCalls().ListByRun(ctx, "OTHER", "run-1", store.ConnectorCallFilter{})
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
			if err := s.ConnectorCalls().Append(ctx, connectorCallRecord("run-1", "github.merge", seq)); err != nil {
				errs <- fmt.Errorf("seq %d: %w", seq, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent append: %v", err)
	}

	records, err := s.ConnectorCalls().ListByRun(ctx, "WS", "run-1", store.ConnectorCallFilter{})
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
