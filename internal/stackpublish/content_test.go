package stackpublish

import (
	"strings"
	"testing"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

func TestBuildPRTitle(t *testing.T) {
	id := sl.StackID("epic-E1")
	tests := []struct {
		name          string
		taskID        string
		issueTitle    string
		commitSubject string
		want          string
	}{
		{"issue title preferred over commit", "T-1", "Add merge command", "different subject (T-1)", "Add merge command"},
		{"commit fallback strips task suffix", "T-1", "", "Add stack merge command (T-1)", "Add stack merge command"},
		{"commit fallback uses first line only", "T-1", "", "Subject line\n\nlong body here", "Subject line"},
		{"legacy fallback when both empty", "T-1", "", "", "epic-E1: T-1"},
		{"issue title is whitespace-trimmed", "T-1", "  Padded title  ", "", "Padded title"},
		{"mismatched suffix is left intact", "T-1", "", "Do thing (OTHER-9)", "Do thing (OTHER-9)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPRTitle(id, tt.taskID, PRMeta{Title: tt.issueTitle}, commitText{Subject: tt.commitSubject})
			if got != tt.want {
				t.Fatalf("buildPRTitle = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildPRBodyIssueSourced(t *testing.T) {
	body := buildPRBody("T-2",
		PRMeta{Title: "Issue Title", Summary: "Issue summary text.", AcceptanceCriteria: "- must pass CI"},
		commitText{Subject: "Commit subj (T-2)", Body: "commit body"},
		"abcdef123456", true)

	for _, want := range []string{
		"## Summary",
		"Issue summary text.", // issue Description wins over commit body
		"## Owned change",
		"`T-2`",
		"(`abcdef123456`)",
		"earlier stack dependencies", // hasStackDeps=true
		"## Acceptance criteria",
		"- must pass CI",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n---\n%s", want, body)
		}
	}
	// Phase 5 owns the managed listing — the create body must not embed markers.
	if strings.Contains(body, "loom-stack:start") {
		t.Errorf("create body must not embed managed stack markers:\n%s", body)
	}
}

func TestBuildPRBodyRootUnit(t *testing.T) {
	// Root unit: no parent, no acceptance criteria, commit body seeds the summary.
	body := buildPRBody("T-1", PRMeta{},
		commitText{Subject: "Root commit", Body: "root commit body"},
		"deadbeef", false)

	if !strings.Contains(body, "root commit body") {
		t.Errorf("expected commit body as summary fallback:\n%s", body)
	}
	if strings.Contains(body, "earlier stack dependencies") {
		t.Errorf("root unit must not mention stack dependencies:\n%s", body)
	}
	if strings.Contains(body, "## Acceptance criteria") {
		t.Errorf("root unit without criteria must omit the section:\n%s", body)
	}
}

func TestBuildPRBodyPlaceholderSummary(t *testing.T) {
	body := buildPRBody("T-3", PRMeta{}, commitText{}, "", false)
	if !strings.Contains(body, "_Describe the change._") {
		t.Errorf("expected placeholder summary when no source is available:\n%s", body)
	}
}

func TestStripTaskSuffix(t *testing.T) {
	tests := []struct {
		subject, taskID, want string
	}{
		{"Do thing (T-1)", "T-1", "Do thing"},
		{"Do thing", "T-1", "Do thing"},
		{"Do thing (OTHER-9)", "T-1", "Do thing (OTHER-9)"},
		{"Do thing (T-1)", "", "Do thing (T-1)"},
	}
	for _, tt := range tests {
		if got := stripTaskSuffix(tt.subject, tt.taskID); got != tt.want {
			t.Errorf("stripTaskSuffix(%q, %q) = %q, want %q", tt.subject, tt.taskID, got, tt.want)
		}
	}
}

func TestShortSHA(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abcdef1234567890", "abcdef123456"},
		{"deadbeef", "deadbeef"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := shortSHA(tt.in); got != tt.want {
			t.Errorf("shortSHA(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
