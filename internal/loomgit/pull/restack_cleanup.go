package pull

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func (s *Service) installRestack(ctx context.Context, request RestackRequest, pullRequest PullRequest,
	old, base string, rebuilt []pulledLayer, result *PullResult) error {
	refsBefore, err := s.revisionRefs(ctx)
	if err != nil {
		return err
	}
	ownCreated, err := s.newOwnRevisions(ctx, rebuilt, request.Workspace, request.Lead)
	if err != nil {
		return err
	}
	err = s.installPull(ctx, pullRequest, old, base, rebuilt, result)
	if err == nil {
		return nil
	}
	cleanupCtx := context.WithoutCancel(ctx)
	plans, planErr := s.store.PendingPullPlans(cleanupCtx, request.Workspace, request.Lead)
	if planErr != nil {
		return errors.Join(err, loomgit.NewError(loomgit.AttentionRequired, "restack phase is unknown", planErr))
	}
	for _, plan := range plans {
		if plan.RequestID == request.RequestID && plan.Phase != "" && plan.Phase != "prepared" && plan.Phase != "not_applied" {
			return err
		}
	}
	if cleanupErr := s.abortRestack(cleanupCtx, request, refsBefore, ownCreated); cleanupErr != nil {
		return errors.Join(err, loomgit.NewError(loomgit.AttentionRequired, "restack cleanup is incomplete", cleanupErr))
	}
	return err
}

func (s *Service) newOwnRevisions(ctx context.Context, rebuilt []pulledLayer, workspace, lead string) ([]string, error) {
	var created []string
	for _, item := range rebuilt {
		if _, err := s.store.RevisionByRequest(ctx, item.layer.RequestID+":derived"); err == nil {
			return nil, loomgit.NewError(loomgit.Stale, "restack request already has a derived revision", nil)
		} else if err != journal.ErrNotFound {
			return nil, err
		}
		if item.original.Revision != 0 {
			continue
		}
		requestID := "own:" + workspace + ":" + lead + ":" + item.original.NewTip
		_, err := s.store.RevisionByRequest(ctx, requestID)
		if err == nil {
			continue
		}
		if err != journal.ErrNotFound {
			return nil, err
		}
		created = append(created, requestID)
	}
	return created, nil
}

func (s *Service) revisionRefs(ctx context.Context) (map[string]string, error) {
	output, err := git(ctx, s.runner, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom")
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		ref, sha, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("invalid revision ref %q", line)
		}
		refs[ref] = sha
	}
	return refs, nil
}

func (s *Service) abortRestack(ctx context.Context, request RestackRequest, before map[string]string, ownCreated []string) error {
	after, err := s.revisionRefs(ctx)
	if err != nil {
		return err
	}
	for ref, sha := range after {
		if previous, exists := before[ref]; exists {
			if previous != sha {
				return fmt.Errorf("revision ref %s changed during restack", ref)
			}
			continue
		}
		if _, err := s.runner.Run(ctx, "update-ref", "-d", ref, sha); err != nil {
			return fmt.Errorf("remove restack revision ref %s: %w", ref, err)
		}
	}
	return s.store.AbortRestack(ctx, request.RequestID, request.Workspace, request.Lead, ownCreated)
}
