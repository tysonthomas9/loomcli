package daytona

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	apiclient "github.com/daytonaio/daytona/libs/api-client-go"
	sdkerrors "github.com/daytonaio/daytona/libs/sdk-go/pkg/errors"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

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
		ID:        strings.TrimSpace(sandbox.GetId()),
		Labels:    cleanMap(sandbox.GetLabels()),
		State:     providerState(sandboxState(sandbox)),
		RawState:  placement.ProviderSandboxRawState(sandboxState(sandbox)),
		CreatedAt: providerSandboxCreatedAt(sandbox.GetCreatedAt()),
	}
}

func providerSandboxFromListItem(item apiclient.SandboxListItem) placement.ProviderSandbox {
	return placement.ProviderSandbox{
		ID:        strings.TrimSpace(item.GetId()),
		Labels:    cleanMap(item.GetLabels()),
		State:     providerState(item.GetState()),
		RawState:  placement.ProviderSandboxRawState(item.GetState()),
		CreatedAt: providerSandboxCreatedAt(item.GetCreatedAt()),
	}
}

func providerSandboxCreatedAt(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	createdAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return createdAt.UTC()
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
