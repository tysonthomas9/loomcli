package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Connector sentinel errors. They wrap the package-level sentinels where a
// generic category applies, so callers can match either the specific or the
// generic error via errors.Is.
var (
	// ErrConnectorExists indicates a connector Create collided with an
	// existing connector of the same identity (first-writer-wins SetNX).
	ErrConnectorExists = fmt.Errorf("domain: connector exists: %w", ErrAlreadyExists)

	// ErrConnectorNotFound indicates the referenced connector does not exist.
	ErrConnectorNotFound = fmt.Errorf("domain: connector not found: %w", ErrNotFound)

	// ErrGrantRevoked indicates the referenced connector grant exists but has
	// been revoked; revoked grants never authorize egress.
	ErrGrantRevoked = errors.New("domain: connector grant revoked")

	// ErrGrantDenied indicates no active grant authorizes the attempted
	// {action, resource} for the binding. Grants are deny-by-default: egress
	// fails closed until an explicit grant exists.
	ErrGrantDenied = errors.New("domain: connector grant denied")
)

// ConnectorSourceKind enumerates the external systems a connector fronts.
type ConnectorSourceKind string

const (
	ConnectorSourceGitHub   ConnectorSourceKind = "github"
	ConnectorSourceSlack    ConnectorSourceKind = "slack"
	ConnectorSourceDatadog  ConnectorSourceKind = "datadog"
	ConnectorSourceInternal ConnectorSourceKind = "internal"
)

// Valid reports whether k is a known source kind.
func (k ConnectorSourceKind) Valid() bool {
	switch k {
	case ConnectorSourceGitHub, ConnectorSourceSlack, ConnectorSourceDatadog, ConnectorSourceInternal:
		return true
	}
	return false
}

// ConnectorStatus enumerates the connector lifecycle.
type ConnectorStatus string

const (
	ConnectorStatusActive   ConnectorStatus = "active"
	ConnectorStatusDisabled ConnectorStatus = "disabled"
)

// Valid reports whether s is a known connector status.
func (s ConnectorStatus) Valid() bool {
	switch s {
	case ConnectorStatusActive, ConnectorStatusDisabled:
		return true
	}
	return false
}

// Inbound dual-secret rotation window bounds: after RotateSecrets the
// previous inbound secret keeps verifying until PreviousSecretValidUntil
// (default 15m from rotation, capped at 24h). Verification against the
// previous secret emits a stale-secret audit signal.
const (
	DefaultConnectorSecretOverlap = 15 * time.Minute
	MaxConnectorSecretOverlap     = 24 * time.Hour
)

// Connector is the per-source control-plane object for step 7: it owns the
// inbound webhook endpoint + signing secret and the sealed outbound
// credential for one named integration. A workspace may hold multiple named
// connectors per source kind, keyed by ConnectorID; bindings and grants
// reference the connector explicitly.
type Connector struct {
	WorkspaceKey string              `json:"workspace_key"`
	ConnectorID  string              `json:"connector_id"`
	SourceKind   ConnectorSourceKind `json:"source_kind"`
	DisplayName  string              `json:"display_name,omitempty"`

	// InboundEndpointPath is the workspace-relative HTTP path the source
	// delivers webhooks to (always "/"-prefixed when set).
	InboundEndpointPath string `json:"inbound_endpoint_path,omitempty"`
	// InboundSecret verifies inbound webhook signatures.
	// PreviousInboundSecret remains valid until PreviousSecretValidUntil
	// after a rotation (dual-secret window). Both are blanked by Redacted.
	InboundSecret            string     `json:"inbound_secret,omitempty"`
	PreviousInboundSecret    string     `json:"previous_inbound_secret,omitempty"`
	PreviousSecretValidUntil *time.Time `json:"previous_secret_valid_until,omitempty"`

	// OutboundCredentialSealed is an opaque ciphertext blob, sealed by
	// serve's vault layer (AES-256-GCM under LOOM_CONNECTOR_VAULT_KEY,
	// behind a sealer interface) BEFORE any store write. Stores never see
	// plaintext; Redacted drops the ciphertext too.
	OutboundCredentialSealed []byte `json:"outbound_credential_sealed,omitempty"`

	Status    ConnectorStatus `json:"status"`
	CreatedBy string          `json:"created_by,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	RotatedAt *time.Time      `json:"rotated_at,omitempty"`
}

// Validate checks the connector's domain invariants. Violations wrap
// ErrInvalid.
func (c *Connector) Validate() error {
	if c.WorkspaceKey == "" {
		return fmt.Errorf("connector workspace_key required: %w", ErrInvalid)
	}
	if c.ConnectorID == "" {
		return fmt.Errorf("connector connector_id required: %w", ErrInvalid)
	}
	if !c.SourceKind.Valid() {
		return fmt.Errorf("connector source_kind %q unknown: %w", c.SourceKind, ErrInvalid)
	}
	if !c.Status.Valid() {
		return fmt.Errorf("connector status %q unknown: %w", c.Status, ErrInvalid)
	}
	if c.InboundEndpointPath != "" && !strings.HasPrefix(c.InboundEndpointPath, "/") {
		return fmt.Errorf("connector inbound_endpoint_path must start with /: %w", ErrInvalid)
	}
	return nil
}

// Redacted returns a copy safe for Get/List responses and logs: inbound
// secrets are blanked and the sealed outbound credential is dropped. The
// privileged Resolve* store paths are the only way to read them back.
func (c Connector) Redacted() Connector {
	c.InboundSecret = ""
	c.PreviousInboundSecret = ""
	c.OutboundCredentialSealed = nil
	return c
}

// HasOutboundCredential reports whether a sealed outbound credential is
// present (survives on pre-redaction values only; redacted copies report
// false by construction).
func (c *Connector) HasOutboundCredential() bool {
	return len(c.OutboundCredentialSealed) > 0
}

// ConnectorGrant authorizes one {action, resource pattern} tuple for one
// binding against one connector. Grants are standalone records keyed by
// binding_id — deliberately not fields on TriggerBinding — and are
// deny-by-default: TriggerBinding.Permissions[] is NOT auto-migrated, so
// egress fails closed until explicit grants exist.
type ConnectorGrant struct {
	WorkspaceKey string `json:"workspace_key"`
	GrantID      string `json:"grant_id"`
	ConnectorID  string `json:"connector_id"`
	BindingID    string `json:"binding_id"`
	// Action is a dotted verb such as "github.merge" or
	// "slack.chat.post_message" (see ValidateConnectorAction).
	Action string `json:"action"`
	// ResourcePattern scopes the grant to matching resources, e.g.
	// "repo:octocat/hello".
	ResourcePattern string     `json:"resource_pattern"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

// Revoked reports whether the grant has been revoked.
func (g *ConnectorGrant) Revoked() bool {
	return g.RevokedAt != nil && !g.RevokedAt.IsZero()
}

// Validate checks the grant's domain invariants. Violations wrap ErrInvalid.
func (g *ConnectorGrant) Validate() error {
	if g.WorkspaceKey == "" {
		return fmt.Errorf("grant workspace_key required: %w", ErrInvalid)
	}
	if g.GrantID == "" {
		return fmt.Errorf("grant grant_id required: %w", ErrInvalid)
	}
	if g.ConnectorID == "" {
		return fmt.Errorf("grant connector_id required: %w", ErrInvalid)
	}
	if g.BindingID == "" {
		return fmt.Errorf("grant binding_id required: %w", ErrInvalid)
	}
	if err := ValidateConnectorAction(g.Action); err != nil {
		return err
	}
	if g.ResourcePattern == "" {
		return fmt.Errorf("grant resource_pattern required: %w", ErrInvalid)
	}
	return nil
}

// ValidateConnectorAction checks the dotted action format: two or more
// non-empty segments of [a-z0-9_-] separated by single dots, e.g.
// "github.merge". Violations wrap ErrInvalid.
func ValidateConnectorAction(action string) error {
	segments := strings.Split(action, ".")
	if len(segments) < 2 {
		return fmt.Errorf("connector action %q needs at least provider.verb segments: %w", action, ErrInvalid)
	}
	for _, seg := range segments {
		if seg == "" {
			return fmt.Errorf("connector action %q has an empty segment: %w", action, ErrInvalid)
		}
		for _, r := range seg {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
				return fmt.Errorf("connector action %q has invalid character %q: %w", action, r, ErrInvalid)
			}
		}
	}
	return nil
}

// ConnectorCallDecision enumerates the outcome recorded for every egress
// attempt — granted AND denied calls are both journaled.
type ConnectorCallDecision string

const (
	ConnectorCallGranted ConnectorCallDecision = "granted"
	ConnectorCallDenied  ConnectorCallDecision = "denied"
	// ConnectorCallStaleSubject marks an egress refused because the
	// pre-egress freshness read showed the subject moved (e.g. branch head
	// changed since the run started).
	ConnectorCallStaleSubject ConnectorCallDecision = "stale_subject"
	// ConnectorCallPreconditionRequired marks an egress refused because the
	// provider demands a server-side precondition (e.g. GitHub merge
	// expected sha) that the caller did not supply.
	ConnectorCallPreconditionRequired ConnectorCallDecision = "precondition_required"
	ConnectorCallUpstreamError        ConnectorCallDecision = "upstream_error"
)

// Valid reports whether d is a known call decision.
func (d ConnectorCallDecision) Valid() bool {
	switch d {
	case ConnectorCallGranted, ConnectorCallDenied, ConnectorCallStaleSubject,
		ConnectorCallPreconditionRequired, ConnectorCallUpstreamError:
		return true
	}
	return false
}

// ConnectorCallID builds the deterministic audit row id "runID#action#seq".
func ConnectorCallID(runID, action string, seq int) string {
	return fmt.Sprintf("%s#%s#%d", runID, action, seq)
}

// ConnectorCallRecord is one append-only audit row in the dedicated
// connector-call journal (separate from TaskRunEvent), written for every
// granted and denied egress call.
type ConnectorCallRecord struct {
	WorkspaceKey string `json:"workspace_key"`
	// CallID is deterministic: ConnectorCallID(RunID, Action, Seq).
	CallID      string                `json:"call_id"`
	Seq         int                   `json:"seq"`
	RunID       string                `json:"run_id"`
	BindingID   string                `json:"binding_id"`
	ConnectorID string                `json:"connector_id"`
	SourceKind  ConnectorSourceKind   `json:"source_kind"`
	Action      string                `json:"action"`
	Resource    string                `json:"resource,omitempty"`
	Decision    ConnectorCallDecision `json:"decision"`
	// UpstreamStatus is the provider HTTP status when egress happened
	// (zero for calls denied before egress).
	UpstreamStatus int    `json:"upstream_status,omitempty"`
	ErrorClass     string `json:"error_class,omitempty"`
	// SanitizedSummary is a redaction-safe human summary; it must never
	// contain secrets, credentials, or raw payload bytes.
	SanitizedSummary string    `json:"sanitized_summary,omitempty"`
	OccurredAt       time.Time `json:"occurred_at"`
}

// Validate checks the record's domain invariants, including that CallID
// matches its deterministic derivation. Violations wrap ErrInvalid.
func (r *ConnectorCallRecord) Validate() error {
	if r.WorkspaceKey == "" {
		return fmt.Errorf("connector call workspace_key required: %w", ErrInvalid)
	}
	if r.RunID == "" {
		return fmt.Errorf("connector call run_id required: %w", ErrInvalid)
	}
	if r.BindingID == "" {
		return fmt.Errorf("connector call binding_id required: %w", ErrInvalid)
	}
	if r.ConnectorID == "" {
		return fmt.Errorf("connector call connector_id required: %w", ErrInvalid)
	}
	if !r.SourceKind.Valid() {
		return fmt.Errorf("connector call source_kind %q unknown: %w", r.SourceKind, ErrInvalid)
	}
	if err := ValidateConnectorAction(r.Action); err != nil {
		return err
	}
	if !r.Decision.Valid() {
		return fmt.Errorf("connector call decision %q unknown: %w", r.Decision, ErrInvalid)
	}
	if want := ConnectorCallID(r.RunID, r.Action, r.Seq); r.CallID != want {
		return fmt.Errorf("connector call_id %q does not match derived %q: %w", r.CallID, want, ErrInvalid)
	}
	return nil
}
