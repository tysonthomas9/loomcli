package lead

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// personaSourceRegistration seeds a role and hands back a registration bound to
// the in-memory store holding it.
func personaSourceRegistration(t *testing.T, in store.RoleCreate) leadSessionRegistration {
	t.Helper()
	st := memstore.New()
	if _, err := st.Roles().Create(t.Context(), in); err != nil {
		t.Fatalf("create role: %v", err)
	}
	return leadSessionRegistration{handle: &bootstrap.StoreHandle{Store: st}, Workspace: in.WorkspaceKey}
}

// Acceptance criterion 2: persona_source: profile suppresses the argv persona
// and must NOT trigger seed-and-shrink. dedicated=true is the interesting case:
// that is exactly where an AGENTS.md would otherwise be written.
func TestGenerateLeadTerminalPromptPersonaSourceProfileSuppresses(t *testing.T) {
	t.Setenv("LOOM_AGENT_ROLE", "lead")
	registration := personaSourceRegistration(t, store.RoleCreate{
		WorkspaceKey:  "E2E",
		Name:          "lead",
		Kind:          string(domain.RoleKindInteractive),
		PersonaSource: domain.PersonaSourceProfile,
		// An inline prompt AND a prompt file are set on purpose: suppression
		// has to beat both of the branches that would otherwise fire.
		Prompt:     "Inline persona that must not reach argv",
		PromptFile: "prompts/ignored.md",
	})

	oldPromptFile := leadPromptFile
	leadPromptFile = ""
	t.Cleanup(func() { leadPromptFile = oldPromptFile })

	prompt, seedAndShrink, err := generateLeadTerminalPrompt(context.Background(), registration, true)
	if err != nil {
		t.Fatalf("generateLeadTerminalPrompt: %v", err)
	}
	if prompt != "" {
		t.Fatalf("persona_source: profile did not suppress the argv persona: %q", prompt)
	}
	if seedAndShrink {
		t.Fatal("persona_source: profile must not seed an ambient instruction file")
	}
}

// Acceptance criterion 3: an explicit --prompt is the operator overriding their
// own config, so it still wins.
func TestGenerateLeadTerminalPromptExplicitPromptBeatsPersonaSource(t *testing.T) {
	t.Setenv("LOOM_AGENT_ROLE", "lead")
	registration := personaSourceRegistration(t, store.RoleCreate{
		WorkspaceKey:  "E2E",
		Name:          "lead",
		Kind:          string(domain.RoleKindInteractive),
		PersonaSource: domain.PersonaSourceProfile,
	})

	oldPromptFile := leadPromptFile
	leadPromptFile = "builtin:lead"
	t.Cleanup(func() { leadPromptFile = oldPromptFile })

	prompt, seedAndShrink, err := generateLeadTerminalPrompt(context.Background(), registration, false)
	if err != nil {
		t.Fatalf("generateLeadTerminalPrompt: %v", err)
	}
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("an explicit --prompt must win over persona_source: profile")
	}
	if seedAndShrink {
		t.Fatal("an explicit --prompt must not trigger seed-and-shrink")
	}
}

// persona_source: argv — and the empty value every pre-existing role carries —
// must leave today's behavior exactly as it was.
func TestGenerateLeadTerminalPromptPersonaSourceArgvIsUnchanged(t *testing.T) {
	for _, source := range []string{"", domain.PersonaSourceArgv} {
		t.Setenv("LOOM_AGENT_ROLE", "lead")
		registration := personaSourceRegistration(t, store.RoleCreate{
			WorkspaceKey:  "E2E",
			Name:          "lead",
			Kind:          string(domain.RoleKindInteractive),
			PersonaSource: source,
			Prompt:        "Inline persona",
		})

		oldPromptFile := leadPromptFile
		leadPromptFile = ""
		prompt, seedAndShrink, err := generateLeadTerminalPrompt(context.Background(), registration, true)
		leadPromptFile = oldPromptFile
		if err != nil {
			t.Fatalf("persona_source %q: generateLeadTerminalPrompt: %v", source, err)
		}
		if !strings.HasPrefix(prompt, "Inline persona") {
			t.Fatalf("persona_source %q: prompt = %q, want the inline role prompt", source, prompt)
		}
		if seedAndShrink {
			t.Fatalf("persona_source %q: inline role prompt must clear seedAndShrink", source)
		}
	}
}

// Acceptance criterion 5: an unreachable store yields a nil role, and the lead
// falls back toward MORE persona rather than booting with none.
func TestLoadLeadRoleUnreachableStoreIsNil(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_AGENT_ROLE", "lead")

	if role := loadLeadRole(context.Background(), leadSessionRegistration{}); role != nil {
		t.Fatalf("loadLeadRole with no store = %+v, want nil", role)
	}
}

// A role that simply does not exist is the same fail-safe nil, not an error.
func TestLoadLeadRoleMissingRoleIsNil(t *testing.T) {
	t.Setenv("LOOM_AGENT_ROLE", "nonexistent")
	registration := personaSourceRegistration(t, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
	})

	if role := loadLeadRole(context.Background(), registration); role != nil {
		t.Fatalf("loadLeadRole for an absent role = %+v, want nil", role)
	}
}
