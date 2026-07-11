// rotate.go is the serve-side rotation ceremony (CV13) — the operational
// answer to "the secret leaked" (S6). One Rotate call:
//
//   - installs a new inbound webhook signing secret, demoting the current one
//     to PreviousInboundSecret with a bounded dual-validity window (default
//     domain.DefaultConnectorSecretOverlap, capped at
//     domain.MaxConnectorSecretOverlap) so in-flight deliveries keep
//     verifying — the inbound verifier emits a stale-secret audit signal on
//     every match against the previous secret;
//   - optionally seals a replacement outbound credential through the Sealer
//     seam (AAD-bound to the workspace+connector identity) and swaps it in
//     the same store write, so stores only ever see ciphertext.
//
// The old outbound credential gets NO grace window: dispatch resolves and
// unseals the credential fresh on every call (see Dispatcher.Dispatch step
// 4), so the swap is effective for the very next egress. In-flight calls that
// already unsealed the old credential complete with it — an accepted,
// documented window, attributable in the audit trail via the connector's
// RotatedAt timestamp on the rotation record.
//
// Both legs land in ONE ConnectorStore.RotateSecrets write, so a reader never
// observes the new inbound secret without the new credential (or vice versa).
package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Rotation audit identifiers. A rotation is a control-plane ceremony, not an
// egress call, but it reuses the connector-call journal (the CV13
// decision-channel-reuse option) so one trail covers the whole connector
// lifecycle. The synthetic binding id keeps rotation rows clearly apart from
// real egress and makes ListByBinding(ws, RotationAuditBindingID) the query
// handle for "every rotation in this workspace".
const (
	// RotationAuditBindingID is the synthetic BindingID rotation records are
	// journaled under — rotations have no trigger binding.
	RotationAuditBindingID = "connector-rotation"
	// RotationAuditAction is the dotted action recorded for a rotation.
	RotationAuditAction = "connector.rotate"
)

// Rotation sentinel errors.
var (
	// ErrRotationConflict indicates the connector changed between the
	// caller's read and this rotation (RotateRequest.ExpectedUpdatedAt
	// mismatch): a concurrent rotation or update won, so this writer is
	// rejected and must re-read before retrying.
	ErrRotationConflict = fmt.Errorf("connector: rotation conflict: %w", domain.ErrConflict)

	// ErrRotationSealerMissing indicates a replacement outbound credential
	// was supplied without a Sealer to seal it. The rotation is refused
	// before any store access so plaintext can never reach a store.
	ErrRotationSealerMissing = fmt.Errorf("connector: rotation with a new outbound credential requires a sealer: %w", domain.ErrInvalid)
)

// RotateRequest carries one rotation ceremony's inputs.
type RotateRequest struct {
	WorkspaceKey string
	ConnectorID  string

	// NewInboundSecret replaces the inbound webhook signing secret. The
	// current secret is demoted to PreviousInboundSecret for the dual-secret
	// window instead of being dropped.
	NewInboundSecret string

	// NewCredential is the PLAINTEXT replacement outbound credential; nil
	// leaves the existing sealed credential in place. Rotate seals it before
	// any store write and wipes the slice before returning, so callers must
	// not reuse the buffer.
	NewCredential []byte

	// InboundWindow bounds how long the previous inbound secret keeps
	// verifying. Zero applies domain.DefaultConnectorSecretOverlap (15m);
	// values above domain.MaxConnectorSecretOverlap (24h) are clamped to the
	// cap; negative durations are invalid.
	InboundWindow time.Duration

	// ExpectedUpdatedAt, when non-zero, is a best-effort optimistic fence:
	// the rotation is rejected with ErrRotationConflict when the connector's
	// UpdatedAt no longer matches, so of two operators acting on the same
	// read, the second writer is rejected. The check happens on a fresh read
	// inside Rotate; the residual read-to-write race is accepted and
	// documented (the authoritative conditional write belongs to the store
	// op, mirroring the accepted-TOCTOU freshness decision).
	ExpectedUpdatedAt time.Time

	// Now overrides the clock for deterministic tests; nil means time.Now.
	Now func() time.Time
}

// validate checks request shape before any store or sealer access.
// Violations wrap domain.ErrInvalid.
func (r RotateRequest) validate() error {
	if r.WorkspaceKey == "" {
		return fmt.Errorf("connector rotate workspace_key required: %w", domain.ErrInvalid)
	}
	if r.ConnectorID == "" {
		return fmt.Errorf("connector rotate connector_id required: %w", domain.ErrInvalid)
	}
	if r.NewInboundSecret == "" {
		return fmt.Errorf("connector rotate new_inbound_secret required: %w", domain.ErrInvalid)
	}
	if r.InboundWindow < 0 {
		return fmt.Errorf("connector rotate inbound_window %v negative: %w", r.InboundWindow, domain.ErrInvalid)
	}
	return nil
}

// Rotate performs one connector rotation ceremony end to end:
//
//  1. resolve the connector (existence, source kind for the audit row, and
//     the UpdatedAt fence read — Get only ever returns a redacted copy),
//  2. seal the replacement outbound credential, when supplied, through the
//     Sealer seam (plaintext is wiped immediately after sealing),
//  3. apply both legs in a single ConnectorStore.RotateSecrets write with
//     PreviousSecretValidUntil = now + window (15m default, 24h cap),
//  4. append a rotation record to the connector-call journal.
//
// On an audit-append failure the rotation has already landed, so Rotate
// returns the rotated (redacted) connector TOGETHER WITH the error; callers
// can act on the rotation while surfacing the journaling failure. audit may
// be nil for callers without a journal; sealer is only required when
// NewCredential is supplied.
func Rotate(ctx context.Context, connectors store.ConnectorStore, audit store.ConnectorAuditStore, sealer Sealer, req RotateRequest) (*domain.Connector, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if len(req.NewCredential) > 0 && sealer == nil {
		return nil, ErrRotationSealerMissing
	}
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	validUntil := now().UTC().Add(overlapWindow(req.InboundWindow))

	// (1) Resolve: not-found fails here, the source kind feeds the audit
	// record, and UpdatedAt feeds the optimistic fence.
	current, err := connectors.Get(ctx, req.WorkspaceKey, req.ConnectorID)
	if err != nil {
		return nil, fmt.Errorf("connector rotate resolve %q: %w", req.ConnectorID, err)
	}
	if !req.ExpectedUpdatedAt.IsZero() && !current.UpdatedAt.Equal(req.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("connector %q updated at %s, caller expected %s: %w",
			req.ConnectorID,
			current.UpdatedAt.UTC().Format(time.RFC3339Nano),
			req.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano),
			ErrRotationConflict)
	}

	rotation := store.ConnectorSecretRotation{
		NewInboundSecret:         req.NewInboundSecret,
		PreviousSecretValidUntil: validUntil,
	}
	// (2) Seal before the store write; the plaintext buffer is wiped either
	// way so it cannot outlive this call.
	if len(req.NewCredential) > 0 {
		sealed, serr := sealer.Seal(req.NewCredential, CredentialAAD(req.WorkspaceKey, req.ConnectorID))
		zeroBytes(req.NewCredential)
		if serr != nil {
			return nil, fmt.Errorf("connector rotate seal credential for %q: %w", req.ConnectorID, serr)
		}
		rotation.NewOutboundCredentialSealed = sealed
	}

	// (3) Single store write applies both legs atomically.
	rotated, err := connectors.RotateSecrets(ctx, req.WorkspaceKey, req.ConnectorID, rotation)
	if err != nil {
		return nil, fmt.Errorf("connector rotate %q: %w", req.ConnectorID, err)
	}

	// (4) Journal the ceremony.
	if aerr := appendRotationAudit(ctx, audit, current.SourceKind, rotated, validUntil, rotation.NewOutboundCredentialSealed != nil, now); aerr != nil {
		return rotated, aerr
	}
	return rotated, nil
}

// overlapWindow normalizes the requested dual-secret window: zero applies the
// 15m default and anything above the 24h cap is clamped (negative values are
// rejected by validate before this runs). Stores re-apply the same cap
// defensively against their own clock.
func overlapWindow(window time.Duration) time.Duration {
	switch {
	case window == 0:
		return domain.DefaultConnectorSecretOverlap
	case window > domain.MaxConnectorSecretOverlap:
		return domain.MaxConnectorSecretOverlap
	}
	return window
}

// appendRotationAudit appends the rotation record to the connector-call
// journal. The run id embeds the store-assigned RotatedAt so every rotation
// gets a distinct deterministic CallID; a duplicate append (retry) is treated
// as success exactly like Dispatcher.appendAudit. The summary carries
// timestamps and flags only — never secret material.
func appendRotationAudit(ctx context.Context, audit store.ConnectorAuditStore, kind domain.ConnectorSourceKind, rotated *domain.Connector, validUntil time.Time, resealed bool, now func() time.Time) error {
	if audit == nil {
		return nil
	}
	occurredAt := now().UTC()
	if rotated.RotatedAt != nil && !rotated.RotatedAt.IsZero() {
		occurredAt = rotated.RotatedAt.UTC()
	}
	runID := fmt.Sprintf("rotation-%s-%d", rotated.ConnectorID, occurredAt.UnixNano())
	rec := &domain.ConnectorCallRecord{
		WorkspaceKey: rotated.WorkspaceKey,
		CallID:       domain.ConnectorCallID(runID, RotationAuditAction, 0),
		RunID:        runID,
		BindingID:    RotationAuditBindingID,
		ConnectorID:  rotated.ConnectorID,
		SourceKind:   kind,
		Action:       RotationAuditAction,
		Resource:     "connector:" + rotated.ConnectorID,
		Decision:     domain.ConnectorCallGranted,
		SanitizedSummary: fmt.Sprintf(
			"inbound secret rotated (previous secret valid until %s); outbound credential resealed=%t",
			validUntil.UTC().Format(time.RFC3339), resealed),
		OccurredAt: occurredAt,
	}
	if err := audit.Append(ctx, rec); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("connector rotate audit %q: %w", rec.CallID, err)
	}
	return nil
}
