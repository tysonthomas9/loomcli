package role

import (
	"strings"
	"testing"
)

// builtin: resolves only in GenerateTerminalPrompt, for interactive terminal
// prompts. A worker role carrying one stores fine and then fails daemon
// creation for EVERY agent in the workspace with a path nobody wrote
// (DOGFOOD-67). Reject it where the mistake is still attributable.
func TestValidateRolePromptFile(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		promptFile string
		wantErr    bool
	}{
		{
			name:       "builtin on a worker role is refused",
			kind:       "worker",
			promptFile: "builtin:pr-review",
			wantErr:    true,
		},
		{
			// Kind defaults to worker when unset, so the default must refuse too
			// or the common invocation stays broken.
			name:       "builtin with no kind is refused",
			promptFile: "builtin:pr-review",
			wantErr:    true,
		},
		{
			name:       "builtin on an interactive role is allowed",
			kind:       "interactive",
			promptFile: "builtin:pr-review",
		},
		{
			name:       "interactive kind is matched case-insensitively",
			kind:       "Interactive",
			promptFile: "builtin:lead",
		},
		{
			name:       "a real path on a worker role is allowed",
			kind:       "worker",
			promptFile: "prompts/critic.md",
		},
		{
			name:       "no prompt file is not this check's business",
			kind:       "worker",
			promptFile: "",
		},
		{
			// "builtin" without the colon is an ordinary relative path.
			name:       "a path merely starting with builtin is allowed",
			kind:       "worker",
			promptFile: "builtins/reviewer.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRolePromptFile(tt.kind, tt.promptFile)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("validateRolePromptFile(%q, %q) = %v, want nil", tt.kind, tt.promptFile, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateRolePromptFile(%q, %q) = nil, want an error", tt.kind, tt.promptFile)
			}
			// The message has to say what to do instead; the original failure
			// mode was an unactionable "file not found" at daemon startup.
			for _, want := range []string{"interactive", "worker role"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q: %v", want, err)
				}
			}
		})
	}
}

// The accepted built-ins are named so the caller does not have to guess, and
// hidden ones stay hidden.
func TestValidateRolePromptFile_ErrorNamesTheBuiltins(t *testing.T) {
	err := validateRolePromptFile("worker", "builtin:pr-review")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "pr-review") {
		t.Errorf("error should list the available built-ins: %v", err)
	}
	if strings.Contains(err.Error(), "pr-review-checkout") {
		t.Errorf("hidden built-ins must not be advertised: %v", err)
	}
}
