package role

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// persona_source is a closed vocabulary; a typo must fail at the CLI naming
// both accepted values, not as a server 400 after the round trip. Both
// spellings of the key are accepted so `role set` and `role add
// --persona-source` agree on the field's name.
func TestBuildRolePatch_PersonaSourceVocabulary(t *testing.T) {
	for _, key := range []string{"persona_source", "persona-source"} {
		for _, valid := range []string{domain.PersonaSourceArgv, domain.PersonaSourceProfile, ""} {
			patch, err := buildRolePatch(key, valid, false)
			if err != nil {
				t.Fatalf("%s %q must be accepted: %v", key, valid, err)
			}
			if patch.PersonaSource == nil || *patch.PersonaSource != valid {
				t.Fatalf("%s: patch.PersonaSource = %v, want %q", key, patch.PersonaSource, valid)
			}
		}

		_, err := buildRolePatch(key, "bogus", false)
		if err == nil {
			t.Fatalf("%s bogus must be rejected", key)
		}
		for _, want := range []string{domain.PersonaSourceArgv, domain.PersonaSourceProfile} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: error %q does not name %q", key, err, want)
			}
		}
	}
}

// A mixed-case value is normalized rather than rejected, matching how `kind` is
// handled: the vocabulary is closed, but the shell is not case-sensitive about
// what a human types.
func TestBuildRolePatch_PersonaSourceNormalizes(t *testing.T) {
	patch, err := buildRolePatch("persona_source", "  PROFILE ", false)
	if err != nil {
		t.Fatalf("buildRolePatch: %v", err)
	}
	if patch.PersonaSource == nil || *patch.PersonaSource != domain.PersonaSourceProfile {
		t.Fatalf("patch.PersonaSource = %v, want %q", patch.PersonaSource, domain.PersonaSourceProfile)
	}
}

// The `role add` flag runs the same validator, so a bad value never reaches a
// RoleCreate.
func TestValidateRolePersonaSourceValue(t *testing.T) {
	for _, valid := range []string{"", "argv", "profile", "Profile"} {
		if err := validateRolePersonaSourceValue(valid); err != nil {
			t.Fatalf("persona-source %q must be accepted: %v", valid, err)
		}
	}
	err := validateRolePersonaSourceValue("ambient")
	if err == nil {
		t.Fatal("--persona-source ambient must be rejected")
	}
	for _, want := range []string{"argv", "profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

// The help texts are the discovery surface for a field with no other
// documentation, so both lists have to mention it.
func TestRolePersonaSourceIsDocumented(t *testing.T) {
	if !strings.Contains(roleSetCmd.Long, "persona_source") {
		t.Fatalf("`role set --help` does not list persona_source:\n%s", roleSetCmd.Long)
	}
	if !strings.Contains(roleUnsetCmd.Long, "persona_source") {
		t.Fatalf("`role unset --help` does not list persona_source:\n%s", roleUnsetCmd.Long)
	}
	if roleAddCmd.Flags().Lookup("persona-source") == nil {
		t.Fatal("`role add` has no --persona-source flag")
	}
}

// A valid value has to survive Create -> Get -> Update, which is the only thing
// that proves the field is carried at every copy site rather than dropped at
// one of them.
func TestPersonaSourceRoundTripsThroughTheStore(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()

	created, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:  "E2E",
		Name:          "lead",
		Kind:          string(domain.RoleKindInteractive),
		PersonaSource: domain.PersonaSourceProfile,
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if created.PersonaSource != domain.PersonaSourceProfile {
		t.Fatalf("created.PersonaSource = %q, want %q", created.PersonaSource, domain.PersonaSourceProfile)
	}

	got, err := st.Roles().Get(ctx, "E2E", "lead")
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	if got.PersonaSource != domain.PersonaSourceProfile {
		t.Fatalf("got.PersonaSource = %q, want %q", got.PersonaSource, domain.PersonaSourceProfile)
	}

	patch, err := buildRolePatch("persona_source", domain.PersonaSourceArgv, false)
	if err != nil {
		t.Fatalf("buildRolePatch: %v", err)
	}
	updated, err := st.Roles().Update(ctx, "E2E", "lead", patch)
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	if updated.PersonaSource != domain.PersonaSourceArgv {
		t.Fatalf("updated.PersonaSource = %q, want %q", updated.PersonaSource, domain.PersonaSourceArgv)
	}

	// Clearing lands as "", which resolves to argv.
	clear, err := buildRolePatch("persona_source", "", true)
	if err != nil {
		t.Fatalf("buildRolePatch clear: %v", err)
	}
	cleared, err := st.Roles().Update(ctx, "E2E", "lead", clear)
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	if cleared.PersonaSource != "" {
		t.Fatalf("cleared.PersonaSource = %q, want empty", cleared.PersonaSource)
	}
}
