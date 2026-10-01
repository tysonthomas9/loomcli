package pull

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
)

func (s *Service) adoptRestackLayers(ctx context.Context, request PullRequest, base string,
	layers []loomgit.AppliedLayer, heads map[string]string) ([]pulledLayer, string, error) {
	cursor := base
	rebuilt := make([]pulledLayer, 0, len(layers))
	for index, layer := range layers {
		landed, err := s.store.IsLanded(ctx, request.Workspace, layer.Change)
		if err != nil {
			return nil, "", err
		}
		if landed {
			continue
		}
		head := heads[layer.Change]
		if head == "" || head == cursor {
			return nil, "", loomgit.NewError(loomgit.Stale, "provider head is missing or unchanged", nil)
		}
		if _, err := s.runner.Run(ctx, "merge-base", "--is-ancestor", cursor, head); err != nil {
			return nil, "", loomgit.NewError(loomgit.StackNotLinear, "provider heads are not a linear stack", err)
		}
		source, err := s.adoptionSource(ctx, request.Workspace, layer)
		if err != nil {
			return nil, "", err
		}
		rebuilt = append(rebuilt, pulledLayer{source: source, trial: replay.Result{HeadSHA: head},
			base: cursor, original: layer, layer: loomgit.AppliedLayer{
				RequestID: fmt.Sprintf("%s:layer:%d", request.RequestID, index),
				Workspace: request.Workspace, Lead: request.Lead, Change: layer.Change,
				OldTip: cursor, NewTip: head}})
		cursor = head
	}
	if len(rebuilt) != len(heads) {
		return nil, "", loomgit.NewError(loomgit.Stale, "provider heads do not cover every layer", nil)
	}
	return rebuilt, cursor, nil
}

func (s *Service) adoptionSource(ctx context.Context, workspace string, layer loomgit.AppliedLayer) (loomgit.Revision, error) {
	if layer.Revision != 0 {
		return s.store.GetRevision(ctx, workspace, layer.Change, layer.Revision)
	}
	return loomgit.Revision{Workspace: workspace, Change: layer.Change,
		BaseSHA: layer.OldTip, HeadSHA: layer.NewTip}, nil
}
