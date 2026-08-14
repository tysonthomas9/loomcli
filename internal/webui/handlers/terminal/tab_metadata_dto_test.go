package terminal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

func TestTerminalTabDTOExposesPublicProjectionAndKeepsOwnerIdentityPrivate(t *testing.T) {
	now := time.Date(2026, time.August, 13, 1, 2, 3, 0, time.UTC)
	dto := terminalTabDTO(&interaction.TabMetadata{
		SessionName:                  "agent_reviewer",
		Workspace:                    "WS",
		Label:                        "Reviewer",
		Kind:                         "agent",
		AgentID:                      "reviewer",
		Role:                         "review",
		Backend:                      "codex",
		Writable:                     false,
		InteractionSessionID:         "session-secret",
		InteractionTerminalID:        "terminal-secret",
		InteractionLeaseID:           "lease-secret",
		InteractionLeaseFencingToken: 42,
		Launch: &interaction.LaunchSpec{
			Argv: []string{"codex", "--secret-argument"},
			Env:  map[string]string{"SECRET_TOKEN": "secret-value"},
			Cwd:  "/private/worktree",
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, public := range []string{
		`"session_name":"agent_reviewer"`,
		`"workspace":"WS"`,
		`"kind":"agent"`,
		`"agent_id":"reviewer"`,
		`"role":"review"`,
		`"backend":"codex"`,
		`"writable":false`,
	} {
		if !strings.Contains(body, public) {
			t.Fatalf("public field %s missing from %s", public, body)
		}
	}
	for _, private := range []string{
		"launch", "secret-argument", "SECRET_TOKEN", "secret-value", "/private/worktree",
		"interaction_session_id", "interaction_terminal_id", "interaction_lease_id",
		"interaction_lease_fencing_token", "session-secret", "terminal-secret", "lease-secret",
	} {
		if strings.Contains(body, private) {
			t.Fatalf("private owner field %q leaked in %s", private, body)
		}
	}
}

func TestTerminalTabDTOsKeepsEmptyListAsJSONArray(t *testing.T) {
	encoded, err := json.Marshal(terminalTabDTOs(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("encoded empty list = %s", encoded)
	}
}
