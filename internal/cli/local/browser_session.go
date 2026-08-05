package local

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const localBrowserSessionResponseLimit = 64 << 10

type localBrowserSessionHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

var localBrowserSessionClient localBrowserSessionHTTPDoer = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type localBrowserSessionOutput struct {
	RuntimeURL string `json:"runtime_url"`
	Workspace  string `json:"workspace"`
	LaunchCode string `json:"launch_code"`
	ExpiresAt  string `json:"expires_at"`
}

type localBrowserSessionLaunchResponse struct {
	LaunchCode string `json:"launch_code"`
	Workspace  string `json:"workspace"`
	ExpiresAt  string `json:"expires_at"`
}

var browserSessionCmd = &cobra.Command{
	Use:    "browser-session",
	Short:  "Create a one-time trusted Desktop browser session",
	Hidden: true,
	RunE:   runBrowserSession,
}

func init() {
	localCmd.AddCommand(browserSessionCmd)
}

func runBrowserSession(cmd *cobra.Command, _ []string) error {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	status, err := readRuntimeStatusFn(cmd.Context(), dataDir)
	if err != nil {
		return fmt.Errorf("local browser session runtime status: %w", err)
	}
	if status == nil || !status.Healthy || status.Runtime == nil {
		return fmt.Errorf("local browser session requires a healthy local runtime")
	}
	result, err := createLocalBrowserSession(cmd.Context(), dataDir, status.Runtime.URL, localBrowserSessionClient)
	if err != nil {
		return err
	}
	return writeJSON(cmd.OutOrStdout(), result)
}

func createLocalBrowserSession(ctx context.Context, dataDir, runtimeURL string, doer localBrowserSessionHTTPDoer) (*localBrowserSessionOutput, error) {
	baseURL, err := validateLocalBrowserRuntimeURL(runtimeURL)
	if err != nil {
		return nil, err
	}
	if doer == nil {
		return nil, fmt.Errorf("local browser session HTTP client is unavailable")
	}
	workspace, err := fetchActiveWorkspaceID(ctx, doer, baseURL)
	if err != nil {
		return nil, err
	}
	credentialDir := filepath.Join(dataDir, ".loom", "operator")
	token, err := authority.ReadLocalOperatorToken(credentialDir)
	if err != nil {
		return nil, fmt.Errorf("local browser session authentication: %w", err)
	}

	endpoint := baseURL + "/api/workspaces/" + url.PathEscape(workspace) + "/operator-sessions/launch"
	payload, err := requestLocalBrowserSession(ctx, doer, endpoint, token)
	if err != nil {
		return nil, err
	}
	if payload.Workspace != workspace {
		return nil, fmt.Errorf("local browser session workspace mismatch")
	}
	if !validLocalBrowserToken(payload.LaunchCode) {
		return nil, fmt.Errorf("local browser session endpoint returned an invalid launch code")
	}
	return &localBrowserSessionOutput{
		RuntimeURL: baseURL,
		Workspace:  workspace,
		LaunchCode: payload.LaunchCode,
		ExpiresAt:  payload.ExpiresAt,
	}, nil
}

func requestLocalBrowserSession(ctx context.Context, doer localBrowserSessionHTTPDoer, endpoint, token string) (*localBrowserSessionLaunchResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build local browser session request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := doer.Do(request)
	if err != nil {
		return nil, fmt.Errorf("local browser session endpoint unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, localBrowserSessionResponseLimit))
		return nil, fmt.Errorf("local browser session endpoint returned HTTP %d", response.StatusCode)
	}
	var payload localBrowserSessionLaunchResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, localBrowserSessionResponseLimit+1))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode local browser session response: %w", err)
	}
	return &payload, nil
}

func fetchActiveWorkspaceID(ctx context.Context, doer localBrowserSessionHTTPDoer, baseURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/workspaces/active", nil)
	if err != nil {
		return "", fmt.Errorf("build active workspace request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := doer.Do(request)
	if err != nil {
		return "", fmt.Errorf("resolve local browser session workspace: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, localBrowserSessionResponseLimit))
		return "", fmt.Errorf("active workspace endpoint returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, localBrowserSessionResponseLimit+1))
	if err := decoder.Decode(&payload); err != nil {
		return "", fmt.Errorf("decode active workspace response: %w", err)
	}
	workspace := strings.TrimSpace(payload.Data.ID)
	if !payload.Success || workspace == "" {
		return "", fmt.Errorf("active workspace endpoint returned no canonical workspace")
	}
	return workspace, nil
}

func validateLocalBrowserRuntimeURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("local browser session runtime URL is invalid")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("local browser session runtime URL must use a loopback IP")
	}
	if parsed.Port() == "" {
		return "", fmt.Errorf("local browser session runtime URL requires an explicit port")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validLocalBrowserToken(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && !bytes.Equal(decoded, make([]byte, 32))
}
