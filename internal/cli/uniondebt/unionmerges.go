package uniondebt

// The two purely-local, purely-git halves of the pr-pending APPLY test:
// which tasks the union branch carries, and whether a task's work has since
// reached the trunk. Both read the refs the clone already has — the package's
// no-fetch rule (see probe.go) holds here too.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// UnionMerge is one first-parent merge that put a task's branch into a union
// branch. ParentSHA is the merged side — recorded as evidence of WHICH commit
// carried the task in, never used as the key: a `-rN` re-cut means the tip
// that was merged is not necessarily the tip that answers to the task's name
// today. The subject is the key.
type UnionMerge struct {
	TaskID    string
	MergeSHA  string
	ParentSHA string
	Subject   string
}

// unionMergeTaskPattern pulls a task ID out of a union merge SUBJECT. Unlike
// headTaskPattern (prpending_sweep.go), which matches a whole branch name, this
// one is unanchored: integrators have written the ref into at least five
// different sentences —
//
//	local union: merge loom/PUPPET-432 (title…)
//	local union: merge loom/PUPPET-233-r2 (title…)
//	union: PR #619 (loom/PUPPET-432)
//	union: PR #691 (loom/PUPPET-530), re-cut head
//	Merge branch 'loom/PUPPET-233' into local/union
//
// — and anchoring on any one of them would silently miss the rest. The ID
// shape is still the strict one headTaskPattern requires, a letter-led key, a
// dash and digits, so `loom/wip` cannot become a backend lookup.
var unionMergeTaskPattern = regexp.MustCompile(`loom/([A-Za-z][A-Za-z0-9]*-[0-9]+)(?:-r[0-9]+)?\b`)

// errUnionRange marks a union enumeration that could not be performed at all —
// a missing clone, union branch or trunk. It is a sentinel rather than an empty
// result on purpose: an empty merge list reads as "this repo owes nothing",
// which is exactly the answer that must never be produced by accident, because
// the caller WRITES on the strength of it.
var errUnionRange = errors.New("union merge range unreadable")

// errNoTaskRef marks a task whose branch no longer resolves in the clone. The
// union merged something whose branch is gone; that is reported, never
// labeled.
var errNoTaskRef = errors.New("no ref resolves for task")

// UnionMerges lists the tasks unionBranch carries that trunk does not, newest
// merge first per task, sorted by task ID.
//
// The RANGE is the enumeration, not an optimisation on it. A union branch that
// has merged its trunk also carries every `Merge pull request #N from
// …/loom/<ID>` commit the trunk ever landed; enumerating the whole branch would
// count every task ever delivered as union debt. `<trunk>..<union>` drops them
// by construction.
func (p *Prober) UnionMerges(clone, unionBranch, trunk string) ([]UnionMerge, error) {
	if err := validateRef(unionBranch); err != nil {
		return nil, err
	}
	base, err := p.trunkRef(clone, trunk)
	if err != nil {
		return nil, err
	}
	if _, code, err := p.git.Run(clone, "rev-parse", "-q", "--verify", unionBranch); err != nil || code != 0 {
		return nil, fmt.Errorf("%w: union branch %s not found in %s", errUnionRange, unionBranch, clone)
	}

	// %x00 separators: subjects contain spaces, parentheses, quotes and
	// commas, and a NUL is the one byte a commit subject cannot hold.
	out, code, err := p.git.Run(clone, "log", "--first-parent", "--merges",
		"--format=%H%x00%P%x00%s", base+".."+unionBranch)
	if err != nil {
		return nil, fmt.Errorf("git log %s..%s in %s: %w", base, unionBranch, clone, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("%w: git log %s..%s in %s: exit %d", errUnionRange, base, unionBranch, clone, code)
	}
	return parseUnionMerges(out), nil
}

// parseUnionMerges reduces the log to one entry per task, keeping the NEWEST
// merge — git log's first — so a re-cut head's merge is the one on record.
func parseUnionMerges(log string) []UnionMerge {
	seen := map[string]bool{}
	var out []UnionMerge
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\x00")
		if len(fields) != 3 {
			continue
		}
		m := unionMergeTaskPattern.FindStringSubmatch(fields[2])
		if m == nil || seen[m[1]] {
			// A merge with no task ref (`local union: merge origin/v5`, a
			// feature branch, a remote-tracking merge) is ordinary union
			// maintenance, not a missing enumeration. Skip it silently.
			continue
		}
		seen[m[1]] = true
		out = append(out, UnionMerge{
			TaskID:    m[1],
			MergeSHA:  fields[0],
			ParentSHA: secondParent(fields[1]),
			Subject:   fields[2],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

// secondParent is the merged side of a merge commit, or "" for a parent list
// that does not have one.
func secondParent(parents string) string {
	fields := strings.Fields(parents)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// UnionTip is the union branch's current tip, for the report header. A derived
// count without the SHA it was derived from is not evidence: the union branch
// is rebuilt periodically and the same command answers differently before and
// after.
func (p *Prober) UnionTip(clone, unionBranch string) (string, error) {
	if err := validateRef(unionBranch); err != nil {
		return "", err
	}
	out, code, err := p.git.Run(clone, "rev-parse", unionBranch)
	if err != nil {
		return "", fmt.Errorf("rev-parse %s in %s: %w", unionBranch, clone, err)
	}
	if code != 0 {
		return "", fmt.Errorf("%w: rev-parse %s in %s: exit %d", errUnionRange, unionBranch, clone, code)
	}
	return strings.TrimSpace(out), nil
}

// Landed reports whether taskID's work has reached the repo's trunk, and why.
//
// Two legs, because a pull request can land two ways:
//
//   - the trunk contains the branch tip outright (an ordinary merge);
//   - `git cherry` reports every one of the branch's commits as already
//     upstream (a squash or rebase landing).
//
// An EMPTY cherry output is NOT landed. A branch with no commits of its own
// proves nothing, and this answer decides whether a marker gets WRITTEN.
func (p *Prober) Landed(clone, trunk, taskID string) (bool, string, error) {
	if err := validateRef(taskID); err != nil {
		return false, "", err
	}
	base, err := p.trunkRef(clone, trunk)
	if err != nil {
		return false, "", err
	}
	ref, tip, err := p.resolveRef(clone, taskID)
	if err != nil {
		return false, "", err
	}
	if ref == "" {
		return false, "", fmt.Errorf("%w: %s in %s", errNoTaskRef, taskID, clone)
	}

	_, code, err := p.git.Run(clone, "merge-base", "--is-ancestor", ref, base)
	if err != nil {
		return false, "", fmt.Errorf("merge-base in %s: %w", clone, err)
	}
	if code == 0 {
		return true, fmt.Sprintf("%s contains %s (%s)", base, ref, abbrev(tip)), nil
	}
	return p.patchEquivalent(clone, base, ref)
}

// patchEquivalent runs the second landing leg. `git cherry` prefixes a commit
// with '-' when an equivalent change is already upstream and '+' when it is
// not; ALL of them must be '-' for the branch to count as landed.
func (p *Prober) patchEquivalent(clone, base, ref string) (bool, string, error) {
	out, code, err := p.git.Run(clone, "cherry", base, ref)
	if err != nil {
		return false, "", fmt.Errorf("cherry %s %s in %s: %w", base, ref, clone, err)
	}
	if code != 0 {
		return false, "", fmt.Errorf("cherry %s %s in %s: exit %d", base, ref, clone, code)
	}
	total, ahead := 0, 0
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "-"):
			total++
		case strings.HasPrefix(line, "+"):
			total++
			ahead++
		}
	}
	if total > 0 && ahead == 0 {
		return true, fmt.Sprintf("all %d commit(s) of %s are patch-equivalent on %s", total, ref, base), nil
	}
	return false, fmt.Sprintf("%s is not on %s: %d of %d commit(s) are not upstream", ref, base, ahead, total), nil
}

// trunkRef resolves the trunk the same way resolveRef degrades: the
// remote-tracking ref first, the bare local branch second. Neither resolving
// is an ERROR and never "not landed" — that direction writes a marker.
func (p *Prober) trunkRef(clone, trunk string) (string, error) {
	if err := validateRef(trunk); err != nil {
		return "", err
	}
	for _, candidate := range []string{"origin/" + trunk, trunk} {
		_, code, err := p.git.Run(clone, "rev-parse", "-q", "--verify", candidate)
		if err != nil {
			return "", fmt.Errorf("rev-parse %s in %s: %w", candidate, clone, err)
		}
		if code == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: neither origin/%s nor %s resolves in %s", errUnionRange, trunk, trunk, clone)
}

// abbrev shortens an object name for a human sentence.
func abbrev(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
