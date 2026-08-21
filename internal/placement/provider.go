// Package placement brokers lead sandbox placement records against a narrow
// provider interface.
package placement

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
)

const (
	// PlacementLabelKey is applied in Provider.Create so reconciliation can
	// match a provider sandbox back to the placement record after a crash.
	PlacementLabelKey = "loom-placement"

	// EnvironmentLabelKey scopes provider-side reconciliation to one Loom
	// deployment sharing a provider account.
	EnvironmentLabelKey = "loom-env"

	// LeadPTYSessionID is the provider PTY session id used for every lead.
	// Serve names every lead PTY by this convention and stores nothing, so
	// the terminal layer must attach using the same id the broker created --
	// two literals that silently drift would make every attach fail as
	// "session not found".
	LeadPTYSessionID = "lead"

	// BootstrapLoomPath is the serve route that streams serve's own loom
	// binary to a booting lead sandbox. The broker builds the download URL by
	// appending this to the public serve origin; the leadapi module registers
	// the same literal as its route. Both reference this const so the two never
	// drift into a silent 404.
	BootstrapLoomPath = "/api/lead/bootstrap/loom"

	// OccupantTokenEnv is the sandbox environment variable carrying the lead
	// API occupant bearer token.
	OccupantTokenEnv = "LOOM_LEAD_OCCUPANT_TOKEN" //nolint:gosec // env var name, not a credential

	// Lead capabilities are re-exported for placement-created lead nodes and
	// occupant tokens. A sandboxed lead needs all five to run its orchestrator
	// loop and use the allowlisted issue-data surface.
	CapLeadSession    = leadtoken.CapLeadSession
	CapLeadAssignment = leadtoken.CapLeadAssignment
	CapLeadInbox      = leadtoken.CapLeadInbox
	CapLeadData       = leadtoken.CapLeadData
	CapLeadDispatch   = leadtoken.CapLeadDispatch
)

// ErrSandboxNotFound lets release confirmation and resume paths distinguish a
// confirmed-missing sandbox from a provider-side failure.
var ErrSandboxNotFound = errors.New("placement: sandbox not found")

// ErrPtySessionAlreadyExists is a successful CreatePty outcome for the broker:
// the requested idempotent session is already present.
var ErrPtySessionAlreadyExists = errors.New("placement: pty session already exists")

// Provider is the narrow sandbox provider surface required by the broker.
type Provider interface {
	Create(context.Context, CreateRequest) (CreateResult, error)
	Get(context.Context, string) (ProviderSandbox, error)
	// FindByName resolves a sandbox by the caller-supplied CreateRequest.Name
	// via an authoritative point read (never an eventually-consistent list).
	// Providers return ErrSandboxNotFound when no sandbox has the name.
	FindByName(context.Context, string) (ProviderSandbox, error)
	EnsureRunning(context.Context, string) (bool, error)
	ListManaged(context.Context, map[string]string) ([]ProviderSandbox, error)
	Delete(context.Context, string) error
	UpdateLastActivity(context.Context, string) error
	SetAutostopInterval(context.Context, string, time.Duration) error
	// PrepareLeadBoot materializes the lead's working state in the sandbox
	// before the lead PTY is created: the repo checkout and role prompt file.
	PrepareLeadBoot(ctx context.Context, sandboxID string, prep LeadBootPrep) error
	// CreatePty creates the requested PTY session if absent. Providers should
	// return ErrPtySessionAlreadyExists when the session already exists.
	CreatePty(context.Context, string, ProcessSpec) error
	ListPtySessions(context.Context, string) ([]PtySession, error)
	KillPtySession(context.Context, string, string) error
}

// ResourceSize is the reserved provider pool capacity for one placement.
type ResourceSize struct {
	VCPU   int
	MemGiB int
}

// CreateRequest is the provider-neutral sandbox creation request.
type CreateRequest struct {
	WorkspaceKey string
	AgentName    string
	SnapshotRef  string
	// Name is a stable, collision-resistant provider-side sandbox name derived
	// from the placement identity. Providers that support caller-supplied names
	// must send it so an ambiguous create can later be reconciled with an
	// authoritative FindByName point read instead of an eventually-consistent
	// label list.
	Name                   string
	Labels                 map[string]string
	Env                    map[string]string
	Resource               ResourceSize
	NetworkDomainAllowlist []string
}

// CreateOutcome classifies how far a Create call provably got. It exists so
// the broker can distinguish "no sandbox can exist" from "a sandbox may exist
// even though the call errored" — releasing a placement in the second case
// severs the only record of a possibly-billing sandbox.
type CreateOutcome string

const (
	// CreateOutcomeUnknown is the fail-closed default: the request may have
	// reached the provider, so a sandbox may exist. The zero value ("") must
	// be treated exactly like CreateOutcomeUnknown everywhere.
	CreateOutcomeUnknown CreateOutcome = "unknown"
	// CreateOutcomeNotDispatched asserts the request provably never left the
	// process (for example local payload validation failed), so no sandbox can
	// exist. Providers must only return it for failures before any network I/O.
	CreateOutcomeNotDispatched CreateOutcome = "not_dispatched"
	// CreateOutcomeCreated asserts the provider acknowledged the sandbox and
	// returned its identity.
	CreateOutcomeCreated CreateOutcome = "created"
)

// CreateResult returns the provider's sandbox identity and how far the create
// provably got.
type CreateResult struct {
	SandboxID string
	Outcome   CreateOutcome
}

// ProvablyNotDispatched reports whether the provider asserted no sandbox can
// exist. Every other outcome — including the zero value — is ambiguous and
// must be treated as "a sandbox may exist".
func (r CreateResult) ProvablyNotDispatched() bool {
	return r.Outcome == CreateOutcomeNotDispatched
}

// LeadBootPrep is the purpose-scoped pre-PTY materialization request for an
// interactive lead placement.
type LeadBootPrep struct {
	Repo *RepoClone
	// GitToken resolves the clone token at prep time. The token must never be
	// stored on this struct or surfaced in logs/errors.
	GitToken   func() (string, error)
	PromptPath string
	PromptText string
	// Files are seeded into the sandbox before the lead PTY starts (ticket 08:
	// the codex auth.json drop rides this). Contents may be credentials and
	// must never appear in logs or errors.
	Files []SandboxFile
	// BootstrapBinary, when set, is downloaded and atomically installed into
	// the sandbox before any other prep step so the lead PTY boots the freshly
	// served binary instead of the one baked into the snapshot. Nil leaves the
	// baked binary in place (behavior byte-identical to no bootstrap). The URL
	// and paths are serve-supplied config, never sandbox-influenced.
	BootstrapBinary *BootstrapBinarySpec
	// Timeout bounds each prep exec command. Zero uses the provider default.
	Timeout time.Duration
}

// BootstrapBinarySpec instructs the provider to download a binary over HTTP and
// atomically install it at an absolute in-sandbox path before the lead PTY
// starts. Every field is serve-supplied config; none is derived from
// sandbox-resident or occupant input.
type BootstrapBinarySpec struct {
	// URL is the http(s) source served by loom serve (BootstrapLoomPath).
	URL string
	// Dest is the absolute in-sandbox install path (e.g. /usr/local/bin/loom).
	Dest string
	// Mode is an octal chmod string applied to the installed file (e.g. "0755").
	Mode string
}

// SandboxFile is a file seeded into the sandbox during lead-boot prep.
type SandboxFile struct {
	// Path is the absolute in-sandbox destination.
	Path string
	// Content is written atomically (write-then-rename).
	Content []byte
	// Mode is an octal chmod string (e.g. "600"). Empty leaves the default.
	Mode string
}

// RepoClone describes the one-shot checkout a provider should create before
// the lead PTY starts.
type RepoClone struct {
	Name      string
	RemoteURL string
	Ref       string
	Checkout  string
}

// ProcessSpec describes the PTY process the provider starts in a sandbox.
type ProcessSpec struct {
	SessionID  string
	Command    []string
	Env        map[string]string
	WorkingDir string
	TTY        bool
}

// PtySession is the provider-visible PTY identity used for idempotent lead boot
// checks.
type PtySession struct {
	SessionID string
}

// ProviderSandboxState is the provider-visible sandbox lifecycle state.
type ProviderSandboxState string

const (
	ProviderSandboxRunning ProviderSandboxState = "running"
	ProviderSandboxStopped ProviderSandboxState = "stopped"
	ProviderSandboxAbsent  ProviderSandboxState = "absent"
)

// ProviderSandboxRawState preserves the provider lifecycle state when attach
// readiness cannot be inferred from the neutral reconciliation state.
type ProviderSandboxRawState string

const (
	ProviderSandboxRawStarted     ProviderSandboxRawState = "started"
	ProviderSandboxRawStopped     ProviderSandboxRawState = "stopped"
	ProviderSandboxRawArchived    ProviderSandboxRawState = "archived"
	ProviderSandboxRawPaused      ProviderSandboxRawState = "paused"
	ProviderSandboxRawStarting    ProviderSandboxRawState = "starting"
	ProviderSandboxRawRestoring   ProviderSandboxRawState = "restoring"
	ProviderSandboxRawResuming    ProviderSandboxRawState = "resuming"
	ProviderSandboxRawStopping    ProviderSandboxRawState = "stopping"
	ProviderSandboxRawPausing     ProviderSandboxRawState = "pausing"
	ProviderSandboxRawArchiving   ProviderSandboxRawState = "archiving"
	ProviderSandboxRawError       ProviderSandboxRawState = "error"
	ProviderSandboxRawBuildFailed ProviderSandboxRawState = "build_failed"
	ProviderSandboxRawDestroyed   ProviderSandboxRawState = "destroyed"
)

// ProviderSandbox is a reconciliation view of provider-side sandbox state.
type ProviderSandbox struct {
	ID        string
	Labels    map[string]string
	State     ProviderSandboxState
	RawState  ProviderSandboxRawState
	CreatedAt time.Time
}

// NormalizeRepoCloneRemote implements the fail-closed remote URL policy for
// provisioned lead checkouts.
func NormalizeRepoCloneRemote(raw string) (normalized string, host string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("repo clone remote URL required: %w", domain.ErrInvalid)
	}
	if rest, ok := strings.CutPrefix(raw, "git@github.com:"); ok {
		rest = strings.TrimSuffix(rest, ".git")
		if !validGitHubOwnerRepo(rest) {
			return "", "", unsupportedRepoCloneRemoteError()
		}
		return "https://github.com/" + rest, "github.com", nil
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", "", unsupportedRepoCloneRemoteError()
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", unsupportedRepoCloneRemoteError()
	}
	host = strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", "", unsupportedRepoCloneRemoteError()
	}
	return raw, host, nil
}

func validGitHubOwnerRepo(rest string) bool {
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return false
	}
	return strings.TrimSpace(parts[0]) != "" &&
		strings.TrimSpace(parts[1]) != "" &&
		!strings.ContainsAny(rest, " \t\r\n")
}

func unsupportedRepoCloneRemoteError() error {
	return fmt.Errorf(
		"unsupported repo clone remote URL: only https:// and git@github.com:owner/repo(.git) are supported: %w",
		domain.ErrInvalid,
	)
}
