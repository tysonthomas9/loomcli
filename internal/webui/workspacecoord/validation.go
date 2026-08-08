package workspacecoord

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

const maxWorkspaceNameLen = 64

var cloneURLPattern = regexp.MustCompile(`^(https://|git@)`)

// IsCloneURL reports whether a value uses one of Loom's supported git clone
// URL forms.
func IsCloneURL(u string) bool {
	return cloneURLPattern.MatchString(u)
}

// ValidateCloneURL validates a git clone URL before any command shells out to
// git. Keep this shared so CLI and web paths enforce the same boundary.
func ValidateCloneURL(u string) error {
	if !IsCloneURL(u) {
		return fmt.Errorf("clone URL must start with https:// or git@: %s", u)
	}
	return validateCloneURL(u)
}

func validateWorkspaceName(name string) *ServiceError {
	if name == "" {
		return ErrValidation("name cannot be empty")
	}
	if len(name) > maxWorkspaceNameLen {
		return ErrValidation(fmt.Sprintf("name too long (max %d characters)", maxWorkspaceNameLen))
	}
	if !validWorkspaceName(name) {
		return ErrValidation("name must contain only alphanumeric characters, hyphens, and underscores")
	}
	return nil
}

func validWorkspaceName(name string) bool {
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validateWorkspaceCreateRequest(req *WorkspaceCreateRequest) *ServiceError {
	if req.Name == "" {
		return ErrValidation("name is required")
	}
	if len(req.Name) > maxWorkspaceNameLen {
		return ErrValidation(fmt.Sprintf("name too long (max %d characters)", maxWorkspaceNameLen))
	}
	if !validWorkspaceName(req.Name) {
		return ErrValidation("name must contain only alphanumeric characters, hyphens, and underscores")
	}

	switch req.Type {
	case "empty":
		// Empty workspaces are valid: users can create the project boundary
		// first and attach one or more repos later.
	case "clone":
		if len(req.CloneURLs) == 0 {
			return ErrValidation("at least one clone URL is required for clone workspace type")
		}
		for _, u := range req.CloneURLs {
			if err := ValidateCloneURL(u); err != nil {
				return ErrValidation(err.Error())
			}
		}
	case "template":
		return &ServiceError{Kind: KindUnavailable, Message: "template workspace type is not yet supported"}
	case "":
		return ErrValidation("type is required")
	default:
		return ErrValidation(fmt.Sprintf("invalid type %q; must be empty, clone, or template", req.Type))
	}

	return nil
}

func normalizeWorkspaceAddReposRequest(req WorkspaceAddReposRequest) WorkspaceAddReposRequest {
	normalized := req
	normalized.Repos = nil
	normalized.CloneURLs = trimNonEmpty(req.CloneURLs)
	for _, repo := range req.Repos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		if IsCloneURL(repo) {
			normalized.CloneURLs = append(normalized.CloneURLs, repo)
			continue
		}
		normalized.Repos = append(normalized.Repos, repo)
	}
	return normalized
}

// WorkspaceAddReposRequiresClone reports whether an add-repositories request
// contains a supported remote clone URL. It recognizes the legacy form where
// clone URLs were supplied through repos as well as the explicit clone_urls
// field, matching normalizeWorkspaceAddReposRequest.
func WorkspaceAddReposRequiresClone(req WorkspaceAddReposRequest) bool {
	for _, cloneURL := range req.CloneURLs {
		if strings.TrimSpace(cloneURL) != "" {
			return true
		}
	}
	for _, repo := range req.Repos {
		if IsCloneURL(strings.TrimSpace(repo)) {
			return true
		}
	}
	return false
}

func validateWorkspaceAddReposRequest(req *WorkspaceAddReposRequest) *ServiceError {
	if req.WorkspaceID == "" {
		return ErrValidation("workspace ID is required")
	}
	if len(req.Repos) == 0 && len(req.CloneURLs) == 0 {
		return ErrValidation("at least one repo path or clone URL is required")
	}
	for _, u := range req.CloneURLs {
		if err := ValidateCloneURL(u); err != nil {
			return ErrValidation(err.Error())
		}
	}
	return nil
}

func trimNonEmpty(values []string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// SECURITY: Clone URL validation defends against:
//  1. Arbitrary protocol injection — only https:// and git@ allowed (prefix check)
//  2. Git flag injection — no path segments starting with "-"
//  3. Control character injection — no null bytes, newlines, carriage returns
//  4. SSRF via IP literals — blocks loopback, private, CGNAT, link-local, metadata IPs
//  5. SSRF via known hostnames — blocks localhost, cloud metadata hostnames
func validateCloneURL(u string) error {
	if strings.ContainsAny(u, "\x00\n\r") {
		return fmt.Errorf("clone URL contains invalid characters")
	}
	for _, seg := range strings.Split(u, "/") {
		if strings.HasPrefix(seg, "-") {
			return fmt.Errorf("clone URL contains suspicious path segment starting with '-'")
		}
	}

	host, err := extractCloneHost(u)
	if err != nil {
		return fmt.Errorf("cannot parse clone URL host")
	}
	if isBlockedCloneHost(host) {
		return fmt.Errorf("clone URL targets a blocked host (private/internal network)")
	}
	return nil
}

func extractCloneHost(cloneURL string) (string, error) {
	if strings.HasPrefix(cloneURL, "https://") {
		u, err := url.Parse(cloneURL)
		if err != nil {
			return "", err
		}
		if u.User != nil {
			return "", fmt.Errorf("URL userinfo is forbidden")
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("URL query strings and fragments are forbidden")
		}
		host := u.Hostname()
		if host == "" {
			return "", fmt.Errorf("empty host in URL")
		}
		return host, nil
	}
	if strings.HasPrefix(cloneURL, "git@") {
		rest := cloneURL[len("git@"):]
		if strings.HasPrefix(rest, "[") {
			closeBracket := strings.Index(rest, "]")
			if closeBracket < 0 {
				return "", fmt.Errorf("unclosed bracket in git@ URL")
			}
			host := rest[1:closeBracket]
			if host == "" {
				return "", fmt.Errorf("empty host in git@ URL")
			}
			return host, nil
		}
		colonIdx := strings.Index(rest, ":")
		if colonIdx <= 0 {
			return "", fmt.Errorf("cannot find host in git@ URL")
		}
		return rest[:colonIdx], nil
	}
	return "", fmt.Errorf("unsupported URL scheme")
}

// cgnatBlock is the Carrier-Grade NAT shared address space (RFC 6598).
var cgnatBlock = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

func isBlockedCloneHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")

	switch host {
	case "localhost", "localhost.localdomain",
		"metadata.google.internal", "metadata.internal":
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || cgnatBlock.Contains(ip)
}

// classifyWorkspaceCreateError maps a workspace creation error to a ServiceError.
func classifyWorkspaceCreateError(err error) *ServiceError {
	var ce *workspaceerrors.CreateError
	if errors.As(err, &ce) {
		switch ce.Code {
		case workspaceerrors.AlreadyExists:
			return ErrConflict(ce.Message)
		case workspaceerrors.PathNotFound, workspaceerrors.NotGitRepo, workspaceerrors.GitFailed:
			return ErrValidation(ce.Message)
		case workspaceerrors.SecurityViolation:
			return ErrForbidden(ce.Message)
		case workspaceerrors.ConfigFailed:
			return ErrInternal(ce.Message, err)
		}
	}
	return ErrInternal("failed to create workspace", err)
}
