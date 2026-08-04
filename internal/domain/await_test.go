package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestValidateAwaitPattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{"subject-scoped key ok", "github.pr.merged:repo/owner/123", false},
		{"multi-colon subject ok", "approval.granted:deploy:prod", false},
		{"literal star is exact-match data, not glob", "github.pr.merged:repo/*", false},
		{"bare event type rejected", "github.pr.merged", true},
		{"empty subject rejected", "github.pr.merged:", true},
		{"whitespace subject rejected", "github.pr.merged:   ", true},
		{"empty event type rejected", ":repo/owner/123", true},
		{"empty pattern rejected", "", true},
		{"lone colon rejected", ":", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAwaitPattern(tc.pattern)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("ValidateAwaitPattern(%q) = %v, want nil", tc.pattern, err)
				}
				return
			}
			if !errors.Is(err, ErrAwaitPatternUnscoped) {
				t.Fatalf("ValidateAwaitPattern(%q) = %v, want ErrAwaitPatternUnscoped", tc.pattern, err)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ErrAwaitPatternUnscoped must wrap ErrInvalid; got %v", err)
			}
		})
	}
}

func TestParseAwaitInstanceKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		key       string
		wantRunID string
		wantN     int
		wantErr   bool
	}{
		{"first await", "run-1#await-1", "run-1", 1, false},
		{"later ordinal", "dr_abc#await-12", "dr_abc", 12, false},
		{"run id containing separator splits on last", "a#await-2#await-7", "a#await-2", 7, false},
		{"empty key", "", "", 0, true},
		{"no separator", "run-1", "", 0, true},
		{"empty run id", "#await-1", "", 0, true},
		{"ordinal zero", "run#await-0", "", 0, true},
		{"negative ordinal", "run#await--1", "", 0, true},
		{"non-numeric ordinal", "run#await-x", "", 0, true},
		{"missing ordinal", "run#await-", "", 0, true},
		{"non-canonical leading zero", "run#await-01", "", 0, true},
		{"non-canonical plus sign", "run#await-+1", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runID, n, err := ParseAwaitInstanceKey(tc.key)
			if tc.wantErr {
				if !errors.Is(err, ErrAwaitInstanceKeyMalformed) || !errors.Is(err, ErrInvalid) {
					t.Fatalf("ParseAwaitInstanceKey(%q) err = %v, want ErrAwaitInstanceKeyMalformed wrapping ErrInvalid", tc.key, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAwaitInstanceKey(%q) = %v, want nil", tc.key, err)
			}
			if runID != tc.wantRunID || n != tc.wantN {
				t.Fatalf("ParseAwaitInstanceKey(%q) = (%q, %d), want (%q, %d)", tc.key, runID, n, tc.wantRunID, tc.wantN)
			}
			if got := AwaitInstanceKey(runID, n); got != tc.key {
				t.Fatalf("AwaitInstanceKey round-trip = %q, want %q", got, tc.key)
			}
		})
	}
}

// validAwait returns a registration-valid instance relative to now;
// mutate per case.
func validAwait(now time.Time) AwaitInstance {
	return AwaitInstance{
		WorkspaceKey: "ws-1",
		InstanceKey:  "run-1#await-1",
		RunID:        "run-1",
		Pattern:      "github.pr.merged:repo/owner/123",
		Deadline:     now.Add(time.Hour),
		RegisteredAt: now,
		Status:       AwaitPending,
	}
}

func TestAwaitInstanceValidateAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		mutate  func(*AwaitInstance)
		wantErr error // nil means valid
	}{
		{"valid pending await", func(a *AwaitInstance) {}, nil},
		{"missing workspace", func(a *AwaitInstance) { a.WorkspaceKey = "" }, ErrInvalid},
		{"missing run id", func(a *AwaitInstance) { a.RunID = "" }, ErrInvalid},
		{"malformed instance key", func(a *AwaitInstance) { a.InstanceKey = "run-1" }, ErrAwaitInstanceKeyMalformed},
		{"ordinal below one", func(a *AwaitInstance) { a.InstanceKey = "run-1#await-0" }, ErrAwaitInstanceKeyMalformed},
		{"key belongs to other run", func(a *AwaitInstance) { a.InstanceKey = "run-2#await-1" }, ErrAwaitInstanceKeyMalformed},
		{"unscoped pattern", func(a *AwaitInstance) { a.Pattern = "github.pr.merged" }, ErrAwaitPatternUnscoped},
		{"empty pattern", func(a *AwaitInstance) { a.Pattern = "" }, ErrAwaitPatternUnscoped},
		{"unknown status", func(a *AwaitInstance) { a.Status = "unknown" }, ErrInvalid},
		{"zero deadline", func(a *AwaitInstance) { a.Deadline = time.Time{} }, ErrAwaitTimeoutRequired},
		{"past deadline", func(a *AwaitInstance) { a.Deadline = now.Add(-time.Minute) }, ErrAwaitTimeoutRequired},
		{"deadline exactly now", func(a *AwaitInstance) { a.Deadline = now }, ErrAwaitTimeoutRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := validAwait(now)
			tc.mutate(&a)
			err := a.ValidateAt(now)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateAt() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateAt() = %v, want %v", err, tc.wantErr)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("await validation errors must wrap ErrInvalid; got %v", err)
			}
		})
	}
}

func TestAwaitStatusEnum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status   AwaitStatus
		valid    bool
		terminal bool
	}{
		{AwaitPending, true, false},
		{AwaitSatisfied, true, true},
		{AwaitTimedOut, true, true},
		{AwaitCancelled, true, true},
		{AwaitStatus("unknown"), false, false},
		{AwaitStatus(""), false, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			t.Parallel()
			if got := tc.status.IsValid(); got != tc.valid {
				t.Errorf("IsValid() = %v, want %v", got, tc.valid)
			}
			if got := tc.status.IsTerminal(); got != tc.terminal {
				t.Errorf("IsTerminal() = %v, want %v", got, tc.terminal)
			}
		})
	}
}

func TestDriverRunStatusSuspendedNotTerminal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status   DriverRunStatus
		terminal bool
	}{
		{DriverRunSuspendedAwaitingEvent, false},
		{DriverRunQueued, false},
		{DriverRunRunning, false},
		{DriverRunCompleted, true},
		{DriverRunFailed, true},
		{DriverRunNeedsReview, true},
		{DriverRunCancelled, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			t.Parallel()
			if got := tc.status.IsTerminal(); got != tc.terminal {
				t.Errorf("IsTerminal(%s) = %v, want %v", tc.status, got, tc.terminal)
			}
		})
	}
	if got := string(DriverRunSuspendedAwaitingEvent); got != "suspended_awaiting_event" {
		t.Errorf("DriverRunSuspendedAwaitingEvent = %q, want suspended_awaiting_event", got)
	}
}

func TestAwaitInstanceJSONRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	resumed := now.Add(2 * time.Minute)
	in := AwaitInstance{
		WorkspaceKey:       "ws-1",
		InstanceKey:        "run-1#await-2",
		RunID:              "run-1",
		Pattern:            "approval.granted:deploy/prod",
		ActorAllow:         []string{"user:tyson", "team:platform"},
		Deadline:           now.Add(time.Hour),
		RegisteredAt:       now,
		Status:             AwaitSatisfied,
		SatisfiedByEventID: "evt-9",
		SatisfiedPayload:   json.RawMessage(`{"approved":true}`),
		ResumedAt:          &resumed,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	for _, want := range []string{
		"workspaceKey", "instanceKey", "runID", "pattern", "actorAllow",
		"deadline", "registeredAt", "status", "satisfiedByEventID",
		"satisfiedPayload", "resumedAt",
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("marshaled AwaitInstance missing camelCase key %q (got %s)", want, raw)
		}
	}
	var out AwaitInstance
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal round-trip: %v", err)
	}
	if out.InstanceKey != in.InstanceKey || out.Status != in.Status ||
		out.SatisfiedByEventID != in.SatisfiedByEventID ||
		string(out.SatisfiedPayload) != string(in.SatisfiedPayload) ||
		!out.Deadline.Equal(in.Deadline) || out.ResumedAt == nil || !out.ResumedAt.Equal(resumed) {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestDriverRunCompositionFieldsJSON(t *testing.T) {
	t.Parallel()
	suspended := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	run := DriverRun{
		WorkspaceKey:        "ws-1",
		RunID:               "run-2",
		DriverID:            "drv-1",
		DriverVersionID:     "dv-1",
		Status:              DriverRunSuspendedAwaitingEvent,
		ParentRunID:         "run-1",
		SuspendedAt:         &suspended,
		ResumeSourceEventID: "evt-9",
	}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	// snake_case like every other DriverRun field (AW5): the fleet-db
	// client decodes v1 responses directly into DriverRun, so the tags ARE
	// the HTTP wire contract; the driver/watch wire carries runs through
	// its own DTOs (internal/driver/run_events.go).
	for _, want := range []string{"parent_run_id", "suspended_at", "resume_source_event_id"} {
		if _, ok := keys[want]; !ok {
			t.Errorf("marshaled DriverRun missing snake_case key %q (got %s)", want, raw)
		}
	}
	for _, reject := range []string{"parentRunID", "suspendedAt", "resumeSourceEventID"} {
		if _, ok := keys[reject]; ok {
			t.Errorf("marshaled DriverRun has camelCase key %q; DriverRun tags are the fleet-db v1 wire", reject)
		}
	}
	var out DriverRun
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal round-trip: %v", err)
	}
	if out.ParentRunID != run.ParentRunID || out.ResumeSourceEventID != run.ResumeSourceEventID ||
		out.SuspendedAt == nil || !out.SuspendedAt.Equal(suspended) {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, run)
	}
	// Detached child = no ParentRunID: omitempty keeps the wire clean.
	detached, err := json.Marshal(DriverRun{WorkspaceKey: "ws-1", RunID: "run-3", Status: DriverRunQueued})
	if err != nil {
		t.Fatalf("Marshal detached: %v", err)
	}
	var detachedKeys map[string]json.RawMessage
	if err := json.Unmarshal(detached, &detachedKeys); err != nil {
		t.Fatalf("Unmarshal detached: %v", err)
	}
	for _, reject := range []string{"parentRunID", "suspendedAt", "resumeSourceEventID"} {
		if _, ok := detachedKeys[reject]; ok {
			t.Errorf("detached run must omit %q", reject)
		}
	}
}
