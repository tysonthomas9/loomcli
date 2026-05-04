package service

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
			if !cloneURLPattern.MatchString(u) {
				return ErrValidation(fmt.Sprintf("clone URL must start with https:// or git@: %s", u))
			}
			if err := validateCloneURL(u); err != nil {
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
