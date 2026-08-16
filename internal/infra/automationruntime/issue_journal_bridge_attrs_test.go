// In-package tests for the pure After-snapshot projection. They import nothing
// beyond stdlib, so they sit safely in package trigger (no memstore cycle) and
// can exercise the unexported issueSubjectAttrs directly.
package trigger

import (
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// TestIssueSubjectAttrs is a table over the After-snapshot projection: only
// status/title/repo/created_by are lifted into SubjectAttrs, scalars stringify
// (numeric/bool stay clean), and objects/arrays/null/absent/non-object inputs
// drop out, yielding a nil map when nothing survives.
func TestIssueSubjectAttrs(t *testing.T) {
	tests := []struct {
		name  string
		after string
		want  map[string]string
	}{
		{
			name:  "all four fields lifted, extras ignored",
			after: `{"status":"open","title":"Bug","repo":"acme/app","created_by":"alice","number":42,"body":"x"}`,
			want:  map[string]string{"status": "open", "title": "Bug", "repo": "acme/app", "created_by": "alice"},
		},
		{
			name:  "numeric and bool scalars stringify cleanly",
			after: `{"status":42,"title":true}`,
			want:  map[string]string{"status": "42", "title": "true"},
		},
		{
			name:  "float number keeps its form",
			after: `{"status":1.5}`,
			want:  map[string]string{"status": "1.5"},
		},
		{
			name:  "empty string value is dropped",
			after: `{"status":"","title":"T"}`,
			want:  map[string]string{"title": "T"},
		},
		{
			name:  "objects arrays and null are dropped",
			after: `{"status":{"x":1},"title":[1,2],"repo":null,"created_by":"bob"}`,
			want:  map[string]string{"created_by": "bob"},
		},
		{
			name:  "no recognized keys yields nil",
			after: `{"number":7,"body":"x"}`,
			want:  nil,
		},
		{
			name:  "non-object after yields nil",
			after: `"just a string"`,
			want:  nil,
		},
		{
			name:  "malformed after yields nil",
			after: `{not json`,
			want:  nil,
		},
		{
			name:  "empty after yields nil",
			after: ``,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw json.RawMessage
			if tt.after != "" {
				raw = json.RawMessage(tt.after)
			}
			got := issueSubjectAttrs(raw)
			if len(got) != len(tt.want) {
				t.Fatalf("issueSubjectAttrs(%s) = %v, want %v", tt.after, got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("issueSubjectAttrs(%s)[%q] = %q, want %q", tt.after, k, got[k], v)
				}
			}
		})
	}
}

// TestToInternalEventShape pins the journal->loopback mapping: deterministic
// event id, system origin, depth-0 root (no parent), verbatim actor, namespaced
// subject ref, and the After snapshot as the emitter payload.
func TestToInternalEventShape(t *testing.T) {
	b := &IssueJournalBridge{}
	got := b.toInternalEvent(automation.JournalEvent{
		ID:       "1707-0",
		Action:   "issue.create",
		Actor:    "user:alice",
		EntityID: "42",
		After:    json.RawMessage(`{"status":"open","title":"T"}`),
	})
	if got.EventID != "fleet-journal-1707-0" {
		t.Fatalf("EventID = %q, want fleet-journal-1707-0", got.EventID)
	}
	if got.EventType != "issue.create" {
		t.Fatalf("EventType = %q, want issue.create (normalization happens in Emit)", got.EventType)
	}
	if got.Origin != automation.EventOriginSystem {
		t.Fatalf("Origin = %q, want system", got.Origin)
	}
	if got.ParentEventID != "" {
		t.Fatalf("ParentEventID = %q, want empty (depth-0 root)", got.ParentEventID)
	}
	if got.ActorRef != "user:alice" {
		t.Fatalf("ActorRef = %q, want verbatim journal actor", got.ActorRef)
	}
	if got.SubjectRef != "issue:42" {
		t.Fatalf("SubjectRef = %q, want issue:42", got.SubjectRef)
	}
	if got.SubjectAttrs["status"] != "open" || got.SubjectAttrs["title"] != "T" {
		t.Fatalf("SubjectAttrs = %v, want status/title from After", got.SubjectAttrs)
	}
	if string(got.Payload) != `{"status":"open","title":"T"}` {
		t.Fatalf("Payload = %s, want the After snapshot", got.Payload)
	}
}
