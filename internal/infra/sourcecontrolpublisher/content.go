package stackpublish

import (
	"fmt"
	"strings"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
)

// PRMeta carries issue-derived content used to seed a newly created stacked PR's
// title and body. All fields are optional; empty fields fall back to text
// derived from the unit's owned commit.
type PRMeta struct {
	Title              string
	Summary            string
	AcceptanceCriteria string
}

// commitText is the subject (first line) and body of a unit's owned commit — the
// fallback source for a PR's title/body when issue metadata is unavailable.
type commitText struct {
	Subject string
	Body    string
}

// buildPRTitle picks the most human-meaningful title available, in order:
//  1. the issue title,
//  2. the owned commit subject, cleaned up to read as a title,
//  3. the legacy "<stack>: <task>" form as a last resort.
func buildPRTitle(id sl.StackID, taskID string, meta PRMeta, commit commitText) string {
	if t := strings.TrimSpace(meta.Title); t != "" {
		return firstLine(t)
	}
	if s := commitDerivedTitle(commit.Subject, taskID); s != "" {
		return s
	}
	return fmt.Sprintf("%s: %s", id, taskID)
}

// buildPRBody renders the create-time body skeleton for a stacked PR. It
// deliberately omits the loom-stack markers: publish Phase 5 appends the managed
// stack listing below whatever this returns, and preserves it on later runs.
func buildPRBody(taskID string, meta PRMeta, commit commitText, sha string, hasStackDeps bool) string {
	var b strings.Builder

	summary := firstNonEmpty(strings.TrimSpace(meta.Summary), strings.TrimSpace(commit.Body))
	if summary == "" {
		// No prose available; restate the change in one line if we can.
		summary = firstNonEmpty(strings.TrimSpace(meta.Title), commitDerivedTitle(commit.Subject, taskID))
	}
	if summary == "" {
		summary = "_Describe the change._"
	}
	fmt.Fprintf(&b, "## Summary\n\n%s\n", summary)

	fmt.Fprintf(&b, "\n## Owned change\n\n`%s`", taskID)
	if s := strings.TrimSpace(sha); s != "" {
		fmt.Fprintf(&b, " (`%s`)", s)
	}
	b.WriteString(".")
	if hasStackDeps {
		fmt.Fprintf(&b, " The PR diff may include earlier stack dependencies; the change owned by this PR is `%s`.", taskID)
	}
	b.WriteString("\n")

	if ac := strings.TrimSpace(meta.AcceptanceCriteria); ac != "" {
		fmt.Fprintf(&b, "\n## Acceptance criteria\n\n%s\n", ac)
	}
	return b.String()
}

// commitDerivedTitle turns an owned commit's subject into a PR title: the first
// line, with a trailing " (TASKID)" stripped (see stripTaskSuffix).
func commitDerivedTitle(commitSubject, taskID string) string {
	return stripTaskSuffix(firstLine(commitSubject), taskID)
}

// firstLine returns the trimmed first line of s.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// stripTaskSuffix removes a trailing " (TASKID)" from a commit subject. Some
// Loom task commits carry this suffix; stripping it keeps the derived PR title
// from repeating the task ID. A no-op when the suffix isn't present.
func stripTaskSuffix(subject, taskID string) string {
	subject = strings.TrimSpace(subject)
	if taskID == "" {
		return subject
	}
	suffix := "(" + taskID + ")"
	if strings.HasSuffix(subject, suffix) {
		return strings.TrimSpace(strings.TrimSuffix(subject, suffix))
	}
	return subject
}

// shortSHA abbreviates a full commit SHA for display; short or empty input is
// returned unchanged.
func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
