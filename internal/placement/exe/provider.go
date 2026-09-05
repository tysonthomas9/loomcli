package exe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// Config configures the exe.dev provider.
type Config struct {
	// Token authenticates to the control plane. serve holds it; a lead never
	// does (the principal-class rule).
	Token string
	// Endpoint overrides the control-plane URL. Empty uses DefaultEndpoint.
	Endpoint string
	// SSHKeyPath is the private key used to reach VMs. Required.
	SSHKeyPath string
	// HostKeyPath persists trust-on-first-use VM host keys. Required: without
	// somewhere to persist them there is nothing to compare against, and every
	// connection would be a first connection.
	HostKeyPath string
	// Image is the VM image for new sandboxes.
	Image string
	// AllowUnrestrictedEgress acknowledges that exe.dev has NO egress policy
	// control. Without it, a provision request carrying a network allowlist is
	// REFUSED rather than silently granted unrestricted network access.
	//
	// It is per-provider on purpose: a deployment that runs Daytona leads
	// under an allowlist keeps that enforcement for Daytona, and has to opt in
	// separately -- and visibly -- to give exe leads open egress.
	AllowUnrestrictedEgress bool
	// RequestTimeout bounds a control-plane call. Zero uses 60s.
	RequestTimeout time.Duration
	// DialTimeout bounds an SSH dial. Zero uses 30s.
	DialTimeout time.Duration
}

// Provider implements placement.Provider for exe.dev.
type Provider struct {
	control  *controlClient
	dialer   *sshDialer
	hostKeys *hostKeyStore
	image    string

	allowUnrestrictedEgress bool
}

// New builds a provider. It fails closed on missing credentials rather than
// deferring the error to the first provision.
func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("exe: control-plane token required")
	}
	if strings.TrimSpace(cfg.SSHKeyPath) == "" {
		return nil, errors.New("exe: ssh key path required")
	}
	if strings.TrimSpace(cfg.HostKeyPath) == "" {
		return nil, errors.New("exe: host key store path required (host keys must be pinned, not ignored)")
	}
	keyPEM, err := os.ReadFile(cfg.SSHKeyPath)
	if err != nil {
		return nil, fmt.Errorf("exe: read ssh key: %w", err)
	}
	hostKeys := newHostKeyStore(cfg.HostKeyPath)
	if err := hostKeys.load(); err != nil {
		return nil, err
	}
	dialer, err := newSSHDialer(keyPEM, hostKeys, cfg.DialTimeout)
	if err != nil {
		return nil, err
	}
	return &Provider{
		control:  newControlClient(cfg.Token, cfg.Endpoint, cfg.RequestTimeout),
		dialer:   dialer,
		hostKeys: hostKeys,
		image:    strings.TrimSpace(cfg.Image),

		allowUnrestrictedEgress: cfg.AllowUnrestrictedEgress,
	}, nil
}

// SupportsParking reports false: exe.dev cannot stop or start a VM, so there
// is no autostop interval to set. placement gates every parking call on this.
func (p *Provider) SupportsParking() bool { return false }

// Create provisions a VM.
//
// The outcome taxonomy is the load-bearing part: NotDispatched is returned
// ONLY when local validation failed before any network I/O. Everything else --
// including a 422 duplicate-name, which PROVES a same-name VM exists -- is
// Unknown, so the broker keeps the placement record instead of severing the
// only reference to a billing sandbox.
func (p *Provider) Create(ctx context.Context, req placement.CreateRequest) (placement.CreateResult, error) {
	// exe.dev has no equivalent of Daytona's create-time domain allow list.
	// Ignoring the field would be a silent fail-OPEN: the broker would believe
	// the lead's egress is confined to the allowlisted hosts while the VM can
	// in fact reach anything. Refuse instead -- and NotDispatched is exact
	// here, since nothing has been sent yet.
	if len(req.NetworkDomainAllowlist) > 0 && !p.allowUnrestrictedEgress {
		return placement.CreateResult{Outcome: placement.CreateOutcomeNotDispatched}, fmt.Errorf(
			"exe: provision requested a network domain allowlist (%d entries) but exe.dev cannot enforce egress policy; "+
				"set AllowUnrestrictedEgress to accept open egress for exe leads, or place this lead on a provider that enforces it",
			len(req.NetworkDomainAllowlist))
	}
	opts := createOpts{
		Name:   req.Name,
		CPU:    req.Resource.VCPU,
		Memory: memoryArg(req.Resource.MemGiB),
		Image:  p.image,
		Tags:   labelsToTags(req.Labels),
		Env:    req.Env,
	}
	outcome, err := p.control.create(ctx, opts)
	switch outcome {
	case outcomeCreated:
		// The VM name IS the sandbox id: exe.dev has no separate identifier,
		// and the name is what every later call addresses.
		return placement.CreateResult{SandboxID: req.Name, Outcome: placement.CreateOutcomeCreated}, nil
	case outcomeNotDispatched:
		return placement.CreateResult{Outcome: placement.CreateOutcomeNotDispatched}, err
	default:
		return placement.CreateResult{Outcome: placement.CreateOutcomeUnknown}, err
	}
}

// Get is an authoritative point read.
func (p *Provider) Get(ctx context.Context, sandboxID string) (placement.ProviderSandbox, error) {
	found, err := p.pointRead(ctx, sandboxID)
	if err != nil {
		return placement.ProviderSandbox{}, err
	}
	return toProviderSandbox(*found), nil
}

// FindByName resolves a sandbox by its caller-supplied name. On exe.dev the
// name and the sandbox id are the same value, so this is Get.
func (p *Provider) FindByName(ctx context.Context, name string) (placement.ProviderSandbox, error) {
	return p.Get(ctx, name)
}

func (p *Provider) pointRead(ctx context.Context, name string) (*vm, error) {
	vms, err := p.control.list(ctx, name)
	if err != nil {
		return nil, err
	}
	for i := range vms {
		if vms[i].Name == name {
			return &vms[i], nil
		}
	}
	// Reached only after list proved a well-formed, authorized, empty result.
	return nil, fmt.Errorf("exe sandbox %q: %w", name, placement.ErrSandboxNotFound)
}

// dial resolves the service-returned SSH route on every connection. exe.dev
// may expose a direct per-VM hostname or a shared gateway with routing encoded
// in the SSH user; the VM record is authoritative for the current account.
func (p *Provider) dial(ctx context.Context, sandboxID string) (*ssh.Client, error) {
	found, err := p.pointRead(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	route, err := sshRouteForVM(*found)
	if err != nil {
		return nil, err
	}
	return p.dialer.dial(ctx, sandboxID, route)
}

// EnsureRunning reports whether the sandbox is usable.
//
// exe.dev has no stop/start, so there is nothing to resume: a VM is either
// running or it is gone. It returns false for "was already running" because
// nothing was resumed -- claiming otherwise would tell the broker a recovery
// happened that never did.
func (p *Provider) EnsureRunning(ctx context.Context, sandboxID string) (bool, error) {
	found, err := p.pointRead(ctx, sandboxID)
	if err != nil {
		return false, err
	}
	if state := neutralState(found.Status); state != placement.ProviderSandboxRunning {
		return false, fmt.Errorf("exe sandbox %q is %q and cannot be started (exe.dev has no start operation)", sandboxID, found.Status)
	}
	return false, nil
}

// ListManaged returns sandboxes carrying every requested label.
func (p *Provider) ListManaged(ctx context.Context, labels map[string]string) ([]placement.ProviderSandbox, error) {
	vms, err := p.control.list(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]placement.ProviderSandbox, 0, len(vms))
	for i := range vms {
		sandbox := toProviderSandbox(vms[i])
		if !matchesLabels(sandbox.Labels, labels) {
			continue
		}
		out = append(out, sandbox)
	}
	return out, nil
}

// Delete removes a VM and forgets its pinned host key, so a later VM reusing
// the name is pinned afresh rather than rejected as a key change.
func (p *Provider) Delete(ctx context.Context, sandboxID string) error {
	if err := p.control.remove(ctx, sandboxID); err != nil {
		return err
	}
	p.hostKeys.forget(vmHost(sandboxID))
	return nil
}

// UpdateLastActivity is a no-op: exe.dev has no idle timer to defer, so there
// is no activity to report. It returns nil rather than an error because the
// broker calls it on the hot attach path, where failing would break attach for
// a difference that does not matter.
func (p *Provider) UpdateLastActivity(context.Context, string) error { return nil }

// SetAutostopInterval is unreachable: SupportsParking() is false, so placement
// never calls it. It fails loudly rather than silently succeeding, so a future
// change that bypasses the capability gate is caught rather than hidden.
func (p *Provider) SetAutostopInterval(context.Context, string, time.Duration) error {
	return errors.New("exe: sandboxes cannot be parked (no stop/start); this call should have been gated by SupportsParking")
}

// PrepareLeadBoot materializes the lead's working state before the PTY starts.
func (p *Provider) PrepareLeadBoot(ctx context.Context, sandboxID string, prep placement.LeadBootPrep) error {
	client, err := p.dial(ctx, sandboxID)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if prep.BootstrapBinary != nil {
		if err := p.installBootstrapBinary(client, sandboxID, prep.BootstrapBinary); err != nil {
			return err
		}
	}
	if prep.Repo != nil {
		if err := p.cloneRepo(client, sandboxID, prep); err != nil {
			return err
		}
	}
	for _, file := range prep.Files {
		if err := writeFile(clientRunner{client}, file); err != nil {
			return fmt.Errorf("exe seed file in sandbox %q: %w", sandboxID, err)
		}
	}
	if strings.TrimSpace(prep.PromptPath) != "" {
		if err := writeFile(clientRunner{client}, placement.SandboxFile{
			Path: prep.PromptPath, Content: []byte(prep.PromptText), Mode: "644",
		}); err != nil {
			return fmt.Errorf("exe write prompt in sandbox %q: %w", sandboxID, err)
		}
	}
	return nil
}

// CreatePty starts the durable tmux session that backs the lead PTY.
func (p *Provider) CreatePty(ctx context.Context, sandboxID string, spec placement.ProcessSpec) error {
	client, err := p.dial(ctx, sandboxID)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	cmd := tmuxCreateSession(spec.SessionID, spec.WorkingDir, spec.Env, spec.Command)
	out, err := run(client, cmd)
	if err != nil {
		// Idempotency is a success for the broker, not a failure.
		if strings.Contains(out, "duplicate session") {
			return placement.ErrPtySessionAlreadyExists
		}
		return fmt.Errorf("exe create pty in sandbox %q: %w (%s)", sandboxID, err, strings.TrimSpace(out))
	}
	return nil
}

// ListPtySessions returns the live tmux sessions.
func (p *Provider) ListPtySessions(ctx context.Context, sandboxID string) ([]placement.PtySession, error) {
	client, err := p.dial(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	out, err := run(client, fmt.Sprintf("tmux -L %s list-sessions -F '#{session_name}'", tmuxSocket))
	if err != nil {
		// No tmux server means NO SESSIONS, not a failure. The broker reads an
		// error here as a boot failure, so conflating them breaks every first
		// boot.
		if tmuxNoServer(out) {
			return nil, nil
		}
		return nil, fmt.Errorf("exe list pty in sandbox %q: %w (%s)", sandboxID, err, strings.TrimSpace(out))
	}
	var sessions []placement.PtySession
	for _, line := range strings.Split(out, "\n") {
		if id := strings.TrimSpace(line); id != "" {
			sessions = append(sessions, placement.PtySession{SessionID: id})
		}
	}
	return sessions, nil
}

// KillPtySession is absent-safe: killing a session that is already gone is the
// desired state, not an error.
func (p *Provider) KillPtySession(ctx context.Context, sandboxID, sessionID string) error {
	client, err := p.dial(ctx, sandboxID)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// exactTarget, not a bare name: tmux target resolution falls back to prefix
	// matching, so "lead" would kill "lead-old" once "lead" is gone. Killing
	// the wrong lead's session is strictly worse than failing to kill.
	out, err := run(client, fmt.Sprintf("tmux -L %s kill-session -t %s", tmuxSocket, exactTarget(sessionID)))
	if err != nil {
		if strings.Contains(out, "can't find session") || tmuxNoServer(out) {
			return nil
		}
		return fmt.Errorf("exe kill pty %q in sandbox %q: %w (%s)", sessionID, sandboxID, err, strings.TrimSpace(out))
	}
	return nil
}

func memoryArg(memGiB int) string {
	if memGiB <= 0 {
		return ""
	}
	return strconv.Itoa(memGiB) + "gb"
}
