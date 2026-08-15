package scriptedroles

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestCatalogRows(t *testing.T) {
	scout, ok := ForRole(ScoutRoleName)
	if !ok || scout.WorkflowName != ScoutWorkflowName || scout.DefaultInstance == nil {
		t.Fatalf("scout catalog row = %+v ok=%v", scout, ok)
	}
	if scout.DefaultRole.Kind != domain.RoleKindWorker || !strings.Contains(scout.DefaultRole.Prompt, "You are the Scout") {
		t.Fatalf("scout role seed = %+v", scout.DefaultRole)
	}
	if scout.DefaultInstance.Binding.TargetEntrypoint != "run" || scout.DefaultInstance.Binding.Schedule != "@weekly" {
		t.Fatalf("scout instance = %+v", scout.DefaultInstance)
	}

	epic, ok := ForWorkflow(EpicRunnerWorkflowName)
	if !ok || epic.RoleName != EpicRunnerRoleName || epic.DefaultInstance != nil {
		t.Fatalf("epic catalog row = %+v ok=%v", epic, ok)
	}
	if len(epic.LeafRunners) != 2 || epic.LeafRunners[0] != LocalTaskRunnerEntrypoint || epic.LeafRunners[1] != DaytonaTaskRunnerEntrypoint {
		t.Fatalf("epic leaves = %v", epic.LeafRunners)
	}
}

func TestCatalogDerivedTrustAndPreflight(t *testing.T) {
	scout, _ := ForRole(ScoutRoleName)
	epic, _ := ForRole(EpicRunnerRoleName)
	if !NeedsPreflight(scout, DaytonaTaskRunnerEntrypoint) {
		t.Fatal("scout Always policy did not preflight")
	}
	if !NeedsPreflight(epic, "") || !NeedsPreflight(epic, LocalTaskRunnerEntrypoint) || NeedsPreflight(epic, DaytonaTaskRunnerEntrypoint) {
		t.Fatal("epic PayloadRunner policy mismatch")
	}
	if !IsTrustedLocalCLIRunner(ScoutTaskRunnerEntrypoint) || !IsTrustedLocalCLIRunner(LocalTaskRunnerEntrypoint) {
		t.Fatal("catalog local leaves are not trusted")
	}
	if IsTrustedLocalCLIRunner(DaytonaTaskRunnerEntrypoint) {
		t.Fatal("remote Daytona leaf received trusted-local treatment")
	}
}

func TestForRoleReturnsDefensiveCopy(t *testing.T) {
	first, _ := ForRole(ScoutRoleName)
	first.LeafRunners[0] = "mutated"
	first.DefaultInstance.Binding.ExcludedActors[0] = "mutated"
	second, _ := ForRole(ScoutRoleName)
	if second.LeafRunners[0] != ScoutTaskRunnerEntrypoint || second.DefaultInstance.Binding.ExcludedActors[0] != "driver-run" {
		t.Fatalf("catalog mutated through caller: %+v", second)
	}
}
