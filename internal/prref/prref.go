// Package prref owns the canonical pull request identity key.
//
// A PR key is "github:<owner>/<repo>#<number>" with owner and repo lowercased
// and taken from the PR's BASE repository (the registered repo), never from a
// fork head. Parse also accepts the legacy unprefixed "owner/repo#number" form.
// The GitHub node ID is carried separately and is only used to heal a key after
// a repository rename or transfer.
package prref

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Provider is the only PR provider prefix currently issued.
const Provider = "github"

const keyPrefix = Provider + ":"

var segmentRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Ref identifies a pull request by its base repository and number.
type Ref struct {
	Owner  string
	Repo   string
	Number int
}

// Key returns the canonical key for r, or "" when r is not a valid reference.
func (r Ref) Key() string {
	return Format(r.Owner, r.Repo, r.Number)
}

// Format builds the canonical key for a PR in the given base repository.
// It returns "" when any component is invalid so callers can omit the key.
func Format(owner, repo string, number int) string {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if !validSegment(owner) || !validSegment(repo) || number <= 0 {
		return ""
	}
	return keyPrefix + strings.ToLower(owner) + "/" + strings.ToLower(repo) + "#" + strconv.Itoa(number)
}

// Parse accepts a canonical ("github:owner/repo#N") or legacy ("owner/repo#N")
// key and returns the lowercased reference.
func Parse(key string) (Ref, error) {
	raw := strings.TrimSpace(key)
	if len(raw) >= len(keyPrefix) && strings.EqualFold(raw[:len(keyPrefix)], keyPrefix) {
		raw = raw[len(keyPrefix):]
	}
	slug, num, ok := strings.Cut(raw, "#")
	if !ok {
		return Ref{}, fmt.Errorf("invalid pr key %q: missing #number", key)
	}
	owner, repo, ok := strings.Cut(slug, "/")
	if !ok || !validSegment(owner) || !validSegment(repo) {
		return Ref{}, fmt.Errorf("invalid pr key %q: want owner/repo#number", key)
	}
	number, err := strconv.Atoi(num)
	if err != nil || number <= 0 || strconv.Itoa(number) != num {
		return Ref{}, fmt.Errorf("invalid pr key %q: bad number", key)
	}
	return Ref{Owner: strings.ToLower(owner), Repo: strings.ToLower(repo), Number: number}, nil
}

// FromURL extracts the reference from a GitHub PR web URL such as
// https://github.com/owner/repo/pull/42 (sub-paths like /files are allowed).
func FromURL(raw string) (Ref, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return Ref{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "github.com" {
		return Ref{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || (!strings.EqualFold(parts[2], "pull") && !strings.EqualFold(parts[2], "pulls")) {
		return Ref{}, false
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	number, err := strconv.Atoi(parts[3])
	if err != nil || number <= 0 || !validSegment(parts[0]) || !validSegment(repo) {
		return Ref{}, false
	}
	return Ref{Owner: strings.ToLower(parts[0]), Repo: strings.ToLower(repo), Number: number}, true
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && segmentRE.MatchString(s)
}
