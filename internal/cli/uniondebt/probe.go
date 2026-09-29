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
	// ClassSuperseded means this ledger item's branch is no longer the branch the
	// debt was filed against: the work has been rebuilt, or is already present in
	// union under a different sha. Merging the recorded ref would re-apply
	// abandoned code.
	ClassSuperseded Class = "superseded"
)

// ProbeResult describes one probe. Ref and TipSHA are empty for ClassNoBranch
// and ClassNoUnion.
type ProbeResult struct {
	Class    Class
	Ref      string
	TipSHA   string
	Conflict string // verbatim merge-tree conflict summary, when ClassConflict
	Detail   string // which signal fired, when ClassSuperseded
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
// Ref resolution tries the highest-numbered origin/loom/<ID>-r<N> first, then
// origin/loom/<ID>, then a bare local loom/<ID>. The local fallback is
// load-bearing and stays LAST: PUPPET-308 exists only as a local branch in the
// meta-harness clone, and an origin-only lookup would call it NoBranch and
// wrongly retire real debt.
//
// recordedTip is the tip the debt was originally filed against, or "" when it
// is unknown. It is only ever used to detect that the ref has since been
// replaced by a non-descendant; an unknown tip simply skips that signal,
// because a false "superseded" would retire real debt.
func (p *Prober) Probe(clone, unionBranch, taskID, recordedTip string) (ProbeResult, error) {
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

	ref, tip, err := p.resolveRef(clone, taskID)
	if err != nil {
		return ProbeResult{}, err
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

	// Superseded is checked only after the cheap ancestor case, so it can never
	// shadow a branch that is genuinely in union.
	detail, err := p.superseded(clone, unionBranch, ref, tip, recordedTip)
	if err != nil {
		return ProbeResult{}, err
	}
	if detail != "" {
		return ProbeResult{Class: ClassSuperseded, Ref: ref, TipSHA: tip, Detail: detail}, nil
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

// resolveRef finds the ref that answers to taskID today, and its tip. It
// returns "" for both when nothing resolves.
//
// The order is the whole point: the highest-numbered republished revision, then
// the unsuffixed origin ref, then the bare local branch LAST.
func (p *Prober) resolveRef(clone, taskID string) (ref, tip string, err error) {
	rev, err := highestRevisionRef(p.git, clone, taskID)
	if err != nil {
		return "", "", err
	}
	candidates := []string{"origin/loom/" + taskID, "loom/" + taskID}
	if rev != "" {
		candidates = append([]string{rev}, candidates...)
	}

	for _, candidate := range candidates {
		out, code, err := p.git.Run(clone, "rev-parse", "-q", "--verify", candidate)
		if err != nil {
			return "", "", fmt.Errorf("rev-parse %s in %s: %w", candidate, clone, err)
		}
		if code == 0 {
			return candidate, strings.TrimSpace(out), nil
		}
	}
	return "", "", nil
}

// superseded reports why ref is superseded, or "" when it is ordinary debt.
//
// Two independent signals, either of which is enough:
//
//   - Ref moved. The tip the debt was filed against is not an ancestor of the
//     ref that answers to the task's name today, so the branch was rebuilt and
//     merging it would re-apply abandoned code. A recorded tip that no longer
//     resolves at all — replaced and then garbage-collected — counts too, but
//     only because a current, different ref exists to compare it against.
//   - Content already present. Every path the branch touches is byte-identical
//     in union, so its work arrived by some other route.
func (p *Prober) superseded(clone, unionBranch, ref, tip, recordedTip string) (string, error) {
	if recordedTip != "" && validateRef(recordedTip) == nil {
		// ^{commit} is what makes this a real existence check: a full 40-char
		// hex name is syntactically valid to rev-parse whether or not the
		// object is still in the clone, so --verify alone would accept a
		// garbage-collected sha.
		_, code, err := p.git.Run(clone, "rev-parse", "-q", "--verify", recordedTip+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("rev-parse %s in %s: %w", recordedTip, clone, err)
		}
		switch {
		case code != 0:
			if tip != recordedTip {
				return fmt.Sprintf("the recorded tip %s no longer resolves in %s and %s now points at %s",
					recordedTip, clone, ref, tip), nil
			}
		default:
			_, code, err := p.git.Run(clone, "merge-base", "--is-ancestor", recordedTip, ref)
			if err != nil {
				return "", fmt.Errorf("merge-base in %s: %w", clone, err)
			}
			if code != 0 {
				return fmt.Sprintf("the recorded tip %s is not an ancestor of %s (%s): the branch was rebuilt",
					recordedTip, ref, tip), nil
			}
		}
	}

	present, err := p.contentInUnion(clone, unionBranch, ref)
	if err != nil {
		return "", err
	}
	if present {
		return fmt.Sprintf("every path %s touches is already byte-identical in %s: its work arrived by another route",
			ref, unionBranch), nil
	}
	return "", nil
}

// diffChunk caps how many paths go into one `git diff` invocation, so a branch
// touching thousands of files cannot overflow ARG_MAX.
const diffChunk = 500

// contentInUnion reports whether every path ref touches since its merge base
// with union is already identical in union. An EMPTY path list is not enough:
// a branch that changes nothing is handled by the ancestor check above, and
// treating it as superseded here would retire debt on no evidence at all.
func (p *Prober) contentInUnion(clone, unionBranch, ref string) (bool, error) {
	base, code, err := p.git.Run(clone, "merge-base", unionBranch, ref)
	if err != nil {
		return false, fmt.Errorf("merge-base %s %s in %s: %w", unionBranch, ref, clone, err)
	}
	if code != 0 {
		// No common ancestor (unrelated histories): nothing to compare.
		return false, nil
	}
	base = strings.TrimSpace(base)

	// -z keeps paths raw: git quotes and escapes unusual names in the default
	// output, and a quoted path handed straight back to git matches nothing.
	out, code, err := p.git.Run(clone, "diff", "-z", "--name-only", base, ref)
	if err != nil {
		return false, fmt.Errorf("diff --name-only %s %s in %s: %w", base, ref, clone, err)
	}
	if code != 0 {
		return false, fmt.Errorf("diff --name-only %s %s in %s: exit %d", base, ref, clone, code)
	}
	var paths []string
	for _, path := range strings.Split(out, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return false, nil
	}

	for start := 0; start < len(paths); start += diffChunk {
		end := start + diffChunk
		if end > len(paths) {
			end = len(paths)
		}
		args := append([]string{"diff", "--quiet", unionBranch, ref, "--"}, paths[start:end]...)
		_, code, err := p.git.Run(clone, args...)
		if err != nil {
			return false, fmt.Errorf("diff --quiet %s %s in %s: %w", unionBranch, ref, clone, err)
		}
		if code != 0 {
			// Some path differs — ordinary debt. ALL of them must match.
			return false, nil
		}
	}
	return true, nil
}
