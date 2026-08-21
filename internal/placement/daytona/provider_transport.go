package daytona

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	apiclient "github.com/daytonaio/daytona/libs/api-client-go"
	sdkerrors "github.com/daytonaio/daytona/libs/sdk-go/pkg/errors"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

func (p *Provider) createPayload(req placement.CreateRequest) (*apiclient.CreateSandbox, error) {
	payload := apiclient.NewCreateSandbox()
	payload.SetSnapshot(p.snapshotLookupName(req.SnapshotRef))
	payload.SetLabels(cleanMap(req.Labels))
	payload.SetEnv(sandboxEnv(req.Env))
	// The caller-supplied name is Daytona's uniqueness anchor: it persists
	// atomically with the create, so an ambiguous create can be reconciled by
	// an authoritative GetSandbox-by-name point read.
	if name := strings.TrimSpace(req.Name); name != "" {
		payload.SetName(name)
	}
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
		case apiclient.SANDBOXSTATE_STOPPED:
			return fmt.Errorf("daytona sandbox %q stopped before reaching started state", id)
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

func (p *Provider) waitForParkingTransition(ctx context.Context, sandbox *apiclient.Sandbox) (*apiclient.Sandbox, error) {
	id := strings.TrimSpace(sandbox.GetId())
	for {
		switch state := sandboxState(sandbox); state {
		case apiclient.SANDBOXSTATE_STOPPING, apiclient.SANDBOXSTATE_PAUSING, apiclient.SANDBOXSTATE_ARCHIVING:
		case apiclient.SANDBOXSTATE_ERROR, apiclient.SANDBOXSTATE_BUILD_FAILED:
			return nil, fmt.Errorf("daytona sandbox %q failed during state transition with state %q: %s", id, state, strings.TrimSpace(sandbox.GetErrorReason()))
		default:
			return sandbox, nil
		}

		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for daytona sandbox %q state transition: %w", id, ctx.Err())
		case <-timer.C:
		}

		next, err := p.getSandbox(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("wait for daytona sandbox %q state transition: %w", id, err)
		}
		sandbox = next
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
