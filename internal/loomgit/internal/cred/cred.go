// Package cred provides a one-shot, repository-scoped credential to host Git.
package cred

import (
	"errors"
	"net/url"
	"strings"
	"sync"
)

var ErrRefused = errors.New("credential refused for URL")

type Source struct {
	mu      sync.Mutex
	repoURL string
	token   string
	used    bool
}

func New(repoURL, token string) *Source {
	return &Source{repoURL: repoURL, token: token}
}

// Take returns the token once, only for the exact HTTPS repository URL.
func (s *Source) Take(repoURL string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used || s.token == "" || !sameRepoURL(s.repoURL, repoURL) {
		return "", ErrRefused
	}
	s.used = true
	return s.token, nil
}

func sameRepoURL(a, b string) bool {
	left, err := url.Parse(a)
	if err != nil {
		return false
	}
	right, err := url.Parse(b)
	if err != nil {
		return false
	}
	if left.Scheme != "https" || right.Scheme != "https" || left.Host == "" ||
		strings.Count(left.EscapedPath(), "/") < 2 ||
		left.User != nil || right.User != nil || left.RawQuery != "" || right.RawQuery != "" ||
		left.Fragment != "" || right.Fragment != "" {
		return false
	}
	return strings.EqualFold(left.Host, right.Host) && left.EscapedPath() == right.EscapedPath()
}
