package sandbox

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// ResolveRepoURL returns the HTTP(S) URL a sandbox uses to clone and push.
// Resolution order is LOOM_SANDBOX_REPO_URL, the worktree's origin, then the
// optional fallback. Only discovered URLs are rewritten for container access;
// an explicit override is already expected to be container-reachable.
func ResolveRepoURL(worktreePath, fallback string) (string, error) {
	raw := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_REPO_URL"))
	explicit := raw != ""
	if raw == "" {
		if out, err := exec.Command("git", "-C", worktreePath, "remote", "get-url", "origin").Output(); err == nil { //nolint:gosec // worktree path is resolved by the caller
			raw = strings.TrimSpace(string(out))
		}
	}
	if raw == "" {
		raw = strings.TrimSpace(fallback)
	}
	if raw == "" {
		return "", nil
	}

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("sandbox repo URL %q must use http or https; set LOOM_SANDBOX_REPO_URL to a container-reachable HTTP(S) clone-and-push URL", raw)
	}
	if !explicit {
		rewriteLocalhost(u)
	}
	return u.String(), nil
}

func rewriteLocalhost(u *url.URL) {
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "0.0.0.0":
		host := HostGateway()
		if port := u.Port(); port != "" {
			host = net.JoinHostPort(host, port)
		}
		u.Host = host
	}
}
