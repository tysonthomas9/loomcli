package svcimpl

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// TestReadAgentNameValidatorAcceptsStorableNames guards the cross-endpoint
// consistency fix (Codex P2): the read-path validator (validateAgentName, used
// by the file service; the diff and terminal handlers use the same
// service.IsValidAgentName) must accept every name the create/store path can
// produce — including dotted fleet-db names — while still rejecting path-unsafe
// input and without regressing legacy names (e.g. uppercase).
func TestReadAgentNameValidatorAcceptsStorableNames(t *testing.T) {
	accept := []string{"lead-claude-1", "ABC", "a-b_c-123", "foo.bar", "agent.one", "a"}
	for _, name := range accept {
		if err := validateAgentName(name); err != nil {
			t.Errorf("read validator rejected valid name %q: %v", name, err)
		}
	}
	reject := []string{"", "agent one", "foo/bar", "../etc/passwd", "..", ".foo", "agent@foo!"}
	for _, name := range reject {
		if err := validateAgentName(name); err == nil {
			t.Errorf("read validator accepted unsafe name %q", name)
		}
	}
}

// TestStoredAgentNameValidatorIsCanonical keeps the create/store path strict:
// the lowercase fleet-db charset only, so uppercase and boundary punctuation
// are rejected before persistence.
func TestStoredAgentNameValidatorIsCanonical(t *testing.T) {
	accept := []string{"foo.bar", "lead-claude-1", "a"}
	for _, name := range accept {
		if err := validateStoredAgentName(name); err != nil {
			t.Errorf("store validator rejected valid name %q: %v", name, err)
		}
	}
	reject := []string{"", "ABC", ".foo", "foo.", "..", "foo/bar", "agent one"}
	for _, name := range reject {
		if err := validateStoredAgentName(name); err == nil {
			t.Errorf("store validator accepted invalid name %q", name)
		}
	}
}

func TestClassifyStoreErrorPreservesAgentsOwnerErrorKinds(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want service.ErrorKind
	}{
		{name: "invalid", err: agents.ErrInvalid, want: service.KindValidation},
		{name: "not found", err: agents.ErrNotFound, want: service.KindNotFound},
		{name: "already exists", err: agents.ErrAlreadyExists, want: service.KindConflict},
		{name: "conflict", err: agents.ErrConflict, want: service.KindConflict},
		{name: "invalid transition", err: agents.ErrInvalidTransition, want: service.KindConflict},
		{name: "unavailable", err: agents.ErrUnavailable, want: service.KindUnavailable},
		{name: "invalid persisted state", err: agents.ErrInvalidPersistedState, want: service.KindInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classified := classifyStoreError("update agent", test.err)
			var serviceErr *service.ServiceError
			if !errors.As(classified, &serviceErr) {
				t.Fatalf("classified error = %T %v", classified, classified)
			}
			if serviceErr.Kind != test.want {
				t.Fatalf("kind = %q, want %q", serviceErr.Kind, test.want)
			}
		})
	}
}
