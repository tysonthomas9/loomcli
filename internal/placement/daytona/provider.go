// Package daytona adapts Daytona sandboxes to placement.Provider.
//
// Daytona's toolbox PTY API cannot run placement.ProcessSpec.Command literally:
// the create payload only has cols, rows, cwd, envs, id, and lazyStart. Lead
// boot therefore depends on the loom-lead-poc-v2 snapshot's profile hook. This
// adapter maps ProcessSpec onto that hook only for the lead PTY session
// (placement.LeadPTYSessionID) by setting LOOM_LEAD_BOOT in the per-PTY
// environment, plus LOOM_LEAD_WORKDIR and LOOM_LEAD_PROMPT_FILE when available.
// Caller-supplied hook keys are stripped at both the sandbox level and the PTY
// level, so this adapter is the sole authority on hook activation. That makes a
// duplicate lead impossible per sandbox through this adapter; the
// workspace-level single-orchestrator invariant remains the broker's admission
// mutex responsibility. Arbitrary ProcessSpec.Command values are intentionally
// not sent to Daytona because there is no command field to send.
package daytona

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	apiclient "github.com/daytonaio/daytona/libs/api-client-go"
	sdkerrors "github.com/daytonaio/daytona/libs/sdk-go/pkg/errors"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/netbase"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

const (
	// APIKeyEnv is the provider credential source. It must never be copied into
	// sandbox or PTY environments.
	APIKeyEnv = "DAYTONA_API_KEY" //nolint:gosec // env var name, not a credential

	defaultAPIURL                     = "https://app.daytona.io/api"
	DefaultSnapshotName               = "loom-lead-poc-v2"
	DefaultSnapshotID                 = "ae429d20-520a-401f-9a6d-b87b89d75939"
	defaultHTTPTimeout                = 60 * time.Second
	defaultLeadBootPrepTimeout        = 5 * time.Minute
	defaultCreateAutoStop             = 15 * time.Minute
	defaultVCPU                       = 1
	defaultMemGiB                     = 2
	defaultPtyCols                    = 120
	defaultPtyRows                    = 40
	leadBootEnv                       = "LOOM_LEAD_BOOT"
	leadWorkdirEnv                    = "LOOM_LEAD_WORKDIR"
	leadPromptFileEnv                 = "LOOM_LEAD_PROMPT_FILE"
	daytonaOrganizationIDEnv          = "DAYTONA_ORGANIZATION_ID"
	daytonaAPIURLEnv                  = "DAYTONA_API_URL"
	daytonaTargetEnv                  = "DAYTONA_TARGET"
	maxDomainAllowlistEntries         = 20
	maxErrorBodyBytes           int64 = 1 << 20
	maxInt32Value               int64 = 1<<31 - 1
	maxPrepDiagnosticBytes            = 4096
	leadCheckoutPresentMarker         = "git"
	leadCheckoutAbsentMarker          = "absent"
	leadCheckoutInvalidExitCode       = 42

	// deleteConflictAttempts bounds retries of the transient
	// "state change in progress" 409. Bounded rather than unbounded because a
	// caller holding a placement in `releasing` will re-drive the delete
	// later; blocking here would hold the per-agent lock instead.
	deleteConflictAttempts = 4
	deleteConflictBackoff  = 500 * time.Millisecond
)

var (
	basicAuthHeaderRe = regexp.MustCompile(`AUTHORIZATION: basic [A-Za-z0-9+/=]+`)
	xAccessTokenURLRe = regexp.MustCompile(`x-access-token:[^@\s]+@`)
)

// Config contains Daytona provider configuration.
type Config struct {
	// APIURL defaults to https://app.daytona.io/api.
	APIURL string
	// OrganizationID is optional and defaults to DAYTONA_ORGANIZATION_ID.
	OrganizationID string
	// Target is optional and defaults to DAYTONA_TARGET.
	Target string
	// HTTPClient is optional. When nil, New uses internal/netbase.Transport.
	HTTPClient *http.Client
	// HTTPTimeout is used on the default HTTP client and as the per-call
	// fallback when the caller supplies an unbounded context.
	HTTPTimeout time.Duration
	// SnapshotName maps SnapshotID to Daytona's create-time snapshot lookup key.
	SnapshotName string
	// SnapshotID is the stable placement record pin for the default snapshot.
	SnapshotID string
	// CreateAutoStopInterval is the provider-side backstop applied at create.
	// Zero uses the default; a negative duration omits the field.
	CreateAutoStopInterval time.Duration
	// CreateAutoDeleteInterval is omitted by default so the broker remains the
	// authority for release. Set a positive duration to add a provider backstop.
	CreateAutoDeleteInterval time.Duration
}

// Provider implements placement.Provider using Daytona.
type Provider struct {
	apiClient                *apiclient.APIClient
	httpClient               *http.Client
	prepHTTPClient           *http.Client
	apiKey                   string
	organizationID           string
	target                   string
	callTimeout              time.Duration
	snapshotName             string
	snapshotID               string
	createAutoStopInterval   time.Duration
	createAutoDeleteInterval time.Duration
}

var _ placement.Provider = (*Provider)(nil)

// NewFromEnv constructs a Provider using Daytona environment variables.
func NewFromEnv() (*Provider, error) {
	return New(Config{})
}

// New constructs a Provider. It does not validate the key against Daytona or
// perform any billable operation.
func New(cfg Config) (*Provider, error) {
	apiKey := strings.TrimSpace(os.Getenv(APIKeyEnv))
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%s is required", APIKeyEnv)
	}
	apiURL, err := normalizeAPIURL(firstNonEmpty(cfg.APIURL, os.Getenv(daytonaAPIURLEnv), defaultAPIURL))
	if err != nil {
		return nil, err
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   timeout,
			Transport: netbase.Transport(),
		}
	}

	apiCfg := apiclient.NewConfiguration()
	apiCfg.Servers = apiclient.ServerConfigurations{{URL: apiURL}}
	apiCfg.HTTPClient = httpClient
	apiCfg.UserAgent = "loomcli-daytona-placement"
	apiCfg.AddDefaultHeader("X-Daytona-Source", "loomcli")

	autoStop := cfg.CreateAutoStopInterval
	if autoStop == 0 {
		autoStop = defaultCreateAutoStop
	}

	snapshotName := firstNonEmpty(cfg.SnapshotName, DefaultSnapshotName)
	snapshotID := firstNonEmpty(cfg.SnapshotID, DefaultSnapshotID)

	return &Provider{
		apiClient:                apiclient.NewAPIClient(apiCfg),
		httpClient:               httpClient,
		prepHTTPClient:           prepHTTPClient(httpClient),
		apiKey:                   apiKey,
		organizationID:           firstNonEmpty(cfg.OrganizationID, os.Getenv(daytonaOrganizationIDEnv)),
		target:                   firstNonEmpty(cfg.Target, os.Getenv(daytonaTargetEnv)),
		callTimeout:              timeout,
		snapshotName:             snapshotName,
		snapshotID:               snapshotID,
		createAutoStopInterval:   autoStop,
		createAutoDeleteInterval: cfg.CreateAutoDeleteInterval,
	}, nil
}

// prepHTTPClient carries no client-level timeout: prep calls are bounded by
// the caller's context deadline (the broker's prep budget), and a fixed client
// timeout would silently cap any budget raised above it.
func prepHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{Transport: netbase.Transport()}
	}
	prep := *base
	prep.Timeout = 0
	return &prep
}

// Create creates a Daytona sandbox from a snapshot. Labels, resource sizing,
// environment, and domain allowlist are sent in this create call because Daytona
// cannot safely apply them afterwards.
func (p *Provider) Create(ctx context.Context, req placement.CreateRequest) (placement.CreateResult, error) {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	payload, err := p.createPayload(req)
	if err != nil {
		return placement.CreateResult{}, err
	}

	apiReq := p.apiClient.SandboxAPI.CreateSandbox(p.authContext(ctx)).CreateSandbox(*payload)
	if p.organizationID != "" {
		apiReq = apiReq.XDaytonaOrganizationID(p.organizationID)
	}
	sandbox, httpResp, err := apiReq.Execute()
	if err != nil {
		return placement.CreateResult{}, fmt.Errorf("daytona create sandbox: %w", convertAPIError(err, httpResp))
	}

	result := placement.CreateResult{SandboxID: strings.TrimSpace(sandbox.GetId())}
	if result.SandboxID == "" {
		return result, fmt.Errorf("daytona create sandbox returned empty id")
	}
	if err := p.waitForStarted(ctx, sandbox); err != nil {
		return result, err
	}
	return result, nil
}

// Get performs a Daytona point read. It never falls back to list filtering.
func (p *Provider) Get(ctx context.Context, sandboxID string) (placement.ProviderSandbox, error) {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	sandbox, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return placement.ProviderSandbox{}, err
	}
	out := providerSandboxFromAPI(sandbox)
	if out.State == placement.ProviderSandboxAbsent {
		return placement.ProviderSandbox{}, fmt.Errorf("%w: daytona sandbox %q state %q", placement.ErrSandboxNotFound, sandboxID, sandboxState(sandbox))
	}
	return out, nil
}

// ListManaged lists Daytona sandboxes with the requested labels. Daytona's list
// API is eventually consistent, so this method reports exactly the listed state
// and leaves point-read confirmation to the broker.
func (p *Provider) ListManaged(ctx context.Context, labels map[string]string) ([]placement.ProviderSandbox, error) {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	labels = cleanMap(labels)
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return nil, fmt.Errorf("encode daytona labels filter: %w", err)
	}

	var out []placement.ProviderSandbox
	cursor := ""
	for {
		req := p.apiClient.SandboxAPI.ListSandboxes(p.authContext(ctx)).
			IncludeErroredDeleted(true).
			Limit(100).
			Labels(string(labelsJSON))
		if cursor != "" {
			req = req.Cursor(cursor)
		}
		if p.organizationID != "" {
			req = req.XDaytonaOrganizationID(p.organizationID)
		}
		resp, httpResp, err := req.Execute()
		if err != nil {
			return nil, fmt.Errorf("daytona list sandboxes: %w", convertAPIError(err, httpResp))
		}
		for _, item := range resp.GetItems() {
			sandbox := providerSandboxFromListItem(item)
			if providerSandboxHasLabels(sandbox, labels) {
				out = append(out, sandbox)
			}
		}
		next := strings.TrimSpace(resp.GetNextCursor())
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

// Delete deletes a Daytona sandbox. A provider-confirmed not-found is mapped to
// placement.ErrSandboxNotFound so the broker can treat already-gone sandboxes as
// a confirmed deletion.
// Delete asks Daytona to destroy the sandbox. Deletion is ASYNCHRONOUS: a
// successful return means the request was accepted, not that the sandbox is
// gone -- an immediate Get still succeeds. Callers must confirm separately.
//
// A delete issued while another state change is in flight returns
// "Conflict: Sandbox state change in progress", which is transient. It is
// retried here rather than surfaced, because a caller that treats it as a
// failure leaves a paid sandbox behind. It must never be mapped to
// ErrSandboxNotFound -- that would falsely confirm a deletion that has not
// happened.
func (p *Provider) Delete(ctx context.Context, sandboxID string) error {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	var lastErr error
	for attempt := range deleteConflictAttempts {
		apiReq := p.apiClient.SandboxAPI.DeleteSandbox(p.authContext(ctx), strings.TrimSpace(sandboxID))
		if p.organizationID != "" {
			apiReq = apiReq.XDaytonaOrganizationID(p.organizationID)
		}
		_, httpResp, err := apiReq.Execute()
		if err == nil {
			return nil
		}
		mapped := p.mapSandboxOperationError("delete", sandboxID, convertAPIError(err, httpResp))
		if !isStateChangeInProgress(mapped) {
			return mapped
		}
		lastErr = mapped
		if attempt == deleteConflictAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("daytona delete sandbox %q: %w", sandboxID, ctx.Err())
		case <-time.After(deleteConflictBackoff << attempt):
		}
	}
	return lastErr
}

// isStateChangeInProgress reports the transient 409 Daytona returns while a
// sandbox is mid-transition.
func isStateChangeInProgress(err error) bool {
	var conflict *sdkerrors.DaytonaConflictError
	if !errors.As(err, &conflict) {
		return false
	}
	return strings.Contains(strings.ToLower(conflict.Error()), "state change in progress")
}

// UpdateLastActivity refreshes Daytona's activity timestamp for a sandbox.
func (p *Provider) UpdateLastActivity(ctx context.Context, sandboxID string) error {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	apiReq := p.apiClient.SandboxAPI.UpdateLastActivity(p.authContext(ctx), strings.TrimSpace(sandboxID))
	if p.organizationID != "" {
		apiReq = apiReq.XDaytonaOrganizationID(p.organizationID)
	}
	httpResp, err := apiReq.Execute()
	if err != nil {
		return p.mapSandboxOperationError("update last activity", sandboxID, convertAPIError(err, httpResp))
	}
	return nil
}

// SetAutostopInterval sets Daytona's auto-stop interval. The API accepts
// minutes; sub-minute positive durations round up rather than disabling auto-stop.
func (p *Provider) SetAutostopInterval(ctx context.Context, sandboxID string, interval time.Duration) error {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	apiReq := p.apiClient.SandboxAPI.SetAutostopInterval(p.authContext(ctx), strings.TrimSpace(sandboxID), durationMinutes(interval))
	if p.organizationID != "" {
		apiReq = apiReq.XDaytonaOrganizationID(p.organizationID)
	}
	_, httpResp, err := apiReq.Execute()
	if err != nil {
		return p.mapSandboxOperationError("set autostop interval", sandboxID, convertAPIError(err, httpResp))
	}
	return nil
}

// PrepareLeadBoot prepares the checkout and prompt file before the lead PTY is
// created. It intentionally exposes no generic exec or upload primitive.
func (p *Provider) PrepareLeadBoot(ctx context.Context, sandboxID string, prep placement.LeadBootPrep) error {
	timeout := prep.Timeout
	if timeout <= 0 {
		timeout = defaultLeadBootPrepTimeout
	}
	ctx, cancel := withTimeoutIfNone(ctx, timeout)
	defer cancel()

	if prep.Repo != nil {
		if _, _, err := placement.NormalizeRepoCloneRemote(prep.Repo.RemoteURL); err != nil {
			return err
		}
	}
	sandbox, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	if prep.Repo != nil {
		if err := p.prepareLeadCheckout(ctx, sandbox, prep); err != nil {
			return err
		}
	}
	if prep.PromptText != "" {
		if err := p.writeLeadPrompt(ctx, sandbox, prep.PromptPath, prep.PromptText); err != nil {
			return err
		}
	}
	return nil
}

// CreatePty creates the requested PTY session if absent. ProcessSpec.Command is
// mapped to the snapshot hook's environment contract; Daytona has no PTY command
// field and therefore cannot honor arbitrary commands literally.
func (p *Provider) CreatePty(ctx context.Context, sandboxID string, spec placement.ProcessSpec) error {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	if strings.TrimSpace(spec.SessionID) == "" {
		return fmt.Errorf("daytona pty session id required")
	}

	sandbox, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	payload := ptyCreatePayload(spec)
	err = p.doToolbox(ctx, sandbox, http.MethodPost, "/process/pty", payload, nil)
	if err == nil {
		return nil
	}
	if isPtyAlreadyExistsError(err, spec.SessionID) {
		return fmt.Errorf("%w: %w", placement.ErrPtySessionAlreadyExists, err)
	}
	if isDaytonaNotFound(err) {
		return fmt.Errorf("%w: %w", placement.ErrSandboxNotFound, err)
	}
	return fmt.Errorf("daytona create pty %q in sandbox %q: %w", spec.SessionID, sandboxID, err)
}

// ListPtySessions lists active toolbox PTY sessions for the sandbox.
func (p *Provider) ListPtySessions(ctx context.Context, sandboxID string) ([]placement.PtySession, error) {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	sandbox, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	var resp ptyListResponse
	if err := p.doToolbox(ctx, sandbox, http.MethodGet, "/process/pty", nil, &resp); err != nil {
		if isDaytonaNotFound(err) {
			return nil, fmt.Errorf("%w: %w", placement.ErrSandboxNotFound, err)
		}
		return nil, fmt.Errorf("daytona list pty sessions in sandbox %q: %w", sandboxID, err)
	}
	out := make([]placement.PtySession, 0, len(resp.Sessions))
	for _, session := range resp.Sessions {
		out = append(out, placement.PtySession{SessionID: session.ID})
	}
	return out, nil
}

// KillPtySession terminates a toolbox PTY session.
func (p *Provider) KillPtySession(ctx context.Context, sandboxID string, sessionID string) error {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	sandbox, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	path := "/process/pty/" + url.PathEscape(strings.TrimSpace(sessionID))
	if err := p.doToolbox(ctx, sandbox, http.MethodDelete, path, nil, nil); err != nil {
		if isDaytonaNotFound(err) {
			return fmt.Errorf("%w: %w", placement.ErrSandboxNotFound, err)
		}
		return fmt.Errorf("daytona kill pty %q in sandbox %q: %w", sessionID, sandboxID, err)
	}
	return nil
}

func (p *Provider) prepareLeadCheckout(ctx context.Context, sandbox *apiclient.Sandbox, prep placement.LeadBootPrep) error {
	repo := prep.Repo
	checkout := strings.TrimSpace(repo.Checkout)
	if checkout == "" || !strings.HasPrefix(checkout, "/") {
		return fmt.Errorf("lead checkout path must be absolute: %w", domain.ErrInvalid)
	}
	remoteURL, host, err := placement.NormalizeRepoCloneRemote(repo.RemoteURL)
	if err != nil {
		return err
	}

	state, err := p.leadCheckoutState(ctx, sandbox, checkout)
	if err != nil {
		return err
	}
	if state == leadCheckoutPresentMarker {
		return nil
	}
	token, encoded, err := resolveLeadGitToken(prep.GitToken)
	if err != nil {
		return err
	}
	// POC: a shallow single-branch clone is enough for lead boot. The lead
	// holds no git credential (ticket 12), so it cannot deepen or fetch later;
	// full clone is the post-POC upgrade and needs async exec. Do not use
	// --filter=blob:none because lazy blob fetches would need mid-session auth.
	cloneCmd := leadCloneCommand(remoteURL, host, strings.TrimSpace(repo.Ref), checkout, encoded)
	if _, err := p.execLeadPrep(ctx, sandbox, cloneCmd, token, encoded); err != nil {
		return err
	}
	return p.assertLeadRemoteURL(ctx, sandbox, checkout, remoteURL, token, encoded)
}

func (p *Provider) leadCheckoutState(ctx context.Context, sandbox *apiclient.Sandbox, checkout string) (string, error) {
	// An existing but EMPTY directory counts as absent: git clone into an
	// empty directory succeeds, and the revive path may find one left by an
	// interrupted earlier prep.
	quoted := shellQuote(checkout)
	cmd := "if [ -e " + quoted + " ]; then " +
		"if git -C " + quoted + " rev-parse --is-inside-work-tree >/dev/null 2>&1; then " +
		"printf %s " + shellQuote(leadCheckoutPresentMarker) + "; " +
		"elif [ -z \"$(ls -A " + quoted + " 2>/dev/null)\" ]; then " +
		"printf %s " + shellQuote(leadCheckoutAbsentMarker) + "; " +
		"else exit 42; fi; " +
		"else printf %s " + shellQuote(leadCheckoutAbsentMarker) + "; fi"
	result, err := p.execLeadPrep(ctx, sandbox, cmd)
	if err != nil {
		var execErr *leadPrepExecError
		if errors.As(err, &execErr) && execErr.exitCode == leadCheckoutInvalidExitCode {
			return "", fmt.Errorf("lead checkout path %q exists but is not a git work tree: %w", checkout, domain.ErrInvalid)
		}
		return "", err
	}
	state := strings.TrimSpace(result.outputText())
	switch state {
	case leadCheckoutPresentMarker, leadCheckoutAbsentMarker:
		return state, nil
	default:
		return "", fmt.Errorf("lead checkout state probe returned %q: %w", state, domain.ErrInvalid)
	}
}

func resolveLeadGitToken(callback func() (string, error)) (token string, encoded string, err error) {
	if callback == nil {
		return "", "", nil
	}
	token, err = callback()
	if err != nil {
		return "", "", fmt.Errorf("resolve git token for lead boot: credential callback failed")
	}
	if token == "" {
		return "", "", nil
	}
	encoded = base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return token, encoded, nil
}

func leadCloneCommand(remoteURL, host, ref, checkout, encodedToken string) string {
	parts := []string{"git"}
	if encodedToken != "" {
		key := "http.https://" + host + "/.extraheader=AUTHORIZATION: basic " + encodedToken
		parts = append(parts, "-c", shellQuote(key))
	}
	parts = append(parts, "clone", "--depth", "1", "--single-branch")
	if ref != "" {
		parts = append(parts, "--branch", shellQuote(ref))
	}
	// The clone stages into a sibling .partial path and renames into place on
	// success, so a clone killed mid-transfer (context budget, network drop)
	// leaves NOTHING at the checkout path -- a partial directory there would
	// wedge every future resume as "exists but is not a git work tree". The
	// rm -rf targets only the staging path, never the checkout (which may
	// hold the lead's work).
	partial := checkout + ".partial"
	parts = append(parts, shellQuote(remoteURL), shellQuote(partial))
	return "rm -rf " + shellQuote(partial) +
		" && " + strings.Join(parts, " ") +
		" && mv " + shellQuote(partial) + " " + shellQuote(checkout)
}

func (p *Provider) assertLeadRemoteURL(
	ctx context.Context,
	sandbox *apiclient.Sandbox,
	checkout string,
	want string,
	redactions ...string,
) error {
	cmd := "git -C " + shellQuote(checkout) + " config --get remote.origin.url"
	result, err := p.execLeadPrep(ctx, sandbox, cmd, redactions...)
	if err != nil {
		return err
	}
	got := strings.TrimSpace(result.outputText())
	if strings.Contains(got, "@") || strings.Contains(strings.ToLower(got), "x-access-token") {
		return fmt.Errorf("lead clone persisted a credential-bearing remote URL")
	}
	if got != want {
		return fmt.Errorf("lead clone remote URL = %q, want %q", got, want)
	}
	return nil
}

func (p *Provider) writeLeadPrompt(ctx context.Context, sandbox *apiclient.Sandbox, promptPath, promptText string) error {
	promptPath = strings.TrimSpace(promptPath)
	if promptPath == "" || !strings.HasPrefix(promptPath, "/") {
		return fmt.Errorf("lead prompt path must be absolute when prompt text is provided: %w", domain.ErrInvalid)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(promptText))
	// Write-then-rename: `>` truncates before base64 writes, and a lead booted
	// by a previous Provision may be inside its one startup read of this file.
	// mv within one filesystem is atomic, so a reader sees the old prompt or
	// the new one, never a partial.
	cmd := "mkdir -p " + shellQuote(path.Dir(promptPath)) +
		" && printf %s " + shellQuote(encoded) + " | base64 -d > " + shellQuote(promptPath+".tmp") +
		" && mv -f " + shellQuote(promptPath+".tmp") + " " + shellQuote(promptPath)
	_, err := p.execLeadPrep(ctx, sandbox, cmd)
	return err
}

func (p *Provider) execLeadPrep(
	ctx context.Context,
	sandbox *apiclient.Sandbox,
	command string,
	redactions ...string,
) (toolboxExecuteResponse, error) {
	var out toolboxExecuteResponse
	req := toolboxExecuteRequest{
		Command: command,
		// The toolbox default is 10 seconds -- far too short for a clone --
		// so the timeout is always sent explicitly, derived from the caller's
		// context deadline (the broker's prep budget).
		Timeout: leadPrepExecTimeoutSeconds(ctx),
	}
	err := p.doToolboxWithClient(ctx, p.prepClient(), sandbox, http.MethodPost, "/process/execute", req, &out)
	if err != nil {
		return out, classifyLeadPrepTransportError(err)
	}
	if out.ExitCode == nil {
		return out, fmt.Errorf("daytona lead prep exec returned no exitCode")
	}
	if *out.ExitCode != 0 {
		return out, &leadPrepExecError{
			exitCode: *out.ExitCode,
			message:  prepDiagnostic(out, command, redactions...),
		}
	}
	return out, nil
}

// leadPrepExecTimeoutSeconds converts the context's remaining budget into the
// toolbox exec timeout field, so the sandbox-side command never outlives the
// caller. Floor of 1 second; the provider default when the context is
// unbounded.
func leadPrepExecTimeoutSeconds(ctx context.Context) int {
	deadline, ok := ctx.Deadline()
	if !ok {
		return int(defaultLeadBootPrepTimeout / time.Second)
	}
	remaining := int(time.Until(deadline) / time.Second)
	if remaining < 1 {
		return 1
	}
	return remaining
}

// classifyLeadPrepTransportError preserves error classification (the
// not-found sentinel and the HTTP status) while discarding response bodies,
// which can echo the credential-bearing exec command.
func classifyLeadPrepTransportError(err error) error {
	if isDaytonaNotFound(err) {
		return fmt.Errorf("daytona lead prep exec: %w", placement.ErrSandboxNotFound)
	}
	if status, _, ok := daytonaStatusAndMessage(err); ok && status != 0 {
		return fmt.Errorf("daytona lead prep exec request failed: http status %d", status)
	}
	return fmt.Errorf("daytona lead prep exec request failed")
}

type toolboxExecuteRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type toolboxExecuteResponse struct {
	ExitCode *int   `json:"exitCode"`
	Result   string `json:"result"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Error    string `json:"error"`
}

func (r toolboxExecuteResponse) outputText() string {
	for _, value := range []string{r.Result, r.Stdout} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type leadPrepExecError struct {
	exitCode int
	message  string
}

func (e *leadPrepExecError) Error() string {
	if strings.TrimSpace(e.message) == "" {
		return fmt.Sprintf("daytona lead prep exec exit code %d", e.exitCode)
	}
	return fmt.Sprintf("daytona lead prep exec exit code %d: %s", e.exitCode, e.message)
}

func prepDiagnostic(out toolboxExecuteResponse, command string, redactions ...string) string {
	text := strings.Join([]string{out.Stderr, out.Error, out.Result, out.Stdout}, "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, command, "[redacted command]")
	for _, secret := range redactions {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	text = basicAuthHeaderRe.ReplaceAllString(text, "AUTHORIZATION: basic [redacted]")
	text = xAccessTokenURLRe.ReplaceAllString(text, "x-access-token:***@")
	if len(text) > maxPrepDiagnosticBytes {
		text = text[:maxPrepDiagnosticBytes] + "..."
	}
	return text
}

func (p *Provider) createPayload(req placement.CreateRequest) (*apiclient.CreateSandbox, error) {
	payload := apiclient.NewCreateSandbox()
	payload.SetSnapshot(p.snapshotLookupName(req.SnapshotRef))
	payload.SetLabels(cleanMap(req.Labels))
	payload.SetEnv(sandboxEnv(req.Env))
	if p.target != "" {
		payload.SetTarget(p.target)
	}

	// Resources are deliberately not sent. Daytona rejects a create that
	// carries both a snapshot and explicit sizing:
	//
	//	Validation error: Cannot specify Sandbox resources when using a snapshot
	//
	// and every lead placement provisions from a snapshot, so setting them
	// fails 100% of the time. Size therefore comes from the snapshot --
	// loom-lead-poc-v2 is 2 vCPU / 4 GiB -- and req.Resource cannot override
	// it. Callers that need the real numbers for quota accounting must read
	// them from the snapshot via SnapshotResources; reserving a requested
	// size here would let the broker admit against capacity the provider has
	// already allocated. At 4 GiB a lead, a ~10 GiB tier pool holds two.

	if p.createAutoStopInterval >= 0 {
		payload.SetAutoStopInterval(int32(math.Ceil(p.createAutoStopInterval.Minutes())))
	}
	if p.createAutoDeleteInterval > 0 {
		payload.SetAutoDeleteInterval(int32(math.Ceil(p.createAutoDeleteInterval.Minutes())))
	}

	allowlist, err := domainAllowlist(req.NetworkDomainAllowlist)
	if err != nil {
		return nil, err
	}
	if allowlist != "" {
		payload.SetDomainAllowList(allowlist)
	}
	return payload, nil
}

func (p *Provider) snapshotLookupName(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == p.snapshotID {
		return p.snapshotName
	}
	return ref
}

func (p *Provider) getSandbox(ctx context.Context, sandboxID string) (*apiclient.Sandbox, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return nil, fmt.Errorf("daytona sandbox id required")
	}
	apiReq := p.apiClient.SandboxAPI.GetSandbox(p.authContext(ctx), sandboxID).Verbose(true)
	if p.organizationID != "" {
		apiReq = apiReq.XDaytonaOrganizationID(p.organizationID)
	}
	sandbox, httpResp, err := apiReq.Execute()
	if err != nil {
		return nil, p.mapSandboxOperationError("get", sandboxID, convertAPIError(err, httpResp))
	}
	if sandboxState(sandbox) == apiclient.SANDBOXSTATE_DESTROYED {
		return nil, fmt.Errorf("%w: daytona sandbox %q state %q", placement.ErrSandboxNotFound, sandboxID, sandboxState(sandbox))
	}
	return sandbox, nil
}

func (p *Provider) waitForStarted(ctx context.Context, sandbox *apiclient.Sandbox) error {
	state := sandboxState(sandbox)
	id := strings.TrimSpace(sandbox.GetId())
	for {
		switch state {
		case apiclient.SANDBOXSTATE_STARTED:
			return nil
		case apiclient.SANDBOXSTATE_ERROR, apiclient.SANDBOXSTATE_BUILD_FAILED:
			return fmt.Errorf("daytona sandbox %q failed to start with state %q: %s", id, state, strings.TrimSpace(sandbox.GetErrorReason()))
		}

		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for daytona sandbox %q to start: %w", id, ctx.Err())
		case <-timer.C:
		}

		next, err := p.getSandbox(ctx, id)
		if err != nil {
			return fmt.Errorf("wait for daytona sandbox %q to start: %w", id, err)
		}
		sandbox = next
		state = sandboxState(next)
	}
}

func (p *Provider) doToolbox(ctx context.Context, sandbox *apiclient.Sandbox, method, path string, in any, out any) error {
	return p.doToolboxWithClient(ctx, p.httpClient, sandbox, method, path, in, out)
}

func (p *Provider) doToolboxWithClient(
	ctx context.Context,
	client *http.Client,
	sandbox *apiclient.Sandbox,
	method string,
	path string,
	in any,
	out any,
) error {
	baseURL := strings.TrimRight(strings.TrimSpace(sandbox.GetToolboxProxyUrl()), "/")
	sandboxID := strings.TrimSpace(sandbox.GetId())
	if baseURL == "" || sandboxID == "" {
		return fmt.Errorf("daytona sandbox %q has no toolbox proxy url", sandboxID)
	}
	req, err := p.newToolboxRequest(ctx, method, baseURL+"/"+sandboxID+path, in)
	if err != nil {
		return err
	}

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return sdkerrors.NewDaytonaError(err.Error(), 0, nil)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return daytonaHTTPError(resp.StatusCode, resp.Header, respBody, resp.Status)
	}
	if out == nil || len(bytes.TrimSpace(respBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode daytona toolbox response: %w", err)
	}
	return nil
}

func (p *Provider) newToolboxRequest(ctx context.Context, method string, url string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("encode daytona toolbox request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (p *Provider) prepClient() *http.Client {
	if p.prepHTTPClient != nil {
		return p.prepHTTPClient
	}
	return prepHTTPClient(p.httpClient)
}

func (p *Provider) authContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, apiclient.ContextAccessToken, p.apiKey)
}

func (p *Provider) withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return withTimeoutIfNone(ctx, p.callTimeout)
}

func withTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (p *Provider) mapSandboxOperationError(operation, sandboxID string, err error) error {
	if isDaytonaNotFound(err) {
		return fmt.Errorf("%w: %w", placement.ErrSandboxNotFound, err)
	}
	return fmt.Errorf("daytona %s sandbox %q: %w", operation, sandboxID, err)
}

type ptyCreateRequest struct {
	Cols      int32             `json:"cols"`
	Cwd       string            `json:"cwd,omitempty"`
	Envs      map[string]string `json:"envs"`
	ID        string            `json:"id"`
	LazyStart bool              `json:"lazyStart"`
	Rows      int32             `json:"rows"`
}

type ptyListResponse struct {
	Sessions []ptySessionInfo `json:"sessions"`
}

type ptySessionInfo struct {
	ID string `json:"id"`
}

func ptyCreatePayload(spec placement.ProcessSpec) ptyCreateRequest {
	env := sandboxEnv(spec.Env)
	isLead := strings.TrimSpace(spec.SessionID) == placement.LeadPTYSessionID
	if isLead {
		env[leadBootEnv] = "1"
		if workdir := strings.TrimSpace(spec.WorkingDir); workdir != "" {
			env[leadWorkdirEnv] = workdir
		}
		if prompt := promptFileFromCommand(spec.Command); prompt != "" {
			env[leadPromptFileEnv] = prompt
		}
	}
	return ptyCreateRequest{
		Cols:      defaultPtyCols,
		Cwd:       strings.TrimSpace(spec.WorkingDir),
		Envs:      env,
		ID:        strings.TrimSpace(spec.SessionID),
		LazyStart: false,
		Rows:      defaultPtyRows,
	}
}

func promptFileFromCommand(command []string) string {
	for i, arg := range command {
		arg = strings.TrimSpace(arg)
		if arg == "--prompt" && i+1 < len(command) {
			return strings.TrimSpace(command[i+1])
		}
		if value, ok := strings.CutPrefix(arg, "--prompt="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sandboxEnv(in map[string]string) map[string]string {
	out := cleanMap(in)
	delete(out, APIKeyEnv)
	delete(out, leadBootEnv)
	delete(out, leadWorkdirEnv)
	delete(out, leadPromptFileEnv)
	return out
}

func normalizedResource(resource placement.ResourceSize) placement.ResourceSize {
	if resource.VCPU <= 0 {
		resource.VCPU = defaultVCPU
	}
	if resource.MemGiB <= 0 {
		resource.MemGiB = defaultMemGiB
	}
	return resource
}

func checkedInt32(name string, value int) (int32, error) {
	if int64(value) > maxInt32Value {
		return 0, fmt.Errorf("daytona %s value %d exceeds int32 max", name, value)
	}
	return int32(value), nil //nolint:gosec // value is checked against maxInt32Value above
}

func domainAllowlist(in []string) (string, error) {
	out := make([]string, 0, len(in))
	for _, entry := range in {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return "", nil
	}
	if len(out) > maxDomainAllowlistEntries {
		return "", fmt.Errorf("daytona domain allowlist has %d entries, max %d", len(out), maxDomainAllowlistEntries)
	}
	return strings.Join(out, ","), nil
}

func providerSandboxFromAPI(sandbox *apiclient.Sandbox) placement.ProviderSandbox {
	return placement.ProviderSandbox{
		ID:     strings.TrimSpace(sandbox.GetId()),
		Labels: cleanMap(sandbox.GetLabels()),
		State:  providerState(sandboxState(sandbox)),
	}
}

func providerSandboxFromListItem(item apiclient.SandboxListItem) placement.ProviderSandbox {
	return placement.ProviderSandbox{
		ID:     strings.TrimSpace(item.GetId()),
		Labels: cleanMap(item.GetLabels()),
		State:  providerState(item.GetState()),
	}
}

func providerState(state apiclient.SandboxState) placement.ProviderSandboxState {
	switch state {
	case apiclient.SANDBOXSTATE_DESTROYED:
		return placement.ProviderSandboxAbsent
	case apiclient.SANDBOXSTATE_STOPPED, apiclient.SANDBOXSTATE_ARCHIVED, apiclient.SANDBOXSTATE_PAUSED:
		return placement.ProviderSandboxStopped
	default:
		return placement.ProviderSandboxRunning
	}
}

func sandboxState(sandbox *apiclient.Sandbox) apiclient.SandboxState {
	if sandbox == nil {
		return apiclient.SANDBOXSTATE_UNKNOWN
	}
	return sandbox.GetState()
}

func providerSandboxHasLabels(sandbox placement.ProviderSandbox, labels map[string]string) bool {
	for key, value := range labels {
		if sandbox.Labels[key] != value {
			return false
		}
	}
	return true
}

func durationMinutes(interval time.Duration) float32 {
	if interval <= 0 {
		return 0
	}
	return float32(math.Ceil(interval.Minutes()))
}

func convertAPIError(err error, httpResp *http.Response) error {
	if err == nil {
		return nil
	}
	return sdkerrors.ConvertAPIError(err, httpResp)
}

func daytonaHTTPError(statusCode int, headers http.Header, body []byte, fallback string) error {
	message := fallback
	var bodyStatus int
	if len(body) > 0 {
		var parsed struct {
			Message    string `json:"message"`
			Error      string `json:"error"`
			StatusCode int    `json:"statusCode"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			if parsed.Message != "" {
				message = parsed.Message
			} else if parsed.Error != "" {
				message = parsed.Error
			}
			bodyStatus = parsed.StatusCode
		}
		if strings.TrimSpace(message) == "" || message == fallback {
			message = strings.TrimSpace(string(body))
		}
	}
	if bodyStatus != 0 {
		statusCode = bodyStatus
	}
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(statusCode)
	}
	switch {
	case statusCode == http.StatusBadRequest:
		return sdkerrors.NewDaytonaValidationError(message, headers)
	case statusCode == http.StatusUnauthorized:
		return sdkerrors.NewDaytonaAuthenticationError(message, headers)
	case statusCode == http.StatusForbidden:
		return sdkerrors.NewDaytonaForbiddenError(message, headers)
	case statusCode == http.StatusNotFound:
		return sdkerrors.NewDaytonaNotFoundError(message, headers)
	case statusCode == http.StatusConflict:
		return sdkerrors.NewDaytonaConflictError(message, headers)
	case statusCode == http.StatusTooManyRequests:
		return sdkerrors.NewDaytonaRateLimitError(message, headers)
	case statusCode >= 500 && statusCode <= 599:
		return sdkerrors.NewDaytonaServerError(message, statusCode, headers)
	default:
		return sdkerrors.NewDaytonaError(message, statusCode, headers)
	}
}

func isDaytonaNotFound(err error) bool {
	var notFound *sdkerrors.DaytonaNotFoundError
	return errors.As(err, &notFound)
}

func isPtyAlreadyExistsError(err error, sessionID string) bool {
	statusCode, message, ok := daytonaStatusAndMessage(err)
	if !ok {
		return false
	}
	if statusCode != http.StatusConflict && statusCode != http.StatusBadRequest {
		return false
	}
	message = strings.ToLower(message)
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	return strings.Contains(message, "already") &&
		strings.Contains(message, "exist") &&
		(sessionID == "" || strings.Contains(message, sessionID))
}

func daytonaStatusAndMessage(err error) (int, string, bool) {
	var conflict *sdkerrors.DaytonaConflictError
	if errors.As(err, &conflict) {
		return conflict.StatusCode, conflict.Message, true
	}
	var validation *sdkerrors.DaytonaValidationError
	if errors.As(err, &validation) {
		return validation.StatusCode, validation.Message, true
	}
	var daytonaErr *sdkerrors.DaytonaError
	if errors.As(err, &daytonaErr) {
		return daytonaErr.StatusCode, daytonaErr.Message, true
	}
	return 0, "", false
}

func cleanMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func normalizeAPIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultAPIURL
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse Daytona API URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("daytona API URL %q must include scheme and host", raw)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
