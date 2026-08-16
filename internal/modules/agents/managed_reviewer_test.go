package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const managedReviewerFingerprintFixture = "63b78c82504513d00bcd4c08633a990f7edcf5c9c84c9e0aec8ac5a881271b83"

func TestConvergeManagedReviewerOwnsFingerprintAndAtomicMutation(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, workspace, "serve-pr-reviewer-convergence", ActionConvergeManagedReviewer)
	command := testManagedReviewerCommand(ManagedReviewerActive)
	var persisted *ManagedReviewerResult
	ports.convergeReviewer = func(_ context.Context, mutation ManagedReviewerMutation) (*ManagedReviewerResult, error) {
		if mutation.WorkspaceKey != workspace || mutation.AgentID != command.AgentID ||
			mutation.DesiredState != ManagedReviewerActive || mutation.ActorID != auth.Subject() ||
			mutation.Fingerprint != managedReviewerFingerprintFixture {
			t.Fatalf("managed reviewer mutation = %+v", mutation)
		}
		persisted = testManagedReviewerResult(mutation, now)
		mutation.Preset.Role.PathPatterns = append(mutation.Preset.Role.PathPatterns, "mutated")
		mutation.Preset.Agent.Metadata = map[string]string{"mutated": "true"}
		return persisted, nil
	}

	result, err := service.ConvergeManagedReviewer(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Agent == nil || result.Role == nil || !result.Changed ||
		result.PresetFingerprint != managedReviewerFingerprintFixture {
		t.Fatalf("managed reviewer result = %#v", result)
	}
	if len(command.Preset.Role.PathPatterns) != 0 || command.Preset.Agent.Metadata != nil {
		t.Fatalf("store mutation escaped into caller command: %+v", command)
	}
	result.Agent.Metadata = map[string]string{"mutated": "true"}
	result.Role.PathPatterns = []string{"mutated"}
	if persisted.Agent.Metadata != nil || len(persisted.Role.PathPatterns) != 0 {
		t.Fatal("service leaked persisted reviewer identity through its result")
	}
}

func TestConvergeManagedReviewerArchivesOnlyAgentIdentity(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, workspace, "serve-pr-reviewer-convergence", ActionConvergeManagedReviewer)
	command := testManagedReviewerCommand(ManagedReviewerArchived)
	ports.convergeReviewer = func(_ context.Context, mutation ManagedReviewerMutation) (*ManagedReviewerResult, error) {
		result := testManagedReviewerResult(mutation, now)
		deletedAt := now.Add(time.Second)
		result.Agent.DeletedAt = &deletedAt
		return result, nil
	}

	result, err := service.ConvergeManagedReviewer(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Agent.DeletedAt == nil || result.Role.Name != command.Preset.Role.Name {
		t.Fatalf("archived reviewer result = %#v", result)
	}
}

func TestConvergeManagedReviewerRequiresExactAuthorityAndRejectsDrift(t *testing.T) {
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	command := testManagedReviewerCommand(ManagedReviewerActive)
	wrong := issueSystem(t, issuer, "WS", "wrong-owner", ActionEnsureManagedRole)
	if result, err := service.ConvergeManagedReviewer(t.Context(), wrong, command); result != nil ||
		!errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong authority result = %#v, %v", result, err)
	}

	auth := issueSystem(t, issuer, "WS", "serve-pr-reviewer-convergence", ActionConvergeManagedReviewer)
	ports.convergeReviewer = func(_ context.Context, mutation ManagedReviewerMutation) (*ManagedReviewerResult, error) {
		result := testManagedReviewerResult(mutation, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
		result.Role.PromptFile = "builtin:drifted"
		return result, nil
	}
	if result, err := service.ConvergeManagedReviewer(t.Context(), auth, command); result != nil ||
		!errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("drifted persistence result = %#v, %v", result, err)
	}
}

func TestManagedReviewerFingerprintMatchesFleetContractFixture(t *testing.T) {
	command, fingerprint, err := normalizeManagedReviewerCommand(testManagedReviewerCommand(ManagedReviewerActive))
	if err != nil {
		t.Fatal(err)
	}
	if command.Preset.PresetID != "reviewer-v1" || fingerprint != managedReviewerFingerprintFixture {
		t.Fatalf("normalized preset = %+v fingerprint = %q", command.Preset, fingerprint)
	}
}

func testManagedReviewerCommand(state ManagedReviewerDesiredState) ManagedReviewerCommand {
	return ManagedReviewerCommand{
		WorkspaceKey: "WS", AgentID: "review-octo-repo-pr-7", DesiredState: state,
		Preset: ManagedReviewerPreset{
			PresetID: "reviewer-v1", Revision: 1,
			Role: ManagedReviewerRoleDefinition{
				Name: "pr-reviewer", Description: "Review a checkout.",
				Kind: RoleKindInteractive, PromptFile: "builtin:pr-review-checkout",
			},
			Agent: ManagedReviewerAgentDefinition{
				Kind: AgentKindSupport, DesiredState: DesiredRunning,
				RoleName: "pr-reviewer", MaxInstances: 1,
			},
		},
	}
}

func testManagedReviewerResult(mutation ManagedReviewerMutation, now time.Time) *ManagedReviewerResult {
	return &ManagedReviewerResult{
		PresetID: mutation.Preset.PresetID, PresetRevision: mutation.Preset.Revision,
		PresetFingerprint: mutation.Fingerprint, Changed: true,
		Role: &Role{
			WorkspaceKey: mutation.WorkspaceKey, Name: mutation.Preset.Role.Name,
			Description: mutation.Preset.Role.Description, Kind: mutation.Preset.Role.Kind,
			PromptFile: mutation.Preset.Role.PromptFile, Prompt: mutation.Preset.Role.Prompt,
			Model: mutation.Preset.Role.Model, TaskFilter: mutation.Preset.Role.TaskFilter,
			Backend: mutation.Preset.Role.Backend, Effort: mutation.Preset.Role.Effort,
			PathPatterns: mutation.Preset.Role.PathPatterns, Skills: mutation.Preset.Role.Skills,
			MaxPriority: mutation.Preset.Role.MaxPriority, MaxConcurrency: mutation.Preset.Role.MaxConcurrency,
			ReadOnly: mutation.Preset.Role.ReadOnly, AllowedTools: mutation.Preset.Role.AllowedTools,
			DeniedTools: mutation.Preset.Role.DeniedTools, MaxBudgetUSD: mutation.Preset.Role.MaxBudgetUSD,
			CreatedAt: now, UpdatedAt: now,
		},
		Agent: &Agent{
			WorkspaceKey: mutation.WorkspaceKey, AgentID: mutation.AgentID,
			GenerationID: "00112233445566778899aabbccddeeff", Name: mutation.AgentID,
			Kind:         mutation.Preset.Agent.Kind,
			Behavior:     BehaviorReference{RoleName: mutation.Preset.Agent.RoleName},
			DesiredState: mutation.Preset.Agent.DesiredState,
			MaxInstances: mutation.Preset.Agent.MaxInstances,
			BudgetPolicy: mutation.Preset.Agent.BudgetPolicy,
			Metadata:     cloneStringMap(mutation.Preset.Agent.Metadata),
			CreatedBy:    mutation.ActorID, CreatedAt: now, UpdatedAt: now,
		},
	}
}
