package agentdef

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func designAction() domain.AgentHookAction {
	return domain.AgentHookAction{
		Type:   domain.AgentHookActionWriteDesign,
		Source: domain.AgentHookCommentSourceFinalReply,
	}
}

func commentAction() domain.AgentHookAction {
	return domain.AgentHookAction{
		Type:   domain.AgentHookActionComment,
		Source: domain.AgentHookCommentSourceFinalReply,
	}
}

func TestHooksFromFlags_WriteDesignAndSetStatus(t *testing.T) {
	tests := []struct {
		name         string
		commentReply bool
		writeDesign  bool
		labels       []string
		removeLabels []string
		setStatus    string
		closeTask    bool
		want         []domain.AgentHookAction
		wantErr      string
	}{
		{
			name:        "design only",
			writeDesign: true,
			want:        []domain.AgentHookAction{designAction()},
		},
		{
			name:      "status only",
			setStatus: "review",
			want:      []domain.AgentHookAction{{Type: domain.AgentHookActionSetStatus, Value: "review"}},
		},
		{
			// The order the whole feature turns on. The design goes ahead of the
			// comment because both draw on the same reply and only the design
			// write is idempotent: a later failure retries the WHOLE pipeline, so
			// the replaceable write belongs first and the append-only one last.
			name:         "design, comment, removals, stamps, then the status",
			commentReply: true,
			writeDesign:  true,
			labels:       []string{"planned"},
			removeLabels: []string{"needs-plan"},
			setStatus:    "open",
			want: []domain.AgentHookAction{
				designAction(),
				commentAction(),
				{Type: domain.AgentHookActionRemoveLabel, Value: "needs-plan"},
				{Type: domain.AgentHookActionAddLabel, Value: "planned"},
				{Type: domain.AgentHookActionSetStatus, Value: "open"},
			},
		},
		{
			name:        "close still comes last",
			writeDesign: true,
			setStatus:   "review",
			closeTask:   true,
			want: []domain.AgentHookAction{
				designAction(),
				{Type: domain.AgentHookActionSetStatus, Value: "review"},
				{Type: domain.AgentHookActionClose},
			},
		},
		{
			name:      "blocked carries its reason",
			setStatus: "blocked:upstream API decision pending",
			want: []domain.AgentHookAction{{
				Type:   domain.AgentHookActionSetStatus,
				Value:  "blocked",
				Reason: "upstream API decision pending",
			}},
		},
		{
			// Split on the FIRST colon only, so a reason keeps colons of its own.
			name:      "a reason may contain colons",
			setStatus: "blocked:waiting on infra: the new cluster",
			want: []domain.AgentHookAction{{
				Type:   domain.AgentHookActionSetStatus,
				Value:  "blocked",
				Reason: "waiting on infra: the new cluster",
			}},
		},
		{
			name:      "surrounding whitespace is trimmed",
			setStatus: "  blocked  :  upstream API decision pending  ",
			want: []domain.AgentHookAction{{
				Type:   domain.AgentHookActionSetStatus,
				Value:  "blocked",
				Reason: "upstream API decision pending",
			}},
		},

		// The refusals. Each one is the server's own PATCH contract reaching the
		// CLI, so an operator is told where the write actually lives rather than
		// storing a pipeline that 400s on every run.
		{
			name:      "closed points at the close flag's endpoint",
			setStatus: "closed",
			wantErr:   "status closed must use close endpoint",
		},
		{
			name:      "in_progress points at the claim endpoint",
			setStatus: "in_progress",
			wantErr:   "status in_progress must use claim endpoint",
		},
		{
			name:      "system-managed statuses are refused",
			setStatus: "tombstone",
			wantErr:   "status tombstone is system-managed",
		},
		{
			name:      "an unknown status is refused",
			setStatus: "waiting",
			wantErr:   `invalid status "waiting"`,
		},
		{
			name:      "blocked without a reason is refused",
			setStatus: "blocked",
			wantErr:   "set_status action to blocked requires a non-blank reason",
		},
		{
			name:      "blocked with a blank reason is refused",
			setStatus: "blocked:   ",
			wantErr:   "set_status action to blocked requires a non-blank reason",
		},
		{
			name:      "a reason on any other status is refused",
			setStatus: "review:looks good",
			wantErr:   "must not set reason for status \"review\"",
		},
		{
			name:      "a blank spec is refused",
			setStatus: "  :why",
			wantErr:   "--on-complete-set-status expects STATUS[:REASON]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hooksFromFlags(tt.commentReply, tt.writeDesign, tt.labels, tt.removeLabels,
				tt.setStatus, tt.closeTask, "")
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
			// Whatever the flags produce must satisfy the model contract, or the
			// supervisor's defensive re-validation refuses it at run time.
			if err := got.Validate(); err != nil {
				t.Errorf("flags produced an invalid pipeline: %v", err)
			}
		})
	}
}

// registerHookFlags is what cobra actually binds, so the round trip is asserted
// through a parse rather than by calling hooksFromFlags with hand-built values:
// a misnamed flag, or one that swallowed the reason at the wrong colon, would
// pass the unit test and fail the user.
func TestRegisterHookFlags_StatusAndDesignRoundTrip(t *testing.T) {
	var reply, writeDesign, closeTask bool
	var labels, removeLabels []string
	var setStatus, cycleSpec string

	cmd := &cobra.Command{Use: "update", RunE: func(*cobra.Command, []string) error { return nil }}
	registerHookFlags(cmd, &reply, &writeDesign, &labels, &removeLabels, &setStatus, &closeTask, &cycleSpec)
	cmd.SetArgs([]string{
		"--on-complete-write-design",
		"--on-complete-comment-reply",
		"--on-complete-add-label", "planned",
		"--on-complete-set-status", "blocked:upstream API decision pending",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	hooks, err := hooksFromFlags(reply, writeDesign, labels, removeLabels, setStatus, closeTask, cycleSpec)
	if err != nil {
		t.Fatalf("hooksFromFlags: %v", err)
	}
	want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		designAction(),
		commentAction(),
		{Type: domain.AgentHookActionAddLabel, Value: "planned"},
		{Type: domain.AgentHookActionSetStatus, Value: "blocked", Reason: "upstream API decision pending"},
	}}
	if !hooks.Equal(want) {
		t.Errorf("parsed pipeline = %+v, want %+v", hooks.OnComplete, want.OnComplete)
	}
}

// The flag help is where an operator learns the two rules no code can enforce
// for them: which statuses this flag cannot express, and that blocked needs a
// reason. Both are configuration-time decisions, so the text is part of the
// feature.
func TestRegisterHookFlags_SetStatusHelpNamesTheContract(t *testing.T) {
	var reply, writeDesign, closeTask bool
	var labels, removeLabels []string
	var setStatus, cycleSpec string

	cmd := &cobra.Command{Use: "update"}
	registerHookFlags(cmd, &reply, &writeDesign, &labels, &removeLabels, &setStatus, &closeTask, &cycleSpec)

	flag := cmd.Flags().Lookup("on-complete-set-status")
	if flag == nil {
		t.Fatal("--on-complete-set-status is not registered")
	}
	for _, phrase := range []string{"open", "review", "deferred", "blocked", "in_progress", "claim", "--on-complete-close", "REQUIRES a reason"} {
		if !strings.Contains(flag.Usage, phrase) {
			t.Errorf("flag help does not mention %q: %s", phrase, flag.Usage)
		}
	}
	if cmd.Flags().Lookup("on-complete-write-design") == nil {
		t.Error("--on-complete-write-design is not registered")
	}
}

func TestAgentUpdateHooksPatch_StatusAndDesign(t *testing.T) {
	t.Run("a design write alone is a real update", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateWriteDesign = true

		got, err := agentUpdateHooksPatch()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{designAction()}}
		if !got.Equal(want) {
			t.Errorf("patch = %+v, want %+v", got.OnComplete, want.OnComplete)
		}
	})

	t.Run("a status alone is a real update", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateSetStatus = "review"

		got, err := agentUpdateHooksPatch()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
			{Type: domain.AgentHookActionSetStatus, Value: "review"},
		}}
		if !got.Equal(want) {
			t.Errorf("patch = %+v, want %+v", got.OnComplete, want.OnComplete)
		}
	})

	t.Run("clear conflicts with either", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			set  func()
			flag string
		}{
			{name: "design", set: func() { agentUpdateWriteDesign = true }, flag: "--on-complete-write-design"},
			{name: "status", set: func() { agentUpdateSetStatus = "review" }, flag: "--on-complete-set-status"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				resetHookFlags(t)
				agentUpdateClear = true
				tt.set()

				_, err := agentUpdateHooksPatch()
				if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
					t.Fatalf("error = %v, want a conflict error", err)
				}
				if !strings.Contains(err.Error(), tt.flag) {
					t.Errorf("error = %v, want it to name %s", err, tt.flag)
				}
			})
		}
	})

	t.Run("the no-op error advertises both flags", func(t *testing.T) {
		resetHookFlags(t)

		_, err := agentUpdateHooksPatch()
		if err == nil {
			t.Fatal("expected the no-op error")
		}
		for _, flag := range []string{"--on-complete-write-design", "--on-complete-set-status"} {
			if !strings.Contains(err.Error(), flag) {
				t.Errorf("error = %v, want the no-op error to list %s", err, flag)
			}
		}
	})

	t.Run("an unsettable status propagates the contract error", func(t *testing.T) {
		resetHookFlags(t)
		agentUpdateSetStatus = "closed"

		if _, err := agentUpdateHooksPatch(); err == nil ||
			!strings.Contains(err.Error(), "close endpoint") {
			t.Fatalf("error = %v, want the PATCH-contract refusal", err)
		}
	})
}

func TestAgentCreateFromFlags_CarriesStatusAndDesignHooks(t *testing.T) {
	resetHookFlags(t)
	agentAddRole = "planner"
	t.Cleanup(func() { agentAddRole = "" })
	agentAddWriteDesign = true
	agentAddLabels = []string{"planned"}
	agentAddSetStatus = "blocked:upstream API decision pending"

	create, err := agentCreateFromFlags("WS", "planner-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		designAction(),
		{Type: domain.AgentHookActionAddLabel, Value: "planned"},
		{Type: domain.AgentHookActionSetStatus, Value: "blocked", Reason: "upstream API decision pending"},
	}}
	if !create.Hooks.Equal(want) {
		t.Errorf("create.Hooks = %+v, want %+v", create.Hooks, want.OnComplete)
	}
}

// The regression pin for everyone who is NOT using these flags: a pipeline built
// without them must be byte-identical to what the previous release produced.
func TestHooksFromFlags_WithoutTheNewFlagsIsUnchanged(t *testing.T) {
	got, err := hooksFromFlags(true, false, []string{"criticized"}, []string{"needs-review"}, "", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		commentAction(),
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
		{Type: domain.AgentHookActionClose},
	}}
	if !got.Equal(want) {
		t.Errorf("hooksFromFlags() = %+v, want the pre-existing pipeline %+v", got.OnComplete, want.OnComplete)
	}
	for _, a := range got.OnComplete {
		if a.Type == domain.AgentHookActionWriteDesign || a.Type == domain.AgentHookActionSetStatus {
			t.Fatalf("an unused flag produced a %s action: %+v", a.Type, got.OnComplete)
		}
		if a.Reason != "" {
			t.Errorf("action %s carries a reason it never asked for: %q", a.Type, a.Reason)
		}
	}
}
