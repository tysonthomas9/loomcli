package cli

import (
	"encoding/json"
	"testing"
)

func TestHasAvailablePlanningTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		want     bool
		wantErr  bool
	}{
		{
			name: "has task needing planning (no design)",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "task has design - not needing planning",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			want: false,
		},
		{
			name: "skip Need Review tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Add feature", Status: "open", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip epics",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			want: false,
		},
		{
			name:     "empty list",
			bdOutput: "[]",
			want:     false,
		},
		{
			name: "mixed - one valid task",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Skip me", Status: "open", Design: ""},
				{ID: "T-2", Title: "Work on me", Status: "open", Design: ""},
				{ID: "T-3", Title: "Has design", Status: "open", Design: "Already planned"},
			}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore execCommand
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.bdOutput, Err: tt.bdErr}
			}

			got, err := HasAvailablePlanningTasks()
			if (err != nil) != tt.wantErr {
				t.Errorf("HasAvailablePlanningTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("HasAvailablePlanningTasks() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasAvailableImplementationTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		want     bool
		wantErr  bool
	}{
		{
			name: "has task with design - ready for implementation",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Implementation plan here"},
			}),
			want: true,
		},
		{
			name: "task has no design - not ready for implementation",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip Need Review tasks even with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Add feature", Status: "open", Design: "Has design"},
			}),
			want: false,
		},
		{
			name: "skip in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: "Has design"},
			}),
			want: false,
		},
		{
			name: "skip epics even with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: "Has design"},
			}),
			want: false,
		},
		{
			name:     "empty list",
			bdOutput: "[]",
			want:     false,
		},
		{
			name: "mixed - one valid task with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Skip me", Status: "open", Design: "Has design"},
				{ID: "T-2", Title: "No design yet", Status: "open", Design: ""},
				{ID: "T-3", Title: "Ready to implement", Status: "open", Design: "Detailed plan"},
			}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore execCommand
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.bdOutput, Err: tt.bdErr}
			}

			got, err := HasAvailableImplementationTasks()
			if (err != nil) != tt.wantErr {
				t.Errorf("HasAvailableImplementationTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("HasAvailableImplementationTasks() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoModeOptions(t *testing.T) {
	opts := AutoModeOptions{
		Interval:     60,
		MaxTasks:     10,
		IdleTimeout:  30,
		AgentType:    "plan",
		AgentName:    "falcon",
		WorktreePath: "/path/to/worktree",
	}

	if opts.Interval != 60 {
		t.Errorf("Interval = %d, want 60", opts.Interval)
	}
	if opts.MaxTasks != 10 {
		t.Errorf("MaxTasks = %d, want 10", opts.MaxTasks)
	}
	if opts.IdleTimeout != 30 {
		t.Errorf("IdleTimeout = %d, want 30", opts.IdleTimeout)
	}
	if opts.AgentType != "plan" {
		t.Errorf("AgentType = %s, want plan", opts.AgentType)
	}
}

func TestFormatLimit(t *testing.T) {
	tests := []struct {
		limit int
		want  string
	}{
		{0, "unlimited"},
		{-1, "unlimited"},
		{1, "1"},
		{10, "10"},
		{100, "100"},
	}

	for _, tt := range tests {
		got := formatLimit(tt.limit)
		if got != tt.want {
			t.Errorf("formatLimit(%d) = %s, want %s", tt.limit, got, tt.want)
		}
	}
}

func TestFormatTimeout(t *testing.T) {
	tests := []struct {
		timeout int
		want    string
	}{
		{0, "none"},
		{-1, "none"},
		{1, "1m"},
		{30, "30m"},
		{60, "60m"},
	}

	for _, tt := range tests {
		got := formatTimeout(tt.timeout)
		if got != tt.want {
			t.Errorf("formatTimeout(%d) = %s, want %s", tt.timeout, got, tt.want)
		}
	}
}

func TestSetupSignalHandler(t *testing.T) {
	// Test that SetupSignalHandler returns a channel
	shutdown := SetupSignalHandler()
	if shutdown == nil {
		t.Error("SetupSignalHandler() returned nil channel")
	}
}

// mustJSON marshals value to JSON string, panics on error (test helper)
func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}
