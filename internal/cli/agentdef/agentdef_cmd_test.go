package agentdef

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// resetHookFlags clears the package-level flag state cobra writes into, so
// tests do not leak values into each other.
func resetHookFlags(t *testing.T) {
	t.Helper()
	agentAddCommentReply, agentAddLabels = false, nil
	agentUpdateCommentReply, agentUpdateLabels, agentUpdateClear = false, nil, false
	t.Cleanup(func() {
		agentAddCommentReply, agentAddLabels = false, nil
		agentUpdateCommentReply, agentUpdateLabels, agentUpdateClear = false, nil, false
	})
}

func TestHooksFromFlags(t *testing.T) {
	tests := []struct {
		name         string
		commentReply bool
		labels       []string
		closeTask    bool
		want         []domain.AgentHookAction
		wantErr      string
	}{
		{
			// The pipeline DOGFOOD-68 exists for: the supervisor stamps, then
			// closes, so the hand-off label lands before the task goes terminal.
			name:         "comment, label, then close",
			commentReply: true,
			labels:       []string{"stage-reviewed"},
			closeTask:    true,
			want: []domain.AgentHookAction{
				{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
				{Type: domain.AgentHookActionAddLabel, Value: "stage-reviewed"},
				{Type: domain.AgentHookActionClose},
			},
		},
		{
			name:      "close alone",
			closeTask: true,
			want: []domain.AgentHookAction{
				{Type: domain.AgentHookActionClose},
			},
		},
		{
			name: "no flags yields no pipeline",
		},
		{
			name:         "comment only",
			commentReply: true,
			want: []domain.AgentHookAction{
				{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
			},
		},
		{
			name:   "labels only",
			labels: []string{"criticized"},
			want:   []domain.AgentHookAction{{Type: domain.AgentHookActionAddLabel, Value: "criticized"}},
		},
		{
			// The comment is always constructed first regardless of flag order,
			// because a label may never precede the artifact it certifies.
			name:         "comment precedes labels, labels keep flag order",
			commentReply: true,
			labels:       []string{"criticized", "reviewed"},
			want: []domain.AgentHookAction{
				{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
				{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
				{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
			},
		},
		{
			name:   "surrounding whitespace is trimmed",
			labels: []string{"  criticized  "},
			want:   []domain.AgentHookAction{{Type: domain.AgentHookActionAddLabel, Value: "criticized"}},
		},
		{
			name:    "blank label is rejected",
			labels:  []string{"  "},
			wantErr: "non-blank label",
		},
		{
			name:    "one blank label among valid ones is rejected",
			labels:  []string{"ok", ""},
			wantErr: "non-blank label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hooksFromFlags(tt.commentReply, tt.labels, tt.closeTask, "")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("hooksFromFlags() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("hooksFromFlags() unexpected error: %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("hooksFromFlags() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("hooksFromFlags() = nil, want a pipeline")
			}
			want := &domain.AgentHooks{OnComplete: tt.want}
			if !got.Equal(want) {
				t.Errorf("hooksFromFlags() = %+v, want %+v", got.OnComplete, tt.want)
			}
			// Whatever the flags produce must satisfy the model contract.
			if err := got.Validate(); err != nil {
				t.Errorf("flags produced an invalid pipeline: %v", err)
			}
		})
	}
}

func TestAgentUpdateHooksPatch(t *testing.T) {
	t.Run("clear yields a non-nil empty pipeline", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateClear = true

		got, err := agentUpdateHooksPatch()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("clear must send a non-nil empty pipeline, not nil (which means no change)")
		}
		if !got.IsEmpty() {
			t.Errorf("clear pipeline = %+v, want empty", got.OnComplete)
		}
	})

	t.Run("set builds the ordered pipeline", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateCommentReply = true
		agentUpdateLabels = []string{"criticized"}

		got, err := agentUpdateHooksPatch()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
			{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
			{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
		}}
		if !got.Equal(want) {
			t.Errorf("patch = %+v, want %+v", got.OnComplete, want.OnComplete)
		}
	})

	t.Run("clear conflicts with the set flags", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			reply  bool
			labels []string
		}{
			{name: "with comment", reply: true},
			{name: "with label", labels: []string{"x"}},
			{name: "with both", reply: true, labels: []string{"x"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				resetHookFlags(t)
				agentUpdateClear = true
				agentUpdateCommentReply = tc.reply
				agentUpdateLabels = tc.labels

				_, err := agentUpdateHooksPatch()
				if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
					t.Fatalf("error = %v, want a conflict error", err)
				}
			})
		}
	})

	t.Run("no flags is an error, not a silent no-op", func(t *testing.T) {
		resetHookFlags(t)

		_, err := agentUpdateHooksPatch()
		if err == nil || !strings.Contains(err.Error(), "nothing to update") {
			t.Fatalf("error = %v, want a no-op error", err)
		}
	})

	t.Run("blank label propagates the validation error", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateLabels = []string{" "}

		if _, err := agentUpdateHooksPatch(); err == nil {
			t.Fatal("expected a blank-label error")
		}
	})
}

func TestAgentCreateFromFlags_CarriesHooks(t *testing.T) {
	resetHookFlags(t)
	agentAddRole = "critic"
	t.Cleanup(func() { agentAddRole = "" })
	agentAddCommentReply = true
	agentAddLabels = []string{"criticized"}

	create, err := agentCreateFromFlags("WS", "critic-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
	}}
	if !create.Hooks.Equal(want) {
		t.Errorf("create.Hooks = %+v, want %+v", create.Hooks, want.OnComplete)
	}
}

func TestAgentCreateFromFlags_NoHooksByDefault(t *testing.T) {
	resetHookFlags(t)
	agentAddRole = "critic"
	t.Cleanup(func() { agentAddRole = "" })

	create, err := agentCreateFromFlags("WS", "plain-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if create.Hooks != nil {
		t.Errorf("create.Hooks = %+v, want nil so existing behavior is preserved", create.Hooks)
	}
}
