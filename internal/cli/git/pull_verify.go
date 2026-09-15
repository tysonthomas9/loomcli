package git

// pull_verify.go holds the read-back verification for the pull path.
//
// Core rule: nothing prints ✓ that was not read back from git after the
// mutation. A nil error from a pull step means "no step reported a problem",
// which is not the same claim as "this worktree now contains the source
// branch" — the summary must only ever make the second one.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// syncState is the measured post-pull state of one worktree. Every value other
// than syncStateAdvanced / syncStateAlreadyCurrent must not print as ✓ — the
// whole point of this type is that the summary reports what git says, not what
// the previous step returned.
type syncState int

const (
	syncStateAdvanced       syncState = iota // HEAD moved and now contains the source
	syncStateAlreadyCurrent                  // HEAD already contained the source
	syncStateBehind                          // still behind after the pull — false ✓ caught
	syncStateUnresolved                      // unmerged files / open merge
	syncStateUnverified                      // could not measure (no ref, read failed)
	syncStateFailed                          // a pull step returned an error
	syncStateSkipped                         // never attempted (e.g. no repo metadata)
)

// pullOutcome is what one worktree's pull actually did, as measured afterwards.
type pullOutcome struct {
	Name       string // repo/worktree name as shown in the summary
	Path       string
	Branch     string // branch checked out in that worktree
	Source     string // source branch, e.g. "dev"
	Remote     string // resolved remote, e.g. "origin"
	HeadBefore string // short sha, may be empty if unreadable
	HeadAfter  string
	Behind     int // commits in <remote>/<source> not in HEAD, after the pull
	State      syncState
	Detail     string // free text for ✗/⚠/? lines (error text, reason)
}

// InSync reports whether this outcome is evidence the worktree contains the
// source branch. Only these two states may print ✓.
func (o pullOutcome) InSync() bool {
	return o.State == syncStateAdvanced || o.State == syncStateAlreadyCurrent
}

// failed marks the outcome as a step failure carrying err's text.
func (o pullOutcome) failed(err error) pullOutcome {
	o.State = syncStateFailed
	if err != nil {
		o.Detail = err.Error()
	}
	return o
}

// sourceRef is the ref the pull merged and the ref verification measures
// against — the same one, deliberately.
func (o pullOutcome) sourceRef() string {
	return resolveRemote(o.Remote) + "/" + o.Source
}

// marker is the summary glyph. Distinct glyphs, not shades of ✓, so a reader
// cannot mistake an unverified or skipped line for a success.
func (o pullOutcome) marker() string {
	switch o.State {
	case syncStateAdvanced, syncStateAlreadyCurrent:
		return "✓"
	case syncStateBehind, syncStateFailed:
		return "✗"
	case syncStateUnresolved:
		return "⚠"
	case syncStateUnverified:
		return "?"
	default:
		return "–"
	}
}

// summaryDetail is the measurement (or the reason there is none) printed after
// the name, so a reader can re-check the claim by hand.
func (o pullOutcome) summaryDetail() string {
	switch o.State {
	case syncStateAdvanced:
		return fmt.Sprintf("advanced %s → %s (%s)", gitShortSHA(o.HeadBefore), gitShortSHA(o.HeadAfter), o.sourceRef())
	case syncStateAlreadyCurrent:
		return fmt.Sprintf("already current at %s (%s)", gitShortSHA(o.HeadAfter), o.sourceRef())
	case syncStateUnverified:
		return "unverified: " + o.Detail
	case syncStateSkipped:
		return "skipped: " + o.Detail
	default:
		return o.Detail
	}
}

func gitRevParseDeps(deps *cli.Deps, dir, ref string) (string, error) {
	if err := validateGitRef(ref); err != nil {
		return "", err
	}
	out, err := runGit(deps, dir, "rev-parse", "--verify", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// gitShortSHA is display only — it never feeds a git argument.
func gitShortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

func countCommitsBehindDeps(deps *cli.Deps, dir, ref string) (int, error) {
	if err := validateGitRef(ref); err != nil {
		return 0, err
	}
	out, err := runGit(deps, dir, "rev-list", "--count", "HEAD.."+ref)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("unreadable commit count %q: %w", strings.TrimSpace(out), err)
	}
	return n, nil
}

// mergeInProgressDeps reports whether dir is sitting on an open merge, plus the
// number of conflicted files (for the human-readable detail). Unmerged files are
// checked first because they are the informative case; MERGE_HEAD catches a
// merge that was staged but never committed.
func mergeInProgressDeps(deps *cli.Deps, dir string) (bool, int) {
	if files, err := getConflictedFilesDeps(deps, dir); err == nil && len(files) > 0 {
		return true, len(files)
	}
	if _, err := gitRevParseDeps(deps, dir, "MERGE_HEAD"); err == nil {
		return true, 0
	}
	return false, 0
}

// verifyPulled fills HeadAfter/Behind/State/Detail by reading the worktree back
// from git. It is only called on an outcome whose pull steps reported success —
// its job is to decide whether that report was true.
func verifyPulled(deps *cli.Deps, o *pullOutcome) {
	if open, conflicts := mergeInProgressDeps(deps, o.Path); open {
		o.State = syncStateUnresolved
		if conflicts > 0 {
			o.Detail = fmt.Sprintf("merge unresolved (%d conflicted files)", conflicts)
		} else {
			o.Detail = "merge unresolved (MERGE_HEAD present, merge not committed)"
		}
		return
	}

	ref := o.sourceRef()
	exists, err := remoteBranchExistsDeps(deps, o.Path, o.Remote, o.Source)
	if err != nil || !exists {
		o.State = syncStateUnverified
		o.Detail = "no local ref " + ref + " (fetch may have failed)"
		return
	}

	behind, err := countCommitsBehindDeps(deps, o.Path, ref)
	if err != nil {
		o.State = syncStateUnverified
		o.Detail = fmt.Sprintf("could not measure distance to %s: %v", ref, err)
		return
	}
	o.Behind = behind

	head, err := gitRevParseDeps(deps, o.Path, "HEAD")
	if err != nil {
		o.State = syncStateUnverified
		o.Detail = fmt.Sprintf("could not read HEAD: %v", err)
		return
	}
	o.HeadAfter = head

	if behind > 0 {
		o.State = syncStateBehind
		o.Detail = fmt.Sprintf("still %d commit(s) behind %s after merge", behind, ref)
		return
	}

	if o.HeadAfter != o.HeadBefore {
		o.State = syncStateAdvanced
		return
	}
	o.State = syncStateAlreadyCurrent
}

// summaryFailures counts outcomes that are evidence the pull did not do what it
// claimed. Unverified and skipped are not failures — the run has no evidence
// either way — but they are not ✓ either, which is why they are excluded here
// and still visible in the counts line.
func summaryFailures(outcomes []pullOutcome) int {
	n := 0
	for _, o := range outcomes {
		switch o.State {
		case syncStateBehind, syncStateUnresolved, syncStateFailed:
			n++
		}
	}
	return n
}

// printPullSummary renders the --- Summary --- block. Every line is a
// measurement; notCovered names worktrees this run never visited.
func printPullSummary(outcomes []pullOutcome, notCovered []string) {
	fmt.Println("--- Summary ---")

	width := 0
	for _, o := range outcomes {
		if len(o.Name) > width {
			width = len(o.Name)
		}
	}
	if width < 12 {
		width = 12
	}

	for _, o := range outcomes {
		fmt.Printf("  %s %-*s  %s\n", o.marker(), width, o.Name, o.summaryDetail())
	}

	if len(outcomes) > 0 {
		fmt.Printf("\n  %s\n", summaryCounts(outcomes))
	}

	if len(notCovered) > 0 {
		fmt.Println("  Not covered (loom sync pulls repo checkouts only, not agent worktrees):")
		fmt.Printf("    %s\n", describeNotCovered(notCovered))
	}
}

// summaryCounts exists so a long summary cannot hide a single ✗ among thirty ✓s.
func summaryCounts(outcomes []pullOutcome) string {
	var inSync, failed, unresolved, unverified, skipped int
	for _, o := range outcomes {
		switch o.State {
		case syncStateAdvanced, syncStateAlreadyCurrent:
			inSync++
		case syncStateBehind, syncStateFailed:
			failed++
		case syncStateUnresolved:
			unresolved++
		case syncStateUnverified:
			unverified++
		case syncStateSkipped:
			skipped++
		}
	}

	parts := []string{fmt.Sprintf("%d in sync", inSync)}
	for _, c := range []struct {
		n     int
		label string
	}{
		{failed, "failed"},
		{unresolved, "unresolved"},
		{unverified, "unverified"},
		{skipped, "skipped"},
	} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.label))
		}
	}
	return strings.Join(parts, ", ")
}

func describeNotCovered(names []string) string {
	const shown = 3
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
}
