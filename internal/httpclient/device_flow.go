package httpclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/netbase"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// deviceFlowClient is the traced HTTP client used by RFC 8628 device flow
// requests. Lazily initialized so tests that override transport via the
// global default still work.
var (
	deviceFlowClientOnce sync.Once
	deviceFlowClient     *http.Client
)

func getDeviceFlowClient() *http.Client {
	deviceFlowClientOnce.Do(func() {
		deviceFlowClient = &http.Client{
			Transport: otelhttp.NewTransport(netbase.Transport()),
		}
	})
	return deviceFlowClient
}

// postForm is a traced equivalent of http.PostForm.
func postForm(endpoint string, form url.Values) (*http.Response, error) {
	return getDeviceFlowClient().PostForm(endpoint, form)
}

// cliClientID is the well-known OAuth client ID for the loom CLI.
const cliClientID = "loom-cli"

// DeviceFlowConfig holds parameters for the device authorization flow.
type DeviceFlowConfig struct {
	AuthURL  string // Better Auth service URL
	ClientID string // OAuth client ID (defaults to cliClientID if empty)
}

// DeviceCodeResponse is the response from the device authorization endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// deviceTokenResponse is the response from the device token polling endpoint.
type deviceTokenResponse struct {
	AccessToken string `json:"access_token"` //nolint:gosec // OAuth2 token field name
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error,omitempty"`
}

// RunDeviceFlow initiates the device authorization flow.
// It displays the verification URL and user code to stderr, then polls
// until the user authorizes or the code expires.
func RunDeviceFlow(cfg DeviceFlowConfig) (token string, expiresAt time.Time, err error) {
	clientID := cfg.ClientID
	if clientID == "" {
		clientID = cliClientID
	}

	// Step 1: Request device code.
	codeResp, err := requestDeviceCode(cfg.AuthURL, clientID)
	if err != nil {
		return "", time.Time{}, err
	}

	// Step 2: Display instructions to stderr (so stdout piping works).
	_, _ = fmt.Fprintf(stderr, "\nTo authenticate, visit: %s\n", codeResp.VerificationURI)
	_, _ = fmt.Fprintf(stderr, "Enter code: %s\n", codeResp.UserCode)
	_, _ = fmt.Fprintf(stderr, "Waiting for authorization...\n\n")

	// Step 3: Poll for token.
	interval := time.Duration(codeResp.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(codeResp.ExpiresIn) * time.Second)

	for {
		if time.Now().After(deadline) {
			return "", time.Time{}, fmt.Errorf("authorization timed out — please try again")
		}

		time.Sleep(interval)

		tokenResp, err := pollDeviceToken(cfg.AuthURL, clientID, codeResp.DeviceCode)
		if err != nil {
			return "", time.Time{}, err
		}

		switch tokenResp.Error {
		case "":
			// Success.
			expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
			return tokenResp.AccessToken, expiresAt, nil
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "expired_token":
			return "", time.Time{}, fmt.Errorf("authorization timed out — please try again")
		case "access_denied":
			return "", time.Time{}, fmt.Errorf("authorization denied — access was not granted")
		default:
			return "", time.Time{}, fmt.Errorf("device flow error: %s", tokenResp.Error)
		}
	}
}

func requestDeviceCode(authURL, clientID string) (*DeviceCodeResponse, error) {
	endpoint := strings.TrimRight(authURL, "/") + "/api/auth/device/code"
	form := url.Values{"client_id": {clientID}}

	resp, err := postForm(endpoint, form) //nolint:gosec // G107: URL is constructed from user-provided auth service base URL
	if err != nil {
		return nil, fmt.Errorf("cannot reach auth service at %s: %w", authURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("reading device code response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, body)
	}

	var codeResp DeviceCodeResponse
	if err := json.Unmarshal(body, &codeResp); err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}
	return &codeResp, nil
}

func pollDeviceToken(authURL, clientID, deviceCode string) (*deviceTokenResponse, error) {
	endpoint := strings.TrimRight(authURL, "/") + "/api/auth/device/token"
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	resp, err := postForm(endpoint, form) //nolint:gosec // G107: URL is constructed from user-provided auth service base URL
	if err != nil {
		return nil, fmt.Errorf("polling device token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("reading device token response: %w", err)
	}

	// RFC 8628: error responses come as 400 Bad Request. 200 is success.
	// Anything else (5xx, 429, etc.) is a transient server error.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return nil, fmt.Errorf("device token poll failed (%d): %s", resp.StatusCode, body)
	}

	var tokenResp deviceTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing device token response: %w", err)
	}
	return &tokenResp, nil
}

// stderr is the writer for user-facing device flow messages.
// Defaults to os.Stderr; override in tests.
var stderr io.Writer = os.Stderr
