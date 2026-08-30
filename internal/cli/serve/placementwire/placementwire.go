// Package placementwire is the single place a lead sandbox provider gets
// wired in.
//
// It exists as its own package rather than a file in serve because a provider
// is usable only through SEVERAL registrations -- the broker's provider
// registry, the revive registry, and the terminal attach registry -- and each
// omission fails somewhere far from the wiring: an unregistered attach factory
// surfaces as a terminal that will not open, a missing revive entry as leads
// that never come back from a serve restart. Keeping them adjacent is what
// makes a missing one visible. It also keeps serve's own import fanout from
// growing with every provider added.
package placementwire

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadprovision"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
	"github.com/tysonthomas9/loomcli/internal/placement/exe"
	"github.com/tysonthomas9/loomcli/internal/store"
	webuiterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// The lead placement knobs serve reads on this package's behalf. They live
// here because this package is their only consumer.
const (
	envLoomLeadMaxVCPU    = "LOOM_LEAD_MAX_VCPU"
	envLoomLeadMaxMemGiB  = "LOOM_LEAD_MAX_MEM_GIB"
	envLoomLeadAllowlist  = "LOOM_LEAD_ALLOWLIST"
	envLoomLeadAPIBaseURL = "LOOM_LEAD_API_BASE_URL"
	envLoomLeadBootstrap  = "LOOM_LEAD_BOOTSTRAP"
)

// Build constructs the placement broker and its provider registry when this
// deployment is configured to place leads in sandboxes. It returns nil (and
// logs once) unless DAYTONA_API_KEY, LOOM_DEPLOYMENT_ID, and an occupant-token
// signing key are all present -- the broker mints occupant tokens with the same
// key the leadapi module verifies (DriverRunTokenKey), so a mismatch would make
// every sandboxed lead's API call fail. Callers treat nil as "sandbox placement
// disabled".
//
// Daytona gates the whole broker because it is still the default provider;
// exe.dev is added to the registry when separately configured. The returned
// registry -- not any single provider -- is what every later routing decision
// keys off, since a sandbox id is unique only within its provider.
func Build(st store.Store, tokenKey []byte) (*placement.Broker, placement.ProviderRegistry) {
	if strings.TrimSpace(os.Getenv(daytona.APIKeyEnv)) == "" {
		return nil, nil
	}
	if len(tokenKey) == 0 {
		slog.Warn("placement broker disabled: no occupant-token signing key (set LOOM_RUN_TOKEN_SIGNING_KEY)")
		return nil, nil
	}
	provider, err := daytona.New(daytona.Config{})
	if err != nil {
		slog.Error("placement broker disabled: construct Daytona provider", "err", err)
		return nil, nil
	}
	providers := placement.ProviderRegistry{domain.RuntimeProviderDaytona: provider}
	if exeProvider := buildExeProvider(); exeProvider != nil {
		providers[domain.RuntimeProviderExe] = exeProvider
		registerExeTerminalAttach(exeProvider)
	}
	broker, err := newBroker(st, providers, tokenKey)
	if err != nil {
		// Most commonly a missing LOOM_DEPLOYMENT_ID (required so provider
		// sandboxes carry the loom-env label the reaper scopes to).
		slog.Error("placement broker disabled: construct broker", "err", err)
		return nil, nil
	}
	slog.Info("Placement broker enabled", "providers", registeredProviderNames(providers))
	return broker, providers
}

// newBroker applies the account-wide and per-provider live budgets.
func newBroker(st store.Store, providers placement.ProviderRegistry, tokenKey []byte) (*placement.Broker, error) {
	// POC operating note: one Codex auth.json (ChatGPT OAuth, including a
	// refresh token) is seeded into every lead sandbox. If OpenAI rotates the
	// refresh token, concurrent leads can invalidate each other's token. The
	// small default MaxLive (about four 2/4 leads) bounds this shared-OAuth
	// blast radius; the real fix (post-POC, ticket 08 §2) is per-lead
	// short-lived tokens. Revoke the codex + claude creds at POC close.
	if LeadBootstrapEnabled() && leadAPIBaseURL() == "" {
		slog.Warn("LOOM_LEAD_BOOTSTRAP enabled but LOOM_LEAD_API_BASE_URL unset; leads will boot the snapshot-baked binary")
	}
	return placement.NewBroker(placement.Config{
		Store: st,
		// Routing follows each node's stamped RuntimeProvider rather than
		// ambient configuration, so adding a provider is a registry entry plus
		// its credential wiring -- nothing in the broker changes.
		Providers:            providers,
		TokenKey:             tokenKey,
		LeadAPIBaseURL:       leadAPIBaseURL(),
		LeadBootstrapEnabled: LeadBootstrapEnabled(),
		MaxLive: placement.ResourceSize{
			VCPU:   leadMaxVCPU(),
			MemGiB: leadMaxMemGiB(),
		},
		// Per-provider caps so one provider cannot consume the whole account
		// budget and starve the other. Their sum should stay at or below
		// MaxLive, which still binds across all of them.
		MaxLiveByProvider: map[domain.RuntimeProvider]placement.ResourceSize{
			domain.RuntimeProviderExe: exeMaxLive(),
		},
	})
}

// exe.dev provider configuration. Token, SSH key path and host-key path are all
// required; setting none of them leaves the provider unregistered, which is the
// safe default because every lookup for an unregistered provider is refused
// fail-closed rather than falling back to Daytona.
const (
	envLoomExeToken      = "LOOM_EXE_TOKEN"        //nolint:gosec // env var name
	envLoomExeSSHKey     = "LOOM_EXE_SSH_KEY_PATH" //nolint:gosec // env var name
	envLoomExeHostKeys   = "LOOM_EXE_HOST_KEY_PATH"
	envLoomExeImage      = "LOOM_EXE_IMAGE"
	envLoomExeMaxVCPU    = "LOOM_EXE_MAX_VCPU"
	envLoomExeMaxMemGiB  = "LOOM_EXE_MAX_MEM_GIB"
	envLoomExeControlURL = "LOOM_EXE_ENDPOINT"
	// Opting in to open egress for exe leads. exe.dev cannot enforce a domain
	// allow list, so without this a provision carrying one is refused instead
	// of being silently granted unrestricted network access.
	envLoomExeAllowOpenEgress = "LOOM_EXE_ALLOW_UNRESTRICTED_EGRESS"
)

// buildExeProvider returns the exe.dev provider, or nil when it is not
// configured.
//
// Setting NONE of the three required values is "exe is off" and is silent.
// Setting SOME of them is a misconfiguration: it logs an error and still
// returns nil, because a registered-but-broken provider does not fail at
// startup -- it fails at provision time, after a placement row exists and a
// caller is waiting on it.
func buildExeProvider() *exe.Provider {
	token := strings.TrimSpace(os.Getenv(envLoomExeToken))
	keyPath := strings.TrimSpace(os.Getenv(envLoomExeSSHKey))
	hostKeyPath := strings.TrimSpace(os.Getenv(envLoomExeHostKeys))
	if token == "" && keyPath == "" && hostKeyPath == "" {
		return nil
	}
	provider, err := exe.New(exe.Config{
		Token:       token,
		Endpoint:    strings.TrimSpace(os.Getenv(envLoomExeControlURL)),
		SSHKeyPath:  keyPath,
		HostKeyPath: hostKeyPath,
		Image:       strings.TrimSpace(os.Getenv(envLoomExeImage)),

		AllowUnrestrictedEgress: strings.TrimSpace(os.Getenv(envLoomExeAllowOpenEgress)) == "1",
	})
	if err != nil {
		slog.Error("exe.dev provider not registered", "err", err)
		return nil
	}
	if strings.TrimSpace(os.Getenv(envLoomExeAllowOpenEgress)) == "1" {
		slog.Warn("exe.dev leads will run with UNRESTRICTED EGRESS (LOOM_EXE_ALLOW_UNRESTRICTED_EGRESS=1); exe.dev cannot enforce a domain allow list")
	}
	slog.Info("exe.dev provider registered")
	return provider
}

// registerExeTerminalAttach makes exe.dev leads attachable in the web terminal.
//
// It is deliberately called from the same branch that registers the provider
// with the broker. Registering one without the other produces leads that
// provision fine and then cannot be opened -- the failure surfaces in the UI,
// far from the wiring line that caused it.
func registerExeTerminalAttach(provider *exe.Provider) {
	webuiterminal.RegisterRemoteUpstreamFactory(domain.RuntimeProviderExe, exeUpstreamFactory(provider))
}

// exeUpstreamFactory adapts the exe provider's SSH/tmux attach to the terminal
// layer's upstream contract.
func exeUpstreamFactory(provider *exe.Provider) webuiterminal.RemoteUpstreamFactory {
	return func(ctx context.Context, sandboxID, ptySessionID string) (webuiterminal.PTYUpstream, error) {
		attachment, err := provider.AttachPTY(ctx, sandboxID, ptySessionID)
		if err != nil {
			// Returning the typed nil pointer directly would hand back a
			// NON-nil interface holding a nil pointer, and every downstream
			// "if upstream != nil" guard would pass before dereferencing it.
			return nil, err
		}
		return attachment, nil
	}
}

// exeMaxLive is exe.dev's slice of the live budget. It is enforced IN ADDITION
// to the account-wide MaxLive, so registering exe cannot raise total capacity
// -- MaxLive bounds the shared-OAuth blast radius, which belongs to the
// credential every lead shares rather than to any one provider.
func exeMaxLive() placement.ResourceSize {
	return placement.ResourceSize{
		VCPU:   boundedIntEnv(envLoomExeMaxVCPU, 4, 64),
		MemGiB: boundedIntEnv(envLoomExeMaxMemGiB, 8, 256),
	}
}

// reviveRegistryFor derives the revive registry from the placement registry.
//
// The two are deliberately not written out separately: a provider that can
// provision but cannot be revived produces leads that come back from a serve
// restart as permanently unattachable, which looks like a lead bug rather than
// a missing wiring line.
func reviveRegistryFor(providers placement.ProviderRegistry) leadprovision.SandboxStateProviderRegistry {
	out := make(leadprovision.SandboxStateProviderRegistry, len(providers))
	for kind, provider := range providers {
		if provider == nil {
			continue
		}
		out[kind] = provider
	}
	return out
}

// registeredProviderNames renders a registry for logging, sorted so the line
// is stable across restarts.
func registeredProviderNames(providers placement.ProviderRegistry) []string {
	names := make([]string, 0, len(providers))
	for kind := range providers {
		names = append(names, string(kind))
	}
	sort.Strings(names)
	return names
}

// LeadProvisioner builds the lead provisioner, or nil when sandbox placement is
// disabled. snapshotRef comes from serve because the same value also drives
// non-placement paths there.
func LeadProvisioner(st store.Store, broker *placement.Broker, snapshotRef string) *leadprovision.Provisioner {
	if st == nil || broker == nil {
		return nil
	}
	return leadprovision.New(
		broker,
		st,
		bootstrap.LoomDir(),
		leadAllowlist(),
		snapshotRef,
		leadprovision.DefaultResource(),
	)
}

// buildLeadReviveCoordinator routes a revive to the platform that owns the
// sandbox id. It is built FROM the placement registry rather than restating it,
// so a provider cannot be provisionable but not revivable.
func LeadReviveCoordinator(providers placement.ProviderRegistry, provisioner *leadprovision.Provisioner) *leadprovision.ReviveCoordinator {
	return leadprovision.NewReviveCoordinator(reviveRegistryFor(providers), provisioner)
}

// leadAllowlist resolves the network domain allowlist for lead sandboxes.
// Daytona applies the allowlist at create time only, so a change here affects
// only newly provisioned sandboxes -- never a lead already running.
func leadAllowlist() []string {
	raw := strings.TrimSpace(os.Getenv(envLoomLeadAllowlist))
	if raw == "" {
		return leadprovision.DefaultAllowlist()
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return leadprovision.DefaultAllowlist()
	}
	return out
}

// LeadBootstrapEnabled gates BOTH the serve route that streams the loom binary
// and the provider's download-at-boot step. They read one function on purpose:
// a lead told to download from a route that is not registered boots into a 404.
func LeadBootstrapEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(envLoomLeadBootstrap))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}

func leadAPIBaseURL() string {
	return strings.TrimSpace(os.Getenv(envLoomLeadAPIBaseURL))
}

func leadMaxVCPU() int {
	return boundedIntEnv(envLoomLeadMaxVCPU, 8, 64)
}

func leadMaxMemGiB() int {
	return boundedIntEnv(envLoomLeadMaxMemGiB, 16, 128)
}

// boundedIntEnv is a private copy of serve's helper. Duplicating ~15 lines is
// the cheaper coupling: the alternative is exporting it and making this
// package depend on serve, which is the direction the dependency must not run.
func boundedIntEnv(name string, def, max int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < 1 {
		return 1
	}
	if n > max {
		return max
	}
	return n
}
