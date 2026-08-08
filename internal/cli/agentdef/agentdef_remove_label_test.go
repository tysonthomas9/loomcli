package agentdef

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestHooksFromFlags_RemoveLabel(t *testing.T) {
	tests := []struct {
		name         string
		commentReply bool
		labels       []string
		removeLabels []string
		closeTask    bool
		want         []domain.AgentHookAction
		wantErr      string
	}{
		{
			name:         "removals only",
			removeLabels: []string{"needs-review"},
			want:         []domain.AgentHookAction{{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"}},
		},
		{
			// The order the whole feature turns on: the artifact lands, the
			// stage's own routing token is consumed, and only then does the
			// certifying stamp publish the hand-off.
			name:         "comment, then removals, then stamps",
			commentReply: true,
			labels:       []string{"reviewed"},
			removeLabels: []string{"needs-review"},
			want: []domain.AgentHookAction{
				{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
				{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
				{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
			},
		},
		{
			name:         "several removals keep flag order",
			removeLabels: []string{"needs-review", "wip"},
			want: []domain.AgentHookAction{
				{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
				{Type: domain.AgentHookActionRemoveLabel, Value: "wip"},
			},
		},
		{
			name:         "close still comes last",
			commentReply: true,
			labels:       []string{"reviewed"},
			removeLabels: []string{"needs-review"},
			closeTask:    true,
			want: []domain.AgentHookAction{
				{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
				{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
				{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
				{Type: domain.AgentHookActionClose},
			},
		},
		{
			name:         "surrounding whitespace is trimmed",
			removeLabels: []string{"  needs-review  "},
			want:         []domain.AgentHookAction{{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"}},
		},
		{
			name:         "blank removal is rejected",
			removeLabels: []string{"  "},
			wantErr:      "--on-complete-remove-label requires a non-blank label",
		},
		{
			name:         "one blank removal among valid ones is rejected",
			removeLabels: []string{"ok", ""},
			wantErr:      "--on-complete-remove-label requires a non-blank label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hooksFromFlags(tt.commentReply, false, tt.labels, tt.removeLabels, "", tt.closeTask, "")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("hooksFromFlags() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("hooksFromFlags() unexpected error: %v", err)
			}
			want := &domain.AgentHooks{OnComplete: tt.want}
			if !got.Equal(want) {
				t.Errorf("hooksFromFlags() = %+v, want %+v", got.OnComplete, tt.want)
			}
			// Whatever the flags produce must satisfy the model contract, or
			// the supervisor's defensive re-validation refuses it at run time.
			if err := got.Validate(); err != nil {
				t.Errorf("flags produced an invalid pipeline: %v", err)
			}
		})
	}
}

// registerHookFlags is what cobra actually binds, so the round trip is asserted
// through a parse rather than by calling hooksFromFlags with hand-built slices:
// a misnamed or non-repeatable flag would pass the unit test and fail the user.
func TestRegisterHookFlags_RemoveLabelRoundTrips(t *testing.T) {
	var reply, writeDesign, closeTask bool
	var labels, removeLabels []string
	var setStatus, cycleSpec string

	cmd := &cobra.Command{Use: "update", RunE: func(*cobra.Command, []string) error { return nil }}
	registerHookFlags(cmd, &reply, &writeDesign, &labels, &removeLabels, &setStatus, &closeTask, &cycleSpec)
	cmd.SetArgs([]string{
		"--on-complete-comment-reply",
		"--on-complete-remove-label", "needs-review",
		"--on-complete-remove-label", "wip",
		"--on-complete-add-label", "reviewed",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	hooks, err := hooksFromFlags(reply, writeDesign, labels, removeLabels, setStatus, closeTask, cycleSpec)
	if err != nil {
		t.Fatalf("hooksFromFlags: %v", err)
	}
	want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		{Type: domain.AgentHookActionRemoveLabel, Value: "wip"},
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
	}}
	if !hooks.Equal(want) {
		t.Errorf("parsed pipeline = %+v, want %+v", hooks.OnComplete, want.OnComplete)
	}
}

// The re-arm hazard is a configuration concern that no code guard can catch —
// the supervisor cannot see which upstream filter keys on the label being
// removed. The only defense is that whoever writes the flag reads about it, so
// the warning living in the help text is part of the feature, not decoration.
func TestRegisterHookFlags_RemoveLabelHelpWarnsAboutReArming(t *testing.T) {
	var reply, writeDesign, closeTask bool
	var labels, removeLabels []string
	var setStatus, cycleSpec string

	cmd := &cobra.Command{Use: "update"}
	registerHookFlags(cmd, &reply, &writeDesign, &labels, &removeLabels, &setStatus, &closeTask, &cycleSpec)

	flag := cmd.Flags().Lookup("on-complete-remove-label")
	if flag == nil {
		t.Fatal("--on-complete-remove-label is not registered")
	}
	for _, phrase := range []string{"re-arms", "loops", "forever"} {
		if !strings.Contains(flag.Usage, phrase) {
			t.Errorf("flag help does not warn about the feeding-stage loop (missing %q): %s", phrase, flag.Usage)
		}
	}
}

func TestAgentUpdateHooksPatch_RemoveLabel(t *testing.T) {
	t.Run("removals alone are a real update", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateRemoveLabels = []string{"needs-review"}

		got, err := agentUpdateHooksPatch(false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
			{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		}}
		if !got.Equal(want) {
			t.Errorf("patch = %+v, want %+v", got.OnComplete, want.OnComplete)
		}
	})

	t.Run("clear conflicts with a removal", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateClear = true
		agentUpdateRemoveLabels = []string{"needs-review"}

		_, err := agentUpdateHooksPatch(false)
		if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("error = %v, want a conflict error", err)
		}
		if !strings.Contains(err.Error(), "--on-complete-remove-label") {
			t.Errorf("error = %v, want it to name the conflicting flag", err)
		}
	})

	t.Run("the no-op error advertises the flag", func(t *testing.T) {
		resetHookFlags(t)

		_, err := agentUpdateHooksPatch(false)
		if err == nil || !strings.Contains(err.Error(), "--on-complete-remove-label") {
			t.Fatalf("error = %v, want the no-op error to list the removal flag", err)
		}
	})

	t.Run("blank removal propagates the validation error", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateRemoveLabels = []string{" "}

		if _, err := agentUpdateHooksPatch(false); err == nil {
			t.Fatal("expected a blank-label error")
		}
	})
}

func TestAgentCreateFromFlags_CarriesRemoveLabelHooks(t *testing.T) {
	resetHookFlags(t)
	agentAddRole = "critic"
	t.Cleanup(func() { agentAddRole = "" })
	agentAddCommentReply = true
	agentAddRemoveLabels = []string{"needs-review"}
	agentAddLabels = []string{"reviewed"}

	create, err := agentCreateFromFlags("WS", "critic-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
	}}
	if !create.Hooks.Equal(want) {
		t.Errorf("create.Hooks = %+v, want %+v", create.Hooks, want.OnComplete)
	}
}
