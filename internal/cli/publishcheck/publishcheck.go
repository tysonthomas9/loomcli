// Package publishcheck states the relationship between a local task branch and
// its published head, so an integrator can name the diverged-branch condition
// instead of remembering it.
//
// When a coder re-cuts an already-published loom/<ID> branch, the new sha is not
// a descendant of origin/loom/<ID>. The push is refused, force-push is forbidden
// by the fleet contract, and the ticket burns two full gate runs before
// dead-ending. Check reports that relationship as a machine-readable verdict.
//
// NO NETWORK, EVER. Nothing here runs `git fetch`, and nothing may be added that
// does — the same rule, and the same reason, as internal/cli/uniondebt: two of
// the union clones use SSH remotes, and every process under the PM2 God Daemon
// fails getpwuid(501), so ssh dies in ~40ms with a message that reads like a
// credentials failure (PUPPET-283). Result.Fetched is therefore hardwired false
// and is printed, so a stale remote-tracking ref is visible rather than silent.
// The caller fetches.
package publishcheck

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Verdict is the relationship between the local branch and its published head.
type Verdict string

const (
	// VerdictUnpublished means no origin/<branch>: an ordinary first publish.
	VerdictUnpublished Verdict = "unpublished"
	// VerdictIdentical means origin == local: the push is a no-op.
	VerdictIdentical Verdict = "identical"
	// VerdictFastForward means origin is an ancestor of local: publishable.
	VerdictFastForward Verdict = "fast-forward"
	// VerdictBehind means local is an ancestor of origin: someone published ahead.
	VerdictBehind Verdict = "behind"
	// VerdictDiverged means neither is an ancestor of the other: the bug this fixes.
	VerdictDiverged Verdict = "diverged"
)

// Result is one publish check. It is the JSON payload of `loom publish-check`.
type Result struct {
	TaskID       string   `json:"task_id"`
	Branch       string   `json:"branch"`               // loom/<ID>, or --branch
	RemoteRef    string   `json:"remote_ref,omitempty"` // origin/loom/<ID>
	LocalSHA     string   `json:"local_sha,omitempty"`
	PublishedSHA string   `json:"published_sha,omitempty"`
	Verdict      Verdict  `json:"verdict"`
	Revisions    []string `json:"revisions,omitempty"`     // existing origin/loom/<ID>-r<N>, ascending
	NextRevision string   `json:"next_revision,omitempty"` // the ref a republish should use
	Detail       string   `json:"detail"`                  // one human sentence
	Fetched      bool     `json:"fetched"`                 // always false; see the package comment
}

// GitRunner runs one git invocation and reports its exit code separately from a
// genuine failure to run. The exit code matters: `git merge-base --is-ancestor`
// uses 1 to mean "not an ancestor", which is a normal result here.
//
// This mirrors uniondebt.gitRunner deliberately rather than importing it: that
// package's doc comment is a no-network contract, and reaching into it from a
// second command would dilute it.
type GitRunner interface {
	Run(dir string, args ...string) (stdout string, exitCode int, err error)
}

type execGitRunner struct{}

func (execGitRunner) Run(dir string, args ...string) (string, int, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // G204 — args are literals plus refs validated by validateRef
	cmd.Dir = dir
	// Keep the check hermetic: the operator's global git config must not change
	// how an ancestry question is answered.
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

// NewGitRunner returns a GitRunner backed by the real git binary.
func NewGitRunner() GitRunner { return execGitRunner{} }

// refPattern mirrors internal/cli/git's validateGitRef, by way of
// internal/cli/uniondebt's copy of it, so a malformed branch name can never
// become a git argument.
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

// Check reports how branch relates to origin/<branch> inside dir.
//
// A missing LOCAL branch is an error, not a verdict: the integrator cannot be
// running without one. A missing REMOTE branch is VerdictUnpublished — the
// ordinary first publish.
func Check(git GitRunner, dir, branch, taskID string) (Result, error) {
	if err := validateRef(branch); err != nil {
		return Result{}, err
	}

	res := Result{TaskID: taskID, Branch: branch, Fetched: false}

	out, code, err := git.Run(dir, "rev-parse", "-q", "--verify", branch)
	if err != nil {
		return Result{}, fmt.Errorf("rev-parse %s in %s: %w", branch, dir, err)
	}
	if code != 0 {
		return Result{}, fmt.Errorf("no local branch %q in %s", branch, dir)
	}
	res.LocalSHA = strings.TrimSpace(out)

	// The revision scan runs on every verdict: an escalation wants to name the
	// next free revision ref whatever the relationship turns out to be.
	if err := scanRevisions(git, dir, branch, &res); err != nil {
		return Result{}, err
	}

	remote := "origin/" + branch
	out, code, err = git.Run(dir, "rev-parse", "-q", "--verify", remote)
	if err != nil {
		return Result{}, fmt.Errorf("rev-parse %s in %s: %w", remote, dir, err)
	}
	if code != 0 {
		res.Verdict = VerdictUnpublished
		res.Detail = fmt.Sprintf("%s does not exist in this clone: %s has never been published from here, so an ordinary push creates it.", remote, branch)
		return res, nil
	}
	res.RemoteRef = remote
	res.PublishedSHA = strings.TrimSpace(out)

	if err := classify(git, dir, &res); err != nil {
		return Result{}, err
	}
	return res, nil
}

// classify answers the ancestry questions and fills Verdict and Detail. It is
// only reached once both shas are known.
func classify(git GitRunner, dir string, res *Result) error {
	if res.PublishedSHA == res.LocalSHA {
		res.Verdict = VerdictIdentical
		res.Detail = fmt.Sprintf("%s already points at %s: the push is a no-op.", res.RemoteRef, short(res.LocalSHA))
		return nil
	}

	ancestor, err := isAncestor(git, dir, res.PublishedSHA, res.LocalSHA)
	if err != nil {
		return err
	}
	if ancestor {
		res.Verdict = VerdictFastForward
		res.Detail = fmt.Sprintf("%s (%s) is an ancestor of %s (%s): an ordinary push publishes it.",
			res.RemoteRef, short(res.PublishedSHA), res.Branch, short(res.LocalSHA))
		return nil
	}

	ancestor, err = isAncestor(git, dir, res.LocalSHA, res.PublishedSHA)
	if err != nil {
		return err
	}
	if ancestor {
		res.Verdict = VerdictBehind
		res.Detail = fmt.Sprintf("%s (%s) is AHEAD of local %s (%s): someone published past you. Fetch and reconcile before pushing; do not force-push.",
			res.RemoteRef, short(res.PublishedSHA), res.Branch, short(res.LocalSHA))
		return nil
	}

	res.Verdict = VerdictDiverged
	base, published, local, err := divergence(git, dir, res.PublishedSHA, res.LocalSHA)
	if err != nil {
		return err
	}
	res.Detail = fmt.Sprintf(
		"diverged at %s: %d commit(s) published, %d local. %s (%s) is not an ancestor of %s (%s), so the push is refused and force-push is forbidden. Publish the work as %s instead.",
		short(base), published, local, res.RemoteRef, short(res.PublishedSHA), res.Branch, short(res.LocalSHA), res.NextRevision)
	return nil
}

// NewCheck runs Check against the real git binary.
func NewCheck(dir, branch, taskID string) (Result, error) {
	return Check(NewGitRunner(), dir, branch, taskID)
}

func isAncestor(git GitRunner, dir, maybeAncestor, descendant string) (bool, error) {
	_, code, err := git.Run(dir, "merge-base", "--is-ancestor", maybeAncestor, descendant)
	if err != nil {
		return false, fmt.Errorf("merge-base --is-ancestor in %s: %w", dir, err)
	}
	return code == 0, nil
}

// divergence names the common ancestor and counts each side's own commits.
func divergence(git GitRunner, dir, published, local string) (base string, aheadPublished, aheadLocal int, err error) {
	out, code, err := git.Run(dir, "merge-base", published, local)
	if err != nil {
		return "", 0, 0, fmt.Errorf("merge-base in %s: %w", dir, err)
	}
	if code != 0 {
		// Unrelated histories have no merge base. That is still divergence; it
		// simply has no base to name.
		return "", 0, 0, nil
	}
	base = strings.TrimSpace(out)
	if aheadPublished, err = countCommits(git, dir, base, published); err != nil {
		return "", 0, 0, err
	}
	if aheadLocal, err = countCommits(git, dir, base, local); err != nil {
		return "", 0, 0, err
	}
	return base, aheadPublished, aheadLocal, nil
}

func countCommits(git GitRunner, dir, base, tip string) (int, error) {
	out, code, err := git.Run(dir, "rev-list", "--count", base+".."+tip)
	if err != nil {
		return 0, fmt.Errorf("rev-list --count in %s: %w", dir, err)
	}
	if code != 0 {
		return 0, fmt.Errorf("rev-list --count %s..%s in %s: exit %d", base, tip, dir, code)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("rev-list --count %s..%s in %s: unparseable count %q", base, tip, dir, strings.TrimSpace(out))
	}
	return n, nil
}

// scanRevisions fills Revisions and NextRevision, on every verdict.
//
// r1 is the unsuffixed ref, so a first republish is -r2. The sort is NUMERIC:
// lexical order would put -r10 before -r2 and hand back a revision number that
// already exists.
func scanRevisions(git GitRunner, dir, branch string, res *Result) error {
	out, code, err := git.Run(dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin/"+branch+"-r*")
	if err != nil {
		return fmt.Errorf("for-each-ref in %s: %w", dir, err)
	}
	if code != 0 {
		return fmt.Errorf("for-each-ref refs/remotes/origin/%s-r* in %s: exit %d", branch, dir, code)
	}

	revPattern := regexp.MustCompile(`^` + regexp.QuoteMeta(branch) + `-r([0-9]+)$`)
	type rev struct {
		n   int
		ref string
	}
	var revs []rev
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		name = strings.TrimPrefix(name, "origin/")
		m := revPattern.FindStringSubmatch(name)
		if m == nil {
			continue // -rc1, -r2x, -review and friends are not revisions
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		revs = append(revs, rev{n: n, ref: name})
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].n < revs[j].n })

	next := 2
	for _, r := range revs {
		res.Revisions = append(res.Revisions, r.ref)
		if r.n >= next {
			next = r.n + 1
		}
	}
	res.NextRevision = fmt.Sprintf("%s-r%d", branch, next)
	return nil
}

func short(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
