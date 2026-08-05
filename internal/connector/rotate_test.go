package connector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	rotateWS         = "ws-rot"
	rotateConn       = "gh-rot"
	rotateOldSecret  = "whsec-OLD-inbound-1"
	rotateNewSecret  = "whsec-NEW-inbound-2"
	rotateOldCred    = "ghp_OLD_outbound_token"
	rotateNewCred    = "ghp_NEW_outbound_token"
	rotateVaultBytes = "rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr" // 32 bytes
)

// newRotateHarness seeds memstore with one active github connector holding an
// inbound secret and a sealed outbound credential, and returns the vault that
// sealed it.
func newRotateHarness(t *testing.T) (*memstore.Store, *Vault) {
	t.Helper()
	ms := memstore.New()
	vault, err := NewVault([]byte(rotateVaultBytes))
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	sealed, err := vault.Seal([]byte(rotateOldCred), CredentialAAD(rotateWS, rotateConn))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := ms.Connectors().Create(context.Background(), store.ConnectorCreate{
		WorkspaceKey:             rotateWS,
		ConnectorID:              rotateConn,
		SourceKind:               domain.ConnectorSourceGitHub,
		InboundSecret:            rotateOldSecret,
		OutboundCredentialSealed: sealed,
	}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	return ms, vault
}

func baseRotateRequest() RotateRequest {
	return RotateRequest{
		WorkspaceKey:     rotateWS,
		ConnectorID:      rotateConn,
		NewInboundSecret: rotateNewSecret,
	}
}

func TestRotateValidation(t *testing.T) {
	ms, vault := newRotateHarness(t)
	tests := []struct {
		name    string
		mutate  func(*RotateRequest)
		sealer  Sealer
		wantErr error
	}{
		{
			name:    "missing workspace",
			mutate:  func(r *RotateRequest) { r.WorkspaceKey = "" },
			sealer:  vault,
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "missing connector id",
			mutate:  func(r *RotateRequest) { r.ConnectorID = "" },
			sealer:  vault,
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "missing new inbound secret",
			mutate:  func(r *RotateRequest) { r.NewInboundSecret = "" },
			sealer:  vault,
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "negative window",
			mutate:  func(r *RotateRequest) { r.InboundWindow = -time.Minute },
			sealer:  vault,
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "new credential without sealer",
			mutate:  func(r *RotateRequest) { r.NewCredential = []byte(rotateNewCred) },
			sealer:  nil,
			wantErr: ErrRotationSealerMissing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := baseRotateRequest()
			req.NewCredential = []byte(rotateNewCred)
			tt.mutate(&req)
			if _, err := Rotate(context.Background(), ms.Connectors(), ms.ConnectorCalls(), tt.sealer, req); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Rotate = %v, want %v", err, tt.wantErr)
			}
			if !bytes.Equal(req.NewCredential, make([]byte, len(req.NewCredential))) {
				t.Fatalf("plaintext credential buffer not wiped after refused rotation: %q", req.NewCredential)
			}
			// No refused request may have touched the store.
			secrets, err := ms.Connectors().ResolveInboundSecret(context.Background(), rotateWS, rotateConn)
			if err != nil {
				t.Fatalf("ResolveInboundSecret: %v", err)
			}
			if secrets.Current != rotateOldSecret {
				t.Fatalf("inbound secret = %q after refused rotation, want untouched", secrets.Current)
			}
		})
	}
}

func TestRotateWindowArithmetic(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name   string
		window time.Duration
		want   time.Duration
	}{
		{name: "zero applies 15m default", window: 0, want: domain.DefaultConnectorSecretOverlap},
		{name: "explicit window honored", window: 30 * time.Minute, want: 30 * time.Minute},
		{name: "exact cap allowed", window: domain.MaxConnectorSecretOverlap, want: domain.MaxConnectorSecretOverlap},
		{name: "above cap clamped to 24h", window: 48 * time.Hour, want: domain.MaxConnectorSecretOverlap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, _ := newRotateHarness(t)
			req := baseRotateRequest()
			req.InboundWindow = tt.window
			req.Now = func() time.Time { return now }
			rotated, err := Rotate(context.Background(), ms.Connectors(), ms.ConnectorCalls(), nil, req)
			if err != nil {
				t.Fatalf("Rotate: %v", err)
			}
			if rotated.PreviousSecretValidUntil == nil {
				t.Fatal("PreviousSecretValidUntil = nil, want now+window")
			}
			if want := now.Add(tt.want); !rotated.PreviousSecretValidUntil.Equal(want) {
				t.Fatalf("PreviousSecretValidUntil = %v, want %v", rotated.PreviousSecretValidUntil, want)
			}
			// The dual-secret pair is live: new current, old previous.
			secrets, err := ms.Connectors().ResolveInboundSecret(context.Background(), rotateWS, rotateConn)
			if err != nil {
				t.Fatalf("ResolveInboundSecret: %v", err)
			}
			if secrets.Current != rotateNewSecret || secrets.Previous != rotateOldSecret {
				t.Fatalf("secrets = %+v, want current=new previous=old", secrets)
			}
		})
	}
}

func TestRotateRedactsResult(t *testing.T) {
	ms, vault := newRotateHarness(t)
	req := baseRotateRequest()
	req.NewCredential = []byte(rotateNewCred)
	rotated, err := Rotate(context.Background(), ms.Connectors(), ms.ConnectorCalls(), vault, req)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.InboundSecret != "" || rotated.PreviousInboundSecret != "" || rotated.OutboundCredentialSealed != nil {
		t.Fatalf("rotated connector not redacted: %+v", rotated)
	}
	if rotated.RotatedAt == nil || rotated.RotatedAt.IsZero() {
		t.Fatal("RotatedAt not set on rotated connector")
	}
}

func TestRotateNotFound(t *testing.T) {
	ms, vault := newRotateHarness(t)
	req := baseRotateRequest()
	req.ConnectorID = "nope"
	req.NewCredential = []byte(rotateNewCred)
	if _, err := Rotate(context.Background(), ms.Connectors(), ms.ConnectorCalls(), vault, req); !errors.Is(err, domain.ErrConnectorNotFound) {
		t.Fatalf("Rotate = %v, want ErrConnectorNotFound", err)
	}
	if !bytes.Equal(req.NewCredential, make([]byte, len(req.NewCredential))) {
		t.Fatalf("plaintext credential buffer not wiped after missing connector: %q", req.NewCredential)
	}
}

// TestRotateConcurrentFencing pins the optimistic fence: two writers act on
// the same read; the first rotation lands, the second is rejected with
// ErrRotationConflict (wrapping domain.ErrConflict) and writes nothing.
func TestRotateConcurrentFencing(t *testing.T) {
	ms, vault := newRotateHarness(t)
	ctx := context.Background()
	read, err := ms.Connectors().Get(ctx, rotateWS, rotateConn)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// memstore stamps UpdatedAt with time.Now, whose wall component has
	// microsecond resolution on darwin: spin until the clock has provably
	// advanced past the creation stamp so the first rotation writes a
	// distinguishable UpdatedAt for the fence to trip on.
	for !time.Now().UTC().After(read.UpdatedAt) {
	}

	first := baseRotateRequest()
	first.ExpectedUpdatedAt = read.UpdatedAt
	if _, err := Rotate(ctx, ms.Connectors(), ms.ConnectorCalls(), nil, first); err != nil {
		t.Fatalf("first Rotate: %v", err)
	}

	second := baseRotateRequest()
	second.NewInboundSecret = "whsec-SECOND-writer-3"
	second.ExpectedUpdatedAt = read.UpdatedAt
	second.NewCredential = []byte(rotateNewCred)
	_, err = Rotate(ctx, ms.Connectors(), ms.ConnectorCalls(), vault, second)
	if !errors.Is(err, ErrRotationConflict) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second Rotate = %v, want ErrRotationConflict wrapping domain.ErrConflict", err)
	}
	if !bytes.Equal(second.NewCredential, make([]byte, len(second.NewCredential))) {
		t.Fatalf("plaintext credential buffer not wiped after preflight conflict: %q", second.NewCredential)
	}

	secrets, err := ms.Connectors().ResolveInboundSecret(ctx, rotateWS, rotateConn)
	if err != nil {
		t.Fatalf("ResolveInboundSecret: %v", err)
	}
	if secrets.Current != rotateNewSecret || secrets.Previous != rotateOldSecret {
		t.Fatalf("secrets after rejected second writer = %+v, want first writer's state intact", secrets)
	}
	// Only the winning rotation was journaled.
	recs, err := ms.ConnectorCalls().ListByBinding(ctx, rotateWS, RotationAuditBindingID, store.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("rotation audit records = %d, want 1", len(recs))
	}
}

type conflictingConnectorStore struct {
	store.ConnectorStore
}

func (s *conflictingConnectorStore) RotateSecrets(
	context.Context,
	string,
	string,
	store.ConnectorSecretRotation,
) (*domain.Connector, error) {
	return nil, fmt.Errorf("injected authoritative CAS: %w", domain.ErrConflict)
}

func TestRotateAuthoritativeStoreConflictPreservesSentinelsAndWipesCredential(t *testing.T) {
	ms, vault := newRotateHarness(t)
	read, err := ms.Connectors().Get(context.Background(), rotateWS, rotateConn)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	req := baseRotateRequest()
	req.ExpectedUpdatedAt = read.UpdatedAt
	req.NewCredential = []byte(rotateNewCred)
	_, err = Rotate(
		context.Background(),
		&conflictingConnectorStore{ConnectorStore: ms.Connectors()},
		ms.ConnectorCalls(),
		vault,
		req,
	)
	if !errors.Is(err, ErrRotationConflict) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Rotate = %v, want authoritative ErrRotationConflict wrapping domain.ErrConflict", err)
	}
	if !bytes.Equal(req.NewCredential, make([]byte, len(req.NewCredential))) {
		t.Fatalf("plaintext credential buffer not wiped after authoritative conflict: %q", req.NewCredential)
	}
}

// TestRotatePreviousSecretExpiryBoundary pins the window edge: a previous
// secret resolves inside its validity window and disappears from the resolve
// path once the window has passed.
func TestRotatePreviousSecretExpiryBoundary(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name         string
		now          time.Time // injected rotation clock
		wantPrevious string
	}{
		{
			name:         "inside window previous still resolves",
			now:          time.Now().UTC(),
			wantPrevious: rotateOldSecret,
		},
		{
			name: "past window previous no longer resolves",
			// Rotation happened 2h ago with a 15m window, so the window
			// expired ~1h45m before the resolve below.
			now:          time.Now().UTC().Add(-2 * time.Hour),
			wantPrevious: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, _ := newRotateHarness(t)
			req := baseRotateRequest()
			req.InboundWindow = domain.DefaultConnectorSecretOverlap
			req.Now = func() time.Time { return tt.now }
			if _, err := Rotate(ctx, ms.Connectors(), ms.ConnectorCalls(), nil, req); err != nil {
				t.Fatalf("Rotate: %v", err)
			}
			secrets, err := ms.Connectors().ResolveInboundSecret(ctx, rotateWS, rotateConn)
			if err != nil {
				t.Fatalf("ResolveInboundSecret: %v", err)
			}
			if secrets.Current != rotateNewSecret {
				t.Fatalf("Current = %q, want new secret", secrets.Current)
			}
			if secrets.Previous != tt.wantPrevious {
				t.Fatalf("Previous = %q, want %q", secrets.Previous, tt.wantPrevious)
			}
		})
	}
}

// TestRotateDispatchUnsealsNewCredential is the outbound leg end to end: after
// a rotation that re-seals the credential, the very next Dispatch unseals and
// presents the NEW token (dispatch resolves fresh per call — no grace window
// for the old credential).
func TestRotateDispatchUnsealsNewCredential(t *testing.T) {
	ctx := context.Background()
	h := newDispatchHarness(t)
	h.grant(t, "g-rot", "github.review.post", "repo:octocat/*")

	// Pre-rotation sanity: dispatch presents the originally sealed token.
	if _, err := h.d.Dispatch(ctx, baseRequest(0)); err != nil {
		t.Fatalf("pre-rotation Dispatch: %v", err)
	}
	if cred := h.provider.lastCall(t).Credential; cred != dispatchCredential {
		t.Fatalf("pre-rotation credential = %q, want %q", cred, dispatchCredential)
	}

	req := RotateRequest{
		WorkspaceKey:     dispatchWS,
		ConnectorID:      dispatchConn,
		NewInboundSecret: rotateNewSecret,
		NewCredential:    []byte(rotateNewCred),
	}
	if _, err := Rotate(ctx, h.ms.Connectors(), h.ms.ConnectorCalls(), h.sealer.Sealer, req); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !bytes.Equal(req.NewCredential, make([]byte, len(rotateNewCred))) {
		t.Fatalf("plaintext credential buffer not wiped after Rotate: %q", req.NewCredential)
	}

	if _, err := h.d.Dispatch(ctx, baseRequest(1)); err != nil {
		t.Fatalf("post-rotation Dispatch: %v", err)
	}
	if cred := h.provider.lastCall(t).Credential; cred != rotateNewCred {
		t.Fatalf("post-rotation credential = %q, want rotated token %q", cred, rotateNewCred)
	}
}

func TestRotateAppendsAuditRecord(t *testing.T) {
	ctx := context.Background()
	ms, vault := newRotateHarness(t)
	req := baseRotateRequest()
	req.NewCredential = []byte(rotateNewCred)
	rotated, err := Rotate(ctx, ms.Connectors(), ms.ConnectorCalls(), vault, req)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	recs, err := ms.ConnectorCalls().ListByBinding(ctx, rotateWS, RotationAuditBindingID, store.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("rotation audit records = %d, want 1", len(recs))
	}
	rec := recs[0]
	if rec.Decision != domain.ConnectorCallGranted {
		t.Fatalf("Decision = %q, want granted", rec.Decision)
	}
	if rec.Action != RotationAuditAction || rec.ConnectorID != rotateConn ||
		rec.SourceKind != domain.ConnectorSourceGitHub || rec.Resource != "connector:"+rotateConn {
		t.Fatalf("audit record fields = %+v", rec)
	}
	if rotated.RotatedAt == nil || !rec.OccurredAt.Equal(rotated.RotatedAt.UTC()) {
		t.Fatalf("OccurredAt = %v, want store RotatedAt %v", rec.OccurredAt, rotated.RotatedAt)
	}
	if !strings.Contains(rec.SanitizedSummary, "resealed=true") {
		t.Fatalf("summary = %q, want resealed=true", rec.SanitizedSummary)
	}
	// The journal carries no secret material anywhere.
	for _, secret := range []string{rotateOldSecret, rotateNewSecret, rotateOldCred, rotateNewCred} {
		blob := fmt.Sprintf("%+v", rec)
		if strings.Contains(blob, secret) {
			t.Fatalf("audit record leaks secret %q: %s", secret, blob)
		}
	}

	// A second rotation appends a second, distinct record.
	again := baseRotateRequest()
	again.NewInboundSecret = "whsec-THIRD-inbound-4"
	if _, err := Rotate(ctx, ms.Connectors(), ms.ConnectorCalls(), nil, again); err != nil {
		t.Fatalf("second Rotate: %v", err)
	}
	recs, err = ms.ConnectorCalls().ListByBinding(ctx, rotateWS, RotationAuditBindingID, store.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	if len(recs) != 2 || recs[0].CallID == recs[1].CallID {
		t.Fatalf("rotation audit records = %d (ids %q, %q), want 2 distinct", len(recs), recs[0].CallID, recs[len(recs)-1].CallID)
	}
}

type fixedRotatedAtConnectorStore struct {
	store.ConnectorStore
	rotatedAt time.Time
}

func (s *fixedRotatedAtConnectorStore) RotateSecrets(
	ctx context.Context,
	workspaceKey, connectorID string,
	rotation store.ConnectorSecretRotation,
) (*domain.Connector, error) {
	rotated, err := s.ConnectorStore.RotateSecrets(ctx, workspaceKey, connectorID, rotation)
	if rotated != nil {
		rotated.RotatedAt = &s.rotatedAt
	}
	return rotated, err
}

func TestRotateAuditIdentityUsesMonotonicGeneration(t *testing.T) {
	ctx := context.Background()
	ms, _ := newRotateHarness(t)
	connectors := &fixedRotatedAtConnectorStore{
		ConnectorStore: ms.Connectors(),
		rotatedAt:      time.Now().UTC().Truncate(time.Second),
	}
	if _, err := Rotate(ctx, connectors, ms.ConnectorCalls(), nil, baseRotateRequest()); err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	second := baseRotateRequest()
	second.NewInboundSecret = "whsec-THIRD-inbound-4"
	if _, err := Rotate(ctx, connectors, ms.ConnectorCalls(), nil, second); err != nil {
		t.Fatalf("second Rotate: %v", err)
	}
	recs, err := ms.ConnectorCalls().ListByBinding(
		ctx,
		rotateWS,
		RotationAuditBindingID,
		store.ConnectorCallFilter{},
	)
	if err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	if len(recs) != 2 || recs[0].CallID == recs[1].CallID {
		t.Fatalf("same-wall-time rotations collapsed in audit: %+v", recs)
	}
}

// failingAuditStore rejects every append.
type failingAuditStore struct {
	store.ConnectorAuditStore
}

func (f *failingAuditStore) Append(context.Context, *domain.ConnectorCallRecord) error {
	return errors.New("journal down")
}

func TestRotateAuditFailureStillReturnsRotatedConnector(t *testing.T) {
	ctx := context.Background()
	ms, _ := newRotateHarness(t)
	rotated, err := Rotate(ctx, ms.Connectors(), &failingAuditStore{}, nil, baseRotateRequest())
	if err == nil || !strings.Contains(err.Error(), "journal down") {
		t.Fatalf("Rotate = %v, want audit failure surfaced", err)
	}
	if rotated == nil {
		t.Fatal("rotated connector = nil, want the landed rotation returned alongside the audit error")
	}
	secrets, rerr := ms.Connectors().ResolveInboundSecret(ctx, rotateWS, rotateConn)
	if rerr != nil {
		t.Fatalf("ResolveInboundSecret: %v", rerr)
	}
	if secrets.Current != rotateNewSecret {
		t.Fatalf("Current = %q, want rotation landed despite audit failure", secrets.Current)
	}
}

func TestRotateNilAuditStoreSkipsJournal(t *testing.T) {
	ms, _ := newRotateHarness(t)
	if _, err := Rotate(context.Background(), ms.Connectors(), nil, nil, baseRotateRequest()); err != nil {
		t.Fatalf("Rotate with nil audit store: %v", err)
	}
}

// failingSealer rejects every Seal.
type failingSealer struct{ Sealer }

func (f *failingSealer) Seal([]byte, []byte) ([]byte, error) {
	return nil, errors.New("seal exploded")
}

func TestRotateSealFailureWritesNothing(t *testing.T) {
	ctx := context.Background()
	ms, vault := newRotateHarness(t)
	req := baseRotateRequest()
	req.NewCredential = []byte(rotateNewCred)
	if _, err := Rotate(ctx, ms.Connectors(), ms.ConnectorCalls(), &failingSealer{}, req); err == nil || !strings.Contains(err.Error(), "seal exploded") {
		t.Fatalf("Rotate = %v, want seal failure", err)
	}
	// Inbound secret untouched and the OLD credential still unseals.
	secrets, err := ms.Connectors().ResolveInboundSecret(ctx, rotateWS, rotateConn)
	if err != nil {
		t.Fatalf("ResolveInboundSecret: %v", err)
	}
	if secrets.Current != rotateOldSecret || secrets.Previous != "" {
		t.Fatalf("secrets after failed seal = %+v, want untouched", secrets)
	}
	sealed, err := ms.Connectors().ResolveOutboundCredentialSealed(ctx, rotateWS, rotateConn)
	if err != nil {
		t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
	}
	plain, err := vault.Unseal(sealed, CredentialAAD(rotateWS, rotateConn))
	if err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	if string(plain) != rotateOldCred {
		t.Fatalf("credential after failed seal = %q, want old credential intact", plain)
	}
}
