package cli

import (
	"strings"
	"testing"
)

func TestSyncCmd_ArgsValidation(t *testing.T) {
	// Save and restore the global flag
	origSyncAll := syncAll
	defer func() { syncAll = origSyncAll }()

	tests := []struct {
		name      string
		args      []string
		allFlag   bool
		wantError bool
		errorMsg  string
	}{
		// Without --all flag: requires exactly 2 args (worktree, branch)
		{
			name:      "without --all, no args",
			args:      []string{},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},
		{
			name:      "without --all, one arg",
			args:      []string{"falcon"},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},
		{
			name:      "without --all, two args (success)",
			args:      []string{"falcon", "main"},
			allFlag:   false,
			wantError: false,
		},
		{
			name:      "without --all, three args",
			args:      []string{"falcon", "main", "extra"},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},

		// With --all flag: requires exactly 1 arg (branch)
		{
			name:      "with --all, no args",
			args:      []string{},
			allFlag:   true,
			wantError: true,
			errorMsg:  "--all flag requires exactly 1 argument",
		},
		{
			name:      "with --all, one arg (success)",
			args:      []string{"main"},
			allFlag:   true,
			wantError: false,
		},
		{
			name:      "with --all, two args",
			args:      []string{"main", "extra"},
			allFlag:   true,
			wantError: true,
			errorMsg:  "--all flag requires exactly 1 argument",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set the flag state
			syncAll = tc.allFlag

			// Call the Args validation function directly
			err := syncCmd.Args(syncCmd, tc.args)

			if tc.wantError {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tc.errorMsg)
					return
				}
				if tc.errorMsg != "" && !strings.Contains(err.Error(), tc.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tc.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}
