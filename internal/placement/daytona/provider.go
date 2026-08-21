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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	defaultSandboxStartTimeout        = 3 * time.Minute
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
//
// Outcome classification is fail-closed: only a local payload-validation
// failure is NotDispatched. Every Execute error is Unknown because the SDK
// cannot distinguish a request that never reached Daytona from one whose
// response was lost after the sandbox was created — treating the second as
// "no sandbox" is exactly the billing leak the broker guards against.
func (p *Provider) Create(ctx context.Context, req placement.CreateRequest) (placement.CreateResult, error) {
	ctx, cancel := p.withDefaultTimeout(ctx)
	defer cancel()

	payload, err := p.createPayload(req)
	if err != nil {
		return placement.CreateResult{Outcome: placement.CreateOutcomeNotDispatched}, err
	}

	apiReq := p.apiClient.SandboxAPI.CreateSandbox(p.authContext(ctx)).CreateSandbox(*payload)
	if p.organizationID != "" {
		apiReq = apiReq.XDaytonaOrganizationID(p.organizationID)
	}
	sandbox, httpResp, err := apiReq.Execute()
	if err != nil {
		return placement.CreateResult{Outcome: placement.CreateOutcomeUnknown}, fmt.Errorf("daytona create sandbox: %w", convertAPIError(err, httpResp))
	}

	result := placement.CreateResult{SandboxID: strings.TrimSpace(sandbox.GetId()), Outcome: placement.CreateOutcomeCreated}
	if result.SandboxID == "" {
		result.Outcome = placement.CreateOutcomeUnknown
		return result, fmt.Errorf("daytona create sandbox returned empty id")
	}
	if err := p.waitForStarted(ctx, sandbox); err != nil {
		return result, err
	}
	return result, nil
}

// FindByName resolves a sandbox by its caller-supplied name. Daytona's
// GetSandbox endpoint accepts an id or a name and performs a direct repository
// lookup, so this is authoritative — unlike the eventually-consistent list.
func (p *Provider) FindByName(ctx context.Context, name string) (placement.ProviderSandbox, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return placement.ProviderSandbox{}, fmt.Errorf("daytona sandbox name required")
	}
	return p.Get(ctx, name)
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

// EnsureRunning starts a parked sandbox or waits for an in-flight start to
// reach Daytona's raw started state. ProviderSandboxState is too lossy here:
// it maps transitional and terminal raw states to running.
func (p *Provider) EnsureRunning(ctx context.Context, sandboxID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, defaultSandboxStartTimeout)
	defer cancel()

	sandbox, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return false, err
	}
	resumeInProgress := false
	startConflictAttempt := 0
	for {
		switch state := sandboxState(sandbox); state {
		case apiclient.SANDBOXSTATE_STARTED:
			return resumeInProgress, nil
		case apiclient.SANDBOXSTATE_STOPPED, apiclient.SANDBOXSTATE_ARCHIVED, apiclient.SANDBOXSTATE_PAUSED:
			resumeInProgress = true
			started, retry, startErr := p.startParkedSandbox(ctx, sandboxID, &startConflictAttempt)
			if startErr != nil {
				return false, startErr
			}
			if retry {
				sandbox = started
				continue
			}
			if err := p.waitForStarted(ctx, started); err != nil {
				return true, err
			}
			return true, nil
		case apiclient.SANDBOXSTATE_STARTING, apiclient.SANDBOXSTATE_RESTORING, apiclient.SANDBOXSTATE_RESUMING:
			if err := p.waitForStarted(ctx, sandbox); err != nil {
				return true, err
			}
			return true, nil
		case apiclient.SANDBOXSTATE_STOPPING, apiclient.SANDBOXSTATE_PAUSING, apiclient.SANDBOXSTATE_ARCHIVING:
			resumeInProgress = true
			sandbox, err = p.waitForParkingTransition(ctx, sandbox)
			if err != nil {
				return true, err
			}
		case apiclient.SANDBOXSTATE_ERROR, apiclient.SANDBOXSTATE_BUILD_FAILED:
			return false, fmt.Errorf("daytona sandbox %q cannot start from state %q: %s", sandboxID, state, strings.TrimSpace(sandbox.GetErrorReason()))
		default:
			return false, fmt.Errorf("daytona sandbox %q cannot ensure running from state %q", sandboxID, state)
		}
	}
}

// startParkedSandbox issues the start call for a stopped/archived/paused
// sandbox. On a state-change conflict it backs off, re-reads the sandbox, and
// returns retry=true so the caller re-dispatches on the fresh raw state.
func (p *Provider) startParkedSandbox(ctx context.Context, sandboxID string, attempt *int) (*apiclient.Sandbox, bool, error) {
	apiReq := p.apiClient.SandboxAPI.StartSandbox(p.authContext(ctx), strings.TrimSpace(sandboxID))
	if p.organizationID != "" {
		apiReq = apiReq.XDaytonaOrganizationID(p.organizationID)
	}
	started, httpResp, startErr := apiReq.Execute()
	if startErr == nil {
		return started, false, nil
	}
	mapped := p.mapSandboxOperationError("start", sandboxID, convertAPIError(startErr, httpResp))
	if !isStateChangeInProgress(mapped) || *attempt == deleteConflictAttempts-1 {
		return nil, false, mapped
	}
	delay := deleteConflictBackoff << *attempt
	*attempt++
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return nil, false, fmt.Errorf("daytona start sandbox %q: %w", sandboxID, ctx.Err())
	case <-timer.C:
	}
	refreshed, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return nil, false, err
	}
	return refreshed, true, nil
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
	if prep.BootstrapBinary != nil {
		if err := validateBootstrapBinarySpec(*prep.BootstrapBinary); err != nil {
			return err
		}
	}
	sandbox, err := p.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	// Install the served binary first: it is the cheapest step to fail on, and
	// a fail-hard here (deliberate: never fall back to the baked binary) should
	// bail before the more expensive checkout.
	if prep.BootstrapBinary != nil {
		if err := p.installBootstrapBinary(ctx, sandbox, *prep.BootstrapBinary); err != nil {
			return err
		}
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
	for _, file := range prep.Files {
		if err := p.writeSandboxFile(ctx, sandbox, file); err != nil {
			return err
		}
	}
	return nil
}

// writeSandboxFile seeds one prep file (e.g. the codex auth.json drop, ticket
// 08). Contents may be credentials: the base64 payload is passed as a
// redaction so it can never surface in a prep error, and the write is
// atomic (write-then-rename) like the prompt's.
func (p *Provider) writeSandboxFile(ctx context.Context, sandbox *apiclient.Sandbox, file placement.SandboxFile) error {
	filePath := strings.TrimSpace(file.Path)
	if filePath == "" || !strings.HasPrefix(filePath, "/") {
		return fmt.Errorf("sandbox seed file path must be absolute: %w", domain.ErrInvalid)
	}
	mode := strings.TrimSpace(file.Mode)
	if mode != "" && !isOctalMode(mode) {
		return fmt.Errorf("sandbox seed file mode %q must be octal: %w", mode, domain.ErrInvalid)
	}
	encoded := base64.StdEncoding.EncodeToString(file.Content)
	cmd := "mkdir -p " + shellQuote(path.Dir(filePath)) +
		" && printf %s " + shellQuote(encoded) + " | base64 -d > " + shellQuote(filePath+".tmp")
	if mode != "" {
		cmd += " && chmod " + mode + " " + shellQuote(filePath+".tmp")
	}
	cmd += " && mv -f " + shellQuote(filePath+".tmp") + " " + shellQuote(filePath)
	_, err := p.execLeadPrep(ctx, sandbox, cmd, encoded)
	if err != nil {
		return fmt.Errorf("seed sandbox file %q: %w", filePath, err)
	}
	return nil
}

func isOctalMode(mode string) bool {
	if len(mode) < 3 || len(mode) > 4 {
		return false
	}
	for _, r := range mode {
		if r < '0' || r > '7' {
			return false
		}
	}
	return true
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
