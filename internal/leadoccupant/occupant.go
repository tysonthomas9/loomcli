// Package leadoccupant owns the sandbox-side occupant environment, token
// persistence, and bearer transport. It is intentionally stdlib-only so CLI
// and lead runtime composition can share it without dependency cycles.
package leadoccupant

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	EnvOccupantToken = "LOOM_LEAD_OCCUPANT_TOKEN" //nolint:gosec // env var name, not a credential
	EnvLeadAPIURL    = "LOOM_LEAD_API_URL"
	EnvPlacementID   = "LOOM_LEAD_PLACEMENT_ID"
	EnvWorkspace     = "LOOM_WORKSPACE"
	DataPathPrefix   = "/lead/data"

	tokenFileName            = "lead-occupant.token"
	maxUnauthorizedBodyBytes = 64 << 10
	defaultTransportTimeout  = 30 * time.Second
)

// State classifies whether occupant credentials are absent, incomplete, or
// complete enough to construct the allowlisted issue backend.
type State uint8

const (
	StateAbsent State = iota
	StatePartial
	StateComplete
)

// Env is the normalized sandbox occupant environment.
type Env struct {
	BaseURL     string
	Workspace   string
	PlacementID string
	EnvToken    string //nolint:gosec // bearer credential is intentionally carried in memory
}

// FromEnv returns a three-state occupant environment. Presence is keyed by the
// occupant token; once present, URL and workspace are mandatory and callers
// must fail closed when either is missing.
func FromEnv() (Env, State) {
	env := Env{
		BaseURL:     strings.TrimRight(strings.TrimSpace(os.Getenv(EnvLeadAPIURL)), "/"),
		Workspace:   strings.TrimSpace(os.Getenv(EnvWorkspace)),
		PlacementID: strings.TrimSpace(os.Getenv(EnvPlacementID)),
		EnvToken:    strings.TrimSpace(os.Getenv(EnvOccupantToken)),
	}
	if env.EnvToken == "" {
		return env, StateAbsent
	}
	if env.BaseURL == "" || env.Workspace == "" {
		return env, StatePartial
	}
	return env, StateComplete
}

// TokenPath returns the occupant token file, creating its private directory.
func TokenPath() (string, error) {
	base := strings.TrimSpace(os.Getenv("LOOM_CONFIG_DIR"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine home directory: %w", err)
		}
		base = filepath.Join(home, ".loom")
	}
	dir := filepath.Join(base, "tokens")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create occupant token directory: %w", err)
	}
	return filepath.Join(dir, tokenFileName), nil
}

// WriteToken atomically replaces the occupant token file with mode 0600.
func WriteToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("write occupant token: token is empty or malformed")
	}
	path, err := TokenPath()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lead-occupant-token-*")
	if err != nil {
		return fmt.Errorf("create occupant token temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure occupant token temp file: %w", err)
	}
	if _, err := io.WriteString(tmp, token); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write occupant token temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync occupant token temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close occupant token temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace occupant token file: %w", err)
	}
	return nil
}

// ReadToken returns the persisted token or an empty string on any miss,
// unreadable file, or malformed content.
func ReadToken() string {
	path, err := TokenPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path) //nolint:gosec // fixed filename under private config directory
	if err != nil {
		return ""
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return ""
	}
	return token
}

// CurrentToken prefers the file refreshed by the live lead runtime, then the
// initial environment token baked into the sandbox process environment.
func (e Env) CurrentToken() string {
	if token := ReadToken(); token != "" {
		return token
	}
	return strings.TrimSpace(e.EnvToken)
}

// Doer is the injectable outbound HTTP boundary used by occupantTransport.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type occupantTransport struct {
	env  Env
	base Doer
}

// Transport returns a bearer-injecting transport with a bounded stdlib client.
func (e Env) Transport() *occupantTransport {
	return &occupantTransport{
		env:  e,
		base: &http.Client{Timeout: defaultTransportTimeout},
	}
}

func (t *occupantTransport) Do(req *http.Request) (*http.Response, error) {
	defer func() {
		if req.Body != nil {
			_ = req.Body.Close()
		}
	}()

	usedToken := t.env.CurrentToken()
	first, err := cloneForAttempt(req, usedToken, true)
	if err != nil {
		return nil, err
	}
	resp, err := t.base.Do(first)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	captured, err := io.ReadAll(io.LimitReader(resp.Body, maxUnauthorizedBodyBytes))
	if err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("read occupant 401 response: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return nil, fmt.Errorf("close occupant 401 response: %w", err)
	}
	fresher := ReadToken()
	if fresher == "" || fresher == usedToken {
		resp.Body = io.NopCloser(bytes.NewReader(captured))
		resp.ContentLength = int64(len(captured))
		return resp, nil
	}
	if req.Body != nil && req.GetBody == nil {
		return nil, fmt.Errorf("occupant token refreshed but request body cannot be replayed (set GetBody)")
	}
	retry, err := cloneForAttempt(req, fresher, false)
	if err != nil {
		return nil, err
	}
	return t.base.Do(retry)
}

func cloneForAttempt(req *http.Request, token string, first bool) (*http.Request, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+token)
	if req.Body == nil || (first && req.GetBody == nil) {
		return clone, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("reset occupant request body: %w", err)
	}
	clone.Body = body
	return clone, nil
}
