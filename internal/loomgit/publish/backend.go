package publish

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
)

type StackCapabilities struct {
	NativeStacks bool
}

type StackBackend interface {
	Publish(context.Context, StackRequest) ([]loomgit.Revision, error)
	Restack(context.Context, StackRequest, string) error
	MergeUpTo(context.Context, StackRequest, string) error
	Capabilities() StackCapabilities
}

type LoomStackBackend struct{ Store Store }

func RestackOffer(ctx context.Context, offer journal.RestackOffer) (int, error) {
	return pull.RestackOfferWithPublish(ctx, offer, func(ctx context.Context, workspace, lead, change string) error {
		_, err := PublishLocal(ctx, workspace, lead, change)
		return err
	})
}

type GitHubStackBackend struct{ Store Store }

func (GitHubStackBackend) Capabilities() StackCapabilities {
	return StackCapabilities{NativeStacks: true}
}

func (backend GitHubStackBackend) Publish(ctx context.Context, request StackRequest) ([]loomgit.Revision, error) {
	if len(request.Changes) == 0 || request.StackID == "" {
		return nil, errors.New("stack ID and changes are required")
	}
	forge, ok := request.forge.(interface {
		EnsureNativeStack(context.Context, string, string, []int) error
	})
	if !ok {
		return nil, errors.New("forge cannot create native stacks")
	}
	var revisions []loomgit.Revision
	err := stacklock.With(ctx, request.Workspace, request.StackID, func(lockedCtx context.Context) error {
		var publishErr error
		revisions, publishErr = publishStack(lockedCtx, backend.Store, request)
		if publishErr != nil {
			return publishErr
		}
		parts := strings.Split(request.slug, "/")
		if len(parts) != 2 {
			return errors.New("native stack repository slug is invalid")
		}
		numbers := make([]int, 0, len(request.Changes))
		for _, change := range request.Changes {
			publication, found, err := backend.Store.Publication(lockedCtx, request.Workspace, change)
			if err != nil || !found || publication.PRNumber == 0 {
				return errors.New("native stack publication record unavailable")
			}
			numbers = append(numbers, publication.PRNumber)
		}
		return forge.EnsureNativeStack(lockedCtx, parts[0], parts[1], numbers)
	})
	return revisions, err
}

func (backend GitHubStackBackend) Restack(ctx context.Context, request StackRequest, requestID string) error {
	return LoomStackBackend(backend).Restack(ctx, request, requestID)
}

func (GitHubStackBackend) MergeUpTo(context.Context, StackRequest, string) error {
	return loomgit.NewError(loomgit.MergeNotAuthorized, "native stack merge requires separate authorization", nil)
}

func (backend LoomStackBackend) Capabilities() StackCapabilities { return StackCapabilities{} }

func (backend LoomStackBackend) Publish(ctx context.Context, request StackRequest) ([]loomgit.Revision, error) {
	if len(request.Changes) == 0 || request.StackID == "" {
		return nil, errors.New("stack ID and changes are required")
	}
	if provider, ok := request.forge.(interface{ StackLimit() int }); ok {
		if limit := provider.StackLimit(); limit > 0 && len(request.Changes) > limit {
			return nil, loomgit.NewError(loomgit.ProviderStackLimit, "provider stack limit exceeded", nil)
		}
	}
	var revisions []loomgit.Revision
	err := stacklock.With(ctx, request.Workspace, request.StackID, func(lockedCtx context.Context) error {
		var publishErr error
		revisions, publishErr = publishStack(lockedCtx, backend.Store, request)
		return publishErr
	})
	return revisions, err
}

func (backend LoomStackBackend) Restack(ctx context.Context, request StackRequest, requestID string) error {
	if len(request.Changes) == 0 || request.StackID == "" {
		return errors.New("stack ID and changes are required")
	}
	return stacklock.With(ctx, request.Workspace, request.StackID, func(lockedCtx context.Context) error {
		_, err := pull.RestackLocal(lockedCtx, request.WorkingArea, request.BaseSHA, request.Changes, requestID)
		return err
	})
}

func (backend LoomStackBackend) MergeUpTo(context.Context, StackRequest, string) error {
	return loomgit.NewError(loomgit.MergeNotAuthorized, "Loom backend has no automatic merge authorization", nil)
}

type backendRecorder interface {
	RecordStackBackend(context.Context, string, string, string) error
}

func chooseStackBackend(ctx context.Context, recorder backendRecorder, workspace, stackID, slug string, forge Forge, loom, native StackBackend) (StackBackend, error) {
	if recorder == nil || workspace == "" || stackID == "" || loom == nil {
		return nil, errors.New("stack backend selection requires a recorder, workspace, stack and Loom backend")
	}
	if _, err := refname.ChangeBranch(workspace, stackID); err != nil {
		return nil, err
	}
	backend, name := loom, "loom"
	enabled := false
	if capabilityForge, ok := forge.(interface {
		NativeStacksEnabled(context.Context, string, string) (bool, error)
	}); ok {
		parts := strings.Split(slug, "/")
		if len(parts) != 2 {
			return nil, errors.New("native stack repository slug is invalid")
		}
		var err error
		enabled, err = capabilityForge.NativeStacksEnabled(ctx, parts[0], parts[1])
		if err != nil {
			return nil, err
		}
	} else if capabilityForge, ok := forge.(interface{ SupportsNativeStacks() bool }); ok {
		enabled = capabilityForge.SupportsNativeStacks()
	}
	if enabled {
		if native == nil {
			return nil, loomgit.NewError(loomgit.ProviderStackLimit, "native stack backend is unavailable", nil)
		}
		backend, name = native, "native"
	}
	if err := recorder.RecordStackBackend(ctx, workspace, stackID, name); err != nil {
		return nil, err
	}
	return backend, nil
}
