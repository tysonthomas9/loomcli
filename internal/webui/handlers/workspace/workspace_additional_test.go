package workspace

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestNormalizeWorkspaceDataInitializesNestedSlices(t *testing.T) {
	data := &ops.WorkspaceData{
		Repos: []ops.WorkspaceRepo{{Name: "app"}},
		Agents: []ops.WorkspaceAgentInfo{{
			Name: "nova",
		}},
	}
	NormalizeWorkspaceData(data)
	if data.Groups == nil || data.Workspaces == nil {
		t.Fatalf("top-level slices were not initialized: %#v", data)
	}
	if data.Repos[0].Groups == nil {
		t.Fatalf("repo groups were not initialized: %#v", data.Repos[0])
	}
	if data.Agents[0].Repos == nil || data.Agents[0].RepoGroups == nil {
		t.Fatalf("agent repo slices were not initialized: %#v", data.Agents[0])
	}
}
