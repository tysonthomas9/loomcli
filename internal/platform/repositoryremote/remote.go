// Package repositoryremote owns the canonical, token-free repository source
// syntax shared by Workspace admission, Source Control, Connectors, and local
// Git infrastructure. Keeping one validator prevents a lower layer from
// accepting a credential-bearing or ambiguous form rejected by another.
package repositoryremote

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
)

// Normalize returns value unchanged when it is one of the repository source
// forms Fleet admission may durably record:
//   - clean absolute local paths;
//   - http, https, or git URLs without userinfo;
//   - ssh URLs with no userinfo or the conventional non-secret "git" user;
//   - conventional git@host:path SCP syntax.
//
// Query strings, fragments, passwords, other usernames, relative paths, and
// unsupported schemes fail closed.
//
//nolint:cyclop // Remote normalization deliberately enumerates every supported URL and SCP-like form and rejection rule.
func Normalize(value string) (string, error) {
	remote := strings.TrimSpace(value)
	if remote == "" || remote != value || len(remote) > 1024 {
		return "", fmt.Errorf("remote URL must be 1-1024 canonical characters")
	}
	if strings.IndexFunc(remote, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("remote URL contains a control character")
	}
	if strings.ContainsAny(remote, "?#") {
		return "", fmt.Errorf("remote URL query strings and fragments are forbidden")
	}

	if filepath.IsAbs(remote) {
		if filepath.Clean(remote) != remote {
			return "", fmt.Errorf("local repository path must be clean and absolute")
		}
		return remote, nil
	}

	if !strings.Contains(remote, "://") {
		if err := validateSCP(remote); err != nil {
			return "", err
		}
		return remote, nil
	}

	parsed, err := url.Parse(remote)
	if err != nil || parsed.Host == "" || parsed.Path == "" {
		return "", fmt.Errorf("remote URL is malformed")
	}
	switch parsed.Scheme {
	case "http", "https", "git":
		if parsed.User != nil {
			return "", fmt.Errorf("remote URL userinfo is forbidden")
		}
	case "ssh":
		if parsed.User != nil {
			_, hasPassword := parsed.User.Password()
			if hasPassword || parsed.User.Username() != "git" {
				return "", fmt.Errorf("SSH remote may only use the non-secret git user")
			}
		}
	default:
		return "", fmt.Errorf("remote URL scheme is unsupported")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("remote URL query strings and fragments are forbidden")
	}
	return remote, nil
}

func validateSCP(remote string) error {
	if !strings.HasPrefix(remote, "git@") {
		return fmt.Errorf("remote URL format is unsupported")
	}
	rest := strings.TrimPrefix(remote, "git@")
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 ||
		colon == len(rest)-1 ||
		strings.ContainsAny(rest, "@ \t?#") {
		return fmt.Errorf("SCP-style remote is malformed")
	}
	return nil
}
