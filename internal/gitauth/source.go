// Package gitauth resolves machine-local credentials for git network
// operations without exposing them to callers that only need public remotes.
package gitauth

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

// Source resolves an ephemeral credential for one remote URL. A nil
// credential means the remote should use git's existing anonymous/SSH
// behavior. Callers must Close every non-nil Credential.
type Source interface {
	Resolve(ctx context.Context, remoteURL string) (*Credential, error)
}

// Credential is short-lived plaintext authority for one git subprocess.
// Password intentionally remains mutable so Close can overwrite it.
type Credential struct {
	Username string
	Password []byte
}

// Close overwrites the plaintext password.
func (c *Credential) Close() {
	if c == nil {
		return
	}
	for i := range c.Password {
		c.Password[i] = 0
	}
	c.Password = nil
}

// LocalSettingsSource loads the currently saved desktop GitHub credential on
// every Resolve. It deliberately does not cache plaintext or ciphertext, so a
// Settings rotation applies to the next clone/fetch without restarting serve.
type LocalSettingsSource struct {
	DataDir string
}

// NewLocalSettingsSource returns a source for dataDir. An empty directory
// disables credential resolution and preserves ordinary git behavior.
func NewLocalSettingsSource(dataDir string) Source {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil
	}
	return LocalSettingsSource{DataDir: dataDir}
}

// Resolve supplies the saved GitHub PAT only to HTTPS github.com remotes.
// Local paths, SSH remotes, and other HTTPS hosts remain untouched.
func (s LocalSettingsSource) Resolve(ctx context.Context, remoteURL string) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !isGitHubHTTPSRemote(remoteURL) {
		return nil, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(remoteURL))
	if err != nil || parsed.User != nil {
		return nil, fmt.Errorf("resolve GitHub credential for git: URL userinfo is forbidden")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("resolve GitHub credential for git: URL query strings and fragments are forbidden")
	}

	settings, err := localsettings.Load(strings.TrimSpace(s.DataDir))
	if err != nil {
		return nil, fmt.Errorf("load git credential settings: %w", err)
	}
	if strings.TrimSpace(settings.RuntimeCredentials.GitHub.Sealed) == "" {
		return nil, nil
	}
	password, err := localsettings.UnsealRuntimeCredentialBytes(
		strings.TrimSpace(s.DataDir),
		settings,
		localsettings.RuntimeCredentialProviderGitHub,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub credential for git: %w", err)
	}
	if bytes.ContainsAny(password, "\x00\r\n") {
		zero(password)
		return nil, fmt.Errorf("resolve GitHub credential for git: credential contains invalid control characters")
	}
	return &Credential{Username: "x-access-token", Password: password}, nil
}

func isGitHubHTTPSRemote(remoteURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(remoteURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "github.com") &&
		(parsed.Port() == "" || parsed.Port() == "443")
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
