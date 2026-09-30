package ops

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
)

// EnsureInteractiveWorkingArea returns the durable checkout path for each
// selected repository of an interactive agent.
func EnsureInteractiveWorkingArea(ctx context.Context, workspace, lead, wsDir string, repos []localworkspace.Repo) (map[string]string, error) {
	sources := make([]loomworkspace.WorkingAreaSource, 0, len(repos))
	for _, repo := range repos {
		sources = append(sources, loomworkspace.WorkingAreaSource{Name: repo.Name, Path: repo.Path})
	}
	areas, err := loomworkspace.EnsureWorkingArea(ctx, workspace, lead, wsDir, sources)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string, len(areas))
	for _, area := range areas {
		paths[area.Repo] = area.Path
	}
	return paths, nil
}
