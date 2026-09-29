package role

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// builtin: resolves only in GenerateTerminalPrompt, for interactive terminal
// prompts. A worker role carrying one stores fine and then fails daemon
// creation for EVERY agent in the workspace, naming a path nobody wrote.
// Reject it where the mistake is still attributable.
func TestValidateRolePromptFile(t *testing.T) {
	tests := []struct {
		name       string
		roleName   string
		kind       string
		promptFile string
		wantErr    bool
	}{
		{
			name:       "builtin on a worker role is refused",
			roleName:   "critic",
			kind:       "worker",
			promptFile: "builtin:pr-review",
			wantErr:    true,
		},
		{
			// Kind defaults to worker when unset, so the default must refuse too
			// or the common invocation stays broken.
			name:       "builtin with no kind is refused",
			roleName:   "critic",
			promptFile: "builtin:pr-review",
			wantErr:    true,
		},
		{
			name:       "builtin on an interactive role is allowed",
			roleName:   "critic",
			kind:       "interactive",
			promptFile: "builtin:pr-review",
		},
		{
			name:       "interactive kind is matched case-insensitively",
			roleName:   "critic",
			kind:       "Interactive",
			promptFile: "builtin:lead",
		},
		{
			// The daemon resolves an unset kind through the legacy name
			// convention, so this check has to as well or it refuses a role the
			// supervisor would have accepted.
			name:       "a legacy interactive role name needs no explicit kind",
			roleName:   "lead",
			promptFile: "builtin:lead",
		},
		{
			name:       "a real path on a worker role is allowed",
			roleName:   "critic",
			kind:       "worker",
			promptFile: "prompts/critic.md",
		},
		{
			name:       "no prompt file is not this check's business",
			roleName:   "critic",
			kind:       "worker",
			promptFile: "",
		},
		{
			// "builtin" without the colon is an ordinary relative path.
			name:       "a path merely starting with builtin is allowed",
			roleName:   "critic",
			kind:       "worker",
			promptFile: "builtins/reviewer.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRolePromptFile(tt.roleName, tt.kind, tt.promptFile)

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
	err := validateRolePromptFile("critic", "worker", "builtin:pr-review")
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

// An unknown id on an interactive role used to be stored and only fail at
// terminal spawn ("unknown built-in interactive prompt"), far from the typo.
// The valid ids are already in hand at add time.
func TestValidateRolePromptFile_UnknownBuiltinIDIsRefused(t *testing.T) {
	err := validateRolePromptFile("lead", "interactive", "builtin:garbage")
	if err == nil {
		t.Fatal("an unknown built-in id must not be accepted on an interactive role")
	}
	if !strings.Contains(err.Error(), "lead") || !strings.Contains(err.Error(), "pr-review") {
		t.Errorf("error should name the available built-ins: %v", err)
	}

	// A hidden built-in is a real id even though it is not advertised, so it
	// must not be rejected as unknown.
	if err := validateRolePromptFile("lead", "interactive", "builtin:pr-review-checkout"); err != nil {
		t.Errorf("a hidden built-in is still a valid id: %v", err)
	}
}

// roleStoreStub is the smallest RoleStore that lets the update-path validation
// read a stored role.
type roleStoreStub struct {
	store.RoleStore
	role *domain.Role
}

func (s *roleStoreStub) Get(context.Context, string, string) (*domain.Role, error) {
	return s.role, nil
}

// `role set` reaches the same store as `role add`, so it needs the same guard.
// buildRolePatch validates one value at a time and cannot see the combination,
// which is how each of these rebuilds the daemon-killing pair from a role that
// was valid a moment earlier.
func TestValidateRoleUpdate_ClosesTheRoleSetBypass(t *testing.T) {
	worker := &domain.Role{Name: "critic", Kind: domain.RoleKindWorker}
	interactive := &domain.Role{
		Name:       "critic",
		Kind:       domain.RoleKindInteractive,
		PromptFile: "builtin:pr-review",
	}

	tests := []struct {
		name    string
		stored  *domain.Role
		key     string
		value   string
		wantErr bool
	}{
		{
			name:    "prompt_file set to a builtin on a worker role",
			stored:  worker,
			key:     "prompt_file",
			value:   "builtin:pr-review",
			wantErr: true,
		},
		{
			name:    "kind flipped to worker under a stored builtin prompt",
			stored:  interactive,
			key:     "kind",
			value:   "worker",
			wantErr: true,
		},
		{
			name:    "kind cleared, dropping back to the worker default",
			stored:  interactive,
			key:     "kind",
			value:   "",
			wantErr: true,
		},
		{
			name:   "prompt_file set to a builtin on an interactive role",
			stored: interactive,
			key:    "prompt_file",
			value:  "builtin:lead",
		},
		{
			name:   "prompt_file set to a path on a worker role",
			stored: worker,
			key:    "prompt_file",
			value:  "prompts/critic.md",
		},
		{
			// Unrelated keys must not pay for a store read.
			name:   "an unrelated key is not this check's business",
			stored: worker,
			key:    "model",
			value:  "builtin:pr-review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := &roleStoreStub{role: tt.stored}
			err := validateRoleUpdate(context.Background(), roles, "ws", "critic", tt.key, tt.value)
			if tt.wantErr && err == nil {
				t.Fatalf("validateRoleUpdate(%q, %q) = nil, want an error", tt.key, tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateRoleUpdate(%q, %q) = %v, want nil", tt.key, tt.value, err)
			}
		})
	}
}
