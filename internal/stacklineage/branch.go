package stacklineage

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// BranchPrefix is the namespace for every Loom-managed stack branch.
const BranchPrefix = "loom/stack"

// StackBranchPrefix returns the branch-name prefix shared by all units in a
// stack, e.g. "loom/stack/epic-E1/". Used for repo-scoped PR discovery.
func StackBranchPrefix(stackID StackID) string {
	return BranchPrefix + "/" + sanitizeRefSegment(string(stackID)) + "/"
}

// OutputBranchName returns the readable, deterministic branch for a task:
//
//	loom/stack/<sanitized-stack>/<sanitized-task>
//
// Each segment is sanitized to a valid git ref segment. It does NOT add a
// collision suffix; use AssignBranch when registering a node so collisions
// against sibling units in the same stack are resolved and the result persisted.
func OutputBranchName(stackID StackID, taskID string) string {
	return BranchPrefix + "/" + sanitizeRefSegment(string(stackID)) + "/" + sanitizeRefSegment(taskID)
}

// AssignBranch returns the branch to persist for taskID within stackID, avoiding
// collisions with branches already taken by sibling units. Per decision 3, names
// stay human-readable; a short, deterministic hash suffix derived from the raw
// task ID is appended ONLY when the readable name is already taken (e.g. two
// distinct task IDs that sanitize to the same ref). The result is stable for a
// given (taskID, taken set) and must be stored on the Node.
func AssignBranch(stackID StackID, taskID string, taken map[string]struct{}) string {
	readable := OutputBranchName(stackID, taskID)
	if _, clash := taken[readable]; !clash {
		return readable
	}
	suffixed := readable + "-" + shortHash(taskID)
	// Extremely unlikely, but stay correct if the suffixed form also clashes.
	for i := 0; ; i++ {
		if _, clash := taken[suffixed]; !clash {
			return suffixed
		}
		suffixed = readable + "-" + shortHash(taskID+"#"+itoa(i))
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:6]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// sanitizeRefSegment maps an arbitrary string to a single valid git ref path
// segment per git-check-ref-format rules: only [A-Za-z0-9._-] survive (every
// other rune, including '/' and ':', becomes '-'); no leading '-' or '.', no
// trailing '.' or ".lock", no "..", no "@{", non-empty. It is intentionally a
// near 1:1 char map (no run-collapsing) to minimize accidental collisions —
// AssignBranch handles any that remain.
func sanitizeRefSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	out = strings.ReplaceAll(out, "..", "-")        // no ".."
	out = strings.ReplaceAll(out, "@{", "-")        // no "@{"
	out = strings.Trim(out, "-.")                   // no leading/trailing '-' or '.'
	out = strings.TrimSuffix(out, ".lock")          // no trailing ".lock"
	out = strings.Trim(out, "-.")                   // re-trim in case .lock removal exposed one
	if out == "" {
		return "unit"
	}
	return out
}
