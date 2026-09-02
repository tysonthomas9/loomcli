// Package uniondebt turns the `union-pending` ledger into actionable work.
//
// NO NETWORK, EVER. Nothing in this package runs `git fetch`, and nothing may
// be added that does. Two of the three union clones use SSH remotes, and every
// process under the PM2 God Daemon fails getpwuid(501), so ssh — and any fetch
// through it — dies in ~40ms with a message that reads like a credentials
// failure (PUPPET-283). The sweeper reads whatever refs the clone already has
// and records the probe time and tip SHA in what it files, so a stale read is
// visible rather than silent.
package uniondebt

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Class is the outcome of probing one ledger item's branch against the union
// branch of its repo's clone.
type Class string

const (
	// ClassInUnion means the ref is already an ancestor of the union branch:
	// the debt was illusory and the marker can simply be retired.
	ClassInUnion Class = "in-union"
	// ClassClean means the ref is not in union but merges without conflict.
	ClassClean Class = "clean"
	// ClassConflict means the ref is not in union and conflicts with it.
	ClassConflict Class = "conflict"
	// ClassNoBranch means neither origin/loom/<ID> nor loom/<ID> exists.
	ClassNoBranch Class = "no-branch"
	// ClassNoUnion means the clone is missing, or has no union branch.
	ClassNoUnion Class = "no-union"
)

// ProbeResult describes one probe. Ref and TipSHA are empty for ClassNoBranch
// and ClassNoUnion.
type ProbeResult struct {
	Class    Class
	Ref      string
	TipSHA   string
	Conflict string // verbatim merge-tree conflict summary, when ClassConflict
}

// gitRunner runs one git invocation and reports its exit code separately from
// a genuine failure to run. The exit code matters: `git merge-tree` uses 1 to
// mean "conflict", which is a normal result here, and >1 to mean a real error.
type gitRunner interface {
	Run(dir string, args ...string) (stdout string, exitCode int, err error)
}

type execGitRunner struct{}

func (execGitRunner) Run(dir string, args ...string) (string, int, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // G204 — args are literals plus refs validated by validateRef
	cmd.Dir = dir
	// Keep the probe hermetic: the operator's global git config must not change
	// how a merge resolves.
	cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), ee.ExitCode(), nil
		}
		return "", -1, err
	}
	return string(out), 0, nil
}

// refPattern mirrors internal/cli/git's validateGitRef so a malformed task ID
// can never become a git argument.
var refPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

func validateRef(name string) error {
	if !refPattern.MatchString(name) {
		return fmt.Errorf("invalid git ref %q: must match [a-zA-Z0-9][a-zA-Z0-9_./-]*", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid git ref %q: must not contain '..'", name)
	}
	return nil
}

// Prober probes ledger items against a repo's union branch.
type Prober struct {
	git gitRunner
}

// NewProber returns a Prober backed by the real git binary.
func NewProber() *Prober { return &Prober{git: execGitRunner{}} }

// Probe classifies taskID's branch against unionBranch inside clone.
//
// Ref resolution tries origin/loom/<ID> first and then a bare local loom/<ID>.
// The fallback is load-bearing: PUPPET-308 exists only as a local branch in the
// meta-harness clone, and an origin-only lookup would call it NoBranch and
// wrongly retire real debt.
func (p *Prober) Probe(clone, unionBranch, taskID string) (ProbeResult, error) {
	if err := validateRef(unionBranch); err != nil {
		return ProbeResult{}, err
	}
	if err := validateRef(taskID); err != nil {
		return ProbeResult{}, err
	}

	if _, code, err := p.git.Run(clone, "rev-parse", "-q", "--verify", unionBranch); err != nil || code != 0 {
		// A missing clone directory and a missing union branch are the same
		// thing to a caller: there is nothing local to compare against.
		return ProbeResult{Class: ClassNoUnion}, nil //nolint:nilerr // absence is a classification, not a failure
	}

	var ref, tip string
	for _, candidate := range []string{"origin/loom/" + taskID, "loom/" + taskID} {
		out, code, err := p.git.Run(clone, "rev-parse", "-q", "--verify", candidate)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("rev-parse %s in %s: %w", candidate, clone, err)
		}
		if code == 0 {
			ref, tip = candidate, strings.TrimSpace(out)
			break
		}
	}
	if ref == "" {
		return ProbeResult{Class: ClassNoBranch}, nil
	}

	_, code, err := p.git.Run(clone, "merge-base", "--is-ancestor", ref, unionBranch)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("merge-base in %s: %w", clone, err)
	}
	if code == 0 {
		return ProbeResult{Class: ClassInUnion, Ref: ref, TipSHA: tip}, nil
	}

	out, code, err := p.git.Run(clone, "merge-tree", "--write-tree", unionBranch, ref)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("merge-tree in %s: %w", clone, err)
	}
	switch {
	case code == 0:
		return ProbeResult{Class: ClassClean, Ref: ref, TipSHA: tip}, nil
	case code == 1:
		return ProbeResult{Class: ClassConflict, Ref: ref, TipSHA: tip, Conflict: strings.TrimSpace(out)}, nil
	default:
		return ProbeResult{}, fmt.Errorf("merge-tree %s %s in %s: exit %d", unionBranch, ref, clone, code)
	}
}
