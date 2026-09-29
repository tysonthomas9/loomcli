package uniondebt

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// revisionPattern builds the anchored matcher for one task's revision refs.
//
// The glob handed to for-each-ref is deliberately loose (`-r*`), so it also
// catches `-rc1`, `-r2x` and `-review`. Only this regex decides what counts:
// `loom/<ID>-r<N>` with nothing after the digits. The ID is quoted because a
// task ID may carry `.` and `-`, which are regex metacharacters.
func revisionPattern(taskID string) *regexp.Regexp {
	return regexp.MustCompile(`^loom/` + regexp.QuoteMeta(taskID) + `-r([0-9]+)$`)
}

// highestRevisionRef returns the remote-tracking ref for the highest-numbered
// republished revision of taskID's branch, or "" when there is none.
//
// A rebuilt branch is republished as loom/<ID>-r2, -r3, … so loom/<ID> alone is
// no longer a reliable key for a task's branch. Revisions are compared
// NUMERICALLY, not lexically: -r10 supersedes -r2, which a string sort would
// get backwards.
func highestRevisionRef(git gitRunner, clone, taskID string) (string, error) {
	glob := "refs/remotes/origin/loom/" + taskID + "-r*"
	out, code, err := git.Run(clone, "for-each-ref", "--format=%(refname:short)", glob)
	if err != nil {
		return "", fmt.Errorf("for-each-ref %s in %s: %w", glob, clone, err)
	}
	if code != 0 {
		// No refs, or a clone git cannot read. Either way there is no revision
		// to prefer; the caller falls back to the unsuffixed candidates.
		return "", nil
	}

	re := revisionPattern(taskID)
	best, bestN := "", -1
	for _, line := range strings.Split(out, "\n") {
		short := strings.TrimSpace(line)
		if short == "" {
			continue
		}
		m := re.FindStringSubmatch(strings.TrimPrefix(short, "origin/"))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			// Only an integer overflow can land here; treat it as no revision
			// rather than letting it outrank every real one.
			continue
		}
		if n > bestN {
			bestN, best = n, short
		}
	}
	return best, nil
}
