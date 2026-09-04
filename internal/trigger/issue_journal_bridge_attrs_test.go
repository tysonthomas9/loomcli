// In-package tests for the pure After-snapshot projection. They import nothing
// beyond stdlib, so they sit safely in package trigger (no memstore cycle) and
// can exercise the unexported issueSubjectAttrs directly.
package trigger

import (
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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
	got := b.toInternalEvent(store.JournalEvent{
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
	if got.Origin != domain.TriggerEventOriginSystem {
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

// TestIssueSubjectAttrsDropsLabelArray pins why the label attr comes from
// event METADATA and not from the snapshot: "labels" is not in
// issueSubjectAttrKeys, and even if it were, scalarString rejects arrays — so
// no {{attrs.labels}} token can ever render off a snapshot. The single-label
// scalar lane is issueEventSubjectAttrs, which reads fleet-db's per-label event
// metadata. Relaxing THIS projection is not the way to widen that.
func TestIssueSubjectAttrsDropsLabelArray(t *testing.T) {
	after := []byte(`{"status":"open","title":"T","labels":["needs-review","p1"]}`)
	got := issueSubjectAttrs(after)
	if _, ok := got["labels"]; ok {
		t.Fatalf("issueSubjectAttrs kept labels = %v, want the array dropped", got)
	}
	if got["status"] != "open" || got["title"] != "T" {
		t.Fatalf("issueSubjectAttrs = %v, want the scalar fields still lifted", got)
	}
}

// TestIssueEventSubjectAttrsLiftsLabelMetadata pins the {{attrs.label}} lane:
// the label is read from fleet-db's event METADATA, the only place it appears
// as a scalar. The snapshot's labels array stays dropped (see
// TestIssueSubjectAttrsDropsLabelArray), so metadata is not a convenience — it
// is the sole route from a label to a subject-key template.
func TestIssueEventSubjectAttrsLiftsLabelMetadata(t *testing.T) {
	labelEvent := func(action string, meta map[string]string) store.JournalEvent {
		return store.JournalEvent{
			ID: "40", Action: action, Actor: "daemon", EntityID: "DOGFOOD-42",
			After:    json.RawMessage(`{"status":"open","labels":["p1","needs-review"]}`),
			Metadata: meta,
		}
	}

	for _, action := range []string{IssueLabelAddAction, IssueLabelRemoveAction} {
		got := issueEventSubjectAttrs(labelEvent(action, map[string]string{"label": "needs-review"}))
		if got[IssueLabelSubjectAttr] != "needs-review" {
			t.Fatalf("%s attrs = %v, want label=needs-review", action, got)
		}
		// The scalar snapshot fields still ride alongside.
		if got["status"] != "open" {
			t.Fatalf("%s attrs = %v, want status carried too", action, got)
		}
	}

	// A non-label action never grows a label attr, even though its snapshot
	// carries the same labels array.
	if got := issueEventSubjectAttrs(labelEvent("issue.update", map[string]string{"label": "needs-review"})); got[IssueLabelSubjectAttr] != "" {
		t.Fatalf("non-label action attrs = %v, want no label attr", got)
	}

	// Missing/blank metadata contributes NO attr rather than an empty one, so a
	// template naming it falls back to the default subject key instead of
	// collapsing unrelated deliveries onto "".
	for _, meta := range []map[string]string{nil, {}, {"label": "   "}} {
		got := issueEventSubjectAttrs(labelEvent(IssueLabelAddAction, meta))
		if _, ok := got[IssueLabelSubjectAttr]; ok {
			t.Fatalf("attrs for metadata %v = %v, want no label key", meta, got)
		}
	}
}
