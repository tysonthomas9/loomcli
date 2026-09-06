package lead

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

// setLeadListFlags restores every package-level lead flag after the case, so
// the command's globals never leak between tests.
func setLeadListFlags(t *testing.T, list bool, resume string, cont bool, output string) {
	t.Helper()
	prevList, prevResume, prevCont, prevOut := leadListSessions, leadResume, leadContinue, leadListOutput
	t.Cleanup(func() {
		leadListSessions, leadResume, leadContinue, leadListOutput = prevList, prevResume, prevCont, prevOut
	})
	leadListSessions, leadResume, leadContinue, leadListOutput = list, resume, cont, output
}

func TestValidateLeadListFlagsRejectsResumeCombinations(t *testing.T) {
	cases := map[string]struct {
		resume string
		cont   bool
	}{
		"--resume <id>": {resume: "lead-abc"},
		"--continue":    {cont: true},
		"both":          {resume: "lead-abc", cont: true},
	}
	for name, tc := range cases {
		setLeadListFlags(t, true, tc.resume, tc.cont, leadListOutputText)
		err := validateLeadListFlags()
		if err == nil {
			t.Errorf("%s: want a usage error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), "--list-sessions cannot be combined") {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
}

func TestValidateLeadListFlagsOutputFormat(t *testing.T) {
	for _, ok := range []string{"text", "json", "JSON", " text ", ""} {
		setLeadListFlags(t, true, "", false, ok)
		if err := validateLeadListFlags(); err != nil {
			t.Errorf("--output %q should be accepted, got %v", ok, err)
		}
	}
	setLeadListFlags(t, true, "", false, "yaml")
	err := validateLeadListFlags()
	if err == nil || !strings.Contains(err.Error(), "unsupported --output") {
		t.Errorf("--output yaml should be rejected, got %v", err)
	}
}

func sampleLeadRecords() []leadcontrol.LeadSessionRecord {
	started := time.Date(2026, 9, 4, 11, 29, 0, 0, time.UTC)
	finished := started.Add(90 * time.Minute)
	return []leadcontrol.LeadSessionRecord{
		{
			SessionID:     "lead-codex",
			AgentID:       "lead",
			Status:        domain.AgentSessionCompleted,
			StartedAt:     started,
			FinishedAt:    finished,
			Finished:      true,
			WorkDir:       "/work/loomcli",
			Provider:      leadcontrol.RuntimeProviderCodex,
			CodexThreadID: "thread-1",
		},
		{
			SessionID:        "lead-claude",
			AgentID:          "lead",
			Status:           domain.AgentSessionRunning,
			StartedAt:        started.Add(-time.Hour),
			WorkDir:          "/work/loomcli",
			HarnessSessionID: "harness-9",
		},
		{
			SessionID: "lead-nohandle",
			AgentID:   "lead",
			Status:    domain.AgentSessionCompleted,
			StartedAt: started.Add(-2 * time.Hour),
			Finished:  true,
		},
	}
}

func sampleCodexIndex() map[string]leadcontrol.CodexSessionIndexEntry {
	return map[string]leadcontrol.CodexSessionIndexEntry{
		"thread-1": {ID: "thread-1", ThreadName: "Check plan access"},
	}
}

func TestLeadSessionViewsDecoratesWithCodexThreadName(t *testing.T) {
	views := leadSessionViews(sampleLeadRecords(), sampleCodexIndex())
	if len(views) != 3 {
		t.Fatalf("want 3 views, got %d", len(views))
	}
	if views[0].CodexThreadName != "Check plan access" {
		t.Errorf("codex row should carry the thread name, got %q", views[0].CodexThreadName)
	}
	if views[0].ResumeID != "thread-1" {
		t.Errorf("codex row resume id = %q, want the thread id", views[0].ResumeID)
	}
	if views[0].FinishedAt != "2026-09-04 12:59" {
		t.Errorf("finished timestamp = %q", views[0].FinishedAt)
	}
	if views[1].CodexThreadName != "" {
		t.Errorf("a harness row has no codex thread name, got %q", views[1].CodexThreadName)
	}
	if views[1].ResumeID != "harness-9" {
		t.Errorf("harness row resume id = %q", views[1].ResumeID)
	}
	if views[1].FinishedAt != "" {
		t.Errorf("an unfinished row has no finished timestamp, got %q", views[1].FinishedAt)
	}
	if views[2].ResumeID != "" {
		t.Errorf("a row with no provider handle has no resume id, got %q", views[2].ResumeID)
	}
}

func TestLeadSessionViewsWithoutCodexIndex(t *testing.T) {
	views := leadSessionViews(sampleLeadRecords(), nil)
	if views[0].CodexThreadName != "" {
		t.Errorf("no index means no decoration, got %q", views[0].CodexThreadName)
	}
	if views[0].CodexThreadID != "thread-1" {
		t.Errorf("the thread id itself must still be shown, got %q", views[0].CodexThreadID)
	}
}

func renderSample(t *testing.T, format string, records []leadcontrol.LeadSessionRecord) string {
	t.Helper()
	var out bytes.Buffer
	listing := leadSessionListing{
		Workspace: "PUPPET",
		AgentID:   "lead",
		Sessions:  leadSessionViews(records, sampleCodexIndex()),
	}
	if err := renderLeadSessions(&out, listing, format, "/work/loomcli"); err != nil {
		t.Fatalf("render %s: %v", format, err)
	}
	return out.String()
}

func TestRenderLeadSessionsText(t *testing.T) {
	got := renderSample(t, leadListOutputText, sampleLeadRecords())

	for _, want := range []string{
		"SESSION", "RESUME ID", "THREAD", "STATUS", "STARTED", "FINISHED", "WORKDIR",
		"lead-codex", "thread-1", "Check plan access", "2026-09-04 11:29", "2026-09-04 12:59",
		"lead-claude", "harness-9", "/work/loomcli", "lead --resume <RESUME ID>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q:\n%s", want, got)
		}
	}
	// The handle-less row must be visibly un-resumable, not blank.
	if !strings.Contains(got, "lead-nohandle") || !strings.Contains(got, "cannot be resumed") {
		t.Errorf("handle-less row should be listed and explained:\n%s", got)
	}
}

func TestRenderLeadSessionsTextEmpty(t *testing.T) {
	got := renderSample(t, leadListOutputText, nil)
	if !strings.Contains(got, "No lead sessions recorded") || !strings.Contains(got, "PUPPET") {
		t.Errorf("empty listing should say so and name the workspace, got %q", got)
	}
	if strings.Contains(got, "RESUME ID") {
		t.Errorf("an empty listing should print no table header, got %q", got)
	}
}

func TestRenderLeadSessionsJSON(t *testing.T) {
	got := renderSample(t, leadListOutputJSON, sampleLeadRecords())

	var listing leadSessionListing
	if err := json.Unmarshal([]byte(got), &listing); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, got)
	}
	if listing.Workspace != "PUPPET" || listing.AgentID != "lead" {
		t.Errorf("envelope = %+v", listing)
	}
	if len(listing.Sessions) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(listing.Sessions))
	}
	first := listing.Sessions[0]
	if first.SessionID != "lead-codex" || first.CodexThreadName != "Check plan access" {
		t.Errorf("first session = %+v", first)
	}
	if first.Status != string(domain.AgentSessionCompleted) || !first.Finished {
		t.Errorf("status/finished not carried: %+v", first)
	}
	// Keys a script would bind to.
	for _, key := range []string{`"session_id"`, `"resume_id"`, `"codex_thread_name"`, `"harness_session_id"`, `"finished_at"`} {
		if !strings.Contains(got, key) {
			t.Errorf("JSON missing key %s:\n%s", key, got)
		}
	}
}

func TestRenderLeadSessionsJSONEmptyIsAnArray(t *testing.T) {
	got := renderSample(t, leadListOutputJSON, nil)
	if !strings.Contains(got, `"sessions": []`) {
		t.Errorf("empty sessions must marshal as [] so jq keeps working, got %s", got)
	}
}

func TestLeadListSessionsFlagIsRegistered(t *testing.T) {
	if leadCmd.Flags().Lookup("list-sessions") == nil {
		t.Fatal("--list-sessions is not registered on leadCmd")
	}
	out := leadCmd.Flags().Lookup("output")
	if out == nil {
		t.Fatal("--output is not registered on leadCmd")
	}
	if out.DefValue != leadListOutputText {
		t.Errorf("--output default = %q, want %q", out.DefValue, leadListOutputText)
	}
}
