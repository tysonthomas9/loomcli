package trigger

import (
	"errors"
	"testing"
)

// Test vectors are a verbatim port of fleet-db's
// internal/routing/subject_key_test.go — the two implementations are kept in
// lockstep so local/embedded dispatch groups deliveries exactly like fleet-db.
func TestRenderSubjectKey(t *testing.T) {
	inputs := SubjectInputs{
		WorkspaceKey: "TEST",
		BindingID:    "binding-1",
		EventType:    "pull_request.synchronize",
		SubjectRef:   "acme/widgets#42",
		ActorRef:     "github:octocat",
		Attrs: map[string]string{
			"pr_number": "42",
			"repo":      "acme/widgets",
			"empty":     "",
		},
	}
	tests := []struct {
		name     string
		template string
		in       SubjectInputs
		want     string
		wantErr  error
	}{
		{
			name:     "default template uses binding_id|subject_ref",
			template: "",
			in:       inputs,
			want:     "binding-1|acme/widgets#42",
		},
		{
			name:     "blank template is the default template",
			template: "   ",
			in:       inputs,
			want:     "binding-1|acme/widgets#42",
		},
		{
			name:     "default with no subject_ref renders no subject",
			template: "",
			in:       SubjectInputs{BindingID: "binding-1"},
			want:     "",
		},
		{
			name:     "subject_ref token",
			template: "{{subject_ref}}",
			in:       inputs,
			want:     "acme/widgets#42",
		},
		{
			name:     "mixed literal and tokens",
			template: "pr:{{event_type}}:{{subject_ref}}",
			in:       inputs,
			want:     "pr:pull_request.synchronize:acme/widgets#42",
		},
		{
			name:     "attrs token",
			template: "{{attrs.repo}}#{{attrs.pr_number}}",
			in:       inputs,
			want:     "acme/widgets#42",
		},
		{
			name:     "token whitespace is trimmed",
			template: "{{ subject_ref }}",
			in:       inputs,
			want:     "acme/widgets#42",
		},
		{
			name:     "present-but-empty attr renders empty",
			template: "x{{attrs.empty}}y",
			in:       inputs,
			want:     "xy",
		},
		{
			name:     "missing attr is a structured error",
			template: "{{attrs.absent}}",
			in:       inputs,
			wantErr:  ErrMissingSubjectAttr,
		},
		{
			name:     "missing attr with nil attrs map",
			template: "{{attrs.pr_number}}",
			in:       SubjectInputs{BindingID: "binding-1", SubjectRef: "x"},
			wantErr:  ErrMissingSubjectAttr,
		},
		{
			name:     "unknown token rejected",
			template: "{{actor_ref}}",
			in:       inputs,
			wantErr:  ErrInvalidSubjectTemplate,
		},
		{
			name:     "workspace_key is not a renderable token",
			template: "{{workspace_key}}",
			in:       inputs,
			wantErr:  ErrInvalidSubjectTemplate,
		},
		{
			name:     "bare attrs prefix rejected",
			template: "{{attrs.}}",
			in:       inputs,
			wantErr:  ErrInvalidSubjectTemplate,
		},
		{
			name:     "unterminated token rejected",
			template: "{{subject_ref",
			in:       inputs,
			wantErr:  ErrInvalidSubjectTemplate,
		},
		{
			name:     "template rendering blank is rejected",
			template: "{{subject_ref}}",
			in:       SubjectInputs{BindingID: "binding-1"},
			wantErr:  ErrEmptySubjectKey,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderSubjectKey(tt.template, tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RenderSubjectKey(%q) err = %v, want %v", tt.template, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderSubjectKey(%q): %v", tt.template, err)
			}
			if got != tt.want {
				t.Fatalf("RenderSubjectKey(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

// TestRenderSubjectKeyDefaultDisambiguatesBindings pins the collision guard
// the default template provides at the rendered-key level: the same
// subject_ref under two bindings yields distinct keys because the binding id
// is baked into the default key (the stored Redis key additionally prefixes
// workspace and binding id — asserted in the storage key tests).
func TestRenderSubjectKeyDefaultDisambiguatesBindings(t *testing.T) {
	a, err := RenderSubjectKey("", SubjectInputs{BindingID: "binding-a", SubjectRef: "acme/widgets#42"})
	if err != nil {
		t.Fatalf("RenderSubjectKey binding-a: %v", err)
	}
	b, err := RenderSubjectKey("", SubjectInputs{BindingID: "binding-b", SubjectRef: "acme/widgets#42"})
	if err != nil {
		t.Fatalf("RenderSubjectKey binding-b: %v", err)
	}
	if a == b {
		t.Fatalf("default keys collide across bindings: %q", a)
	}
	if a != "binding-a|acme/widgets#42" || b != "binding-b|acme/widgets#42" {
		t.Fatalf("default keys = %q, %q; want binding-prefixed subject refs", a, b)
	}
}
