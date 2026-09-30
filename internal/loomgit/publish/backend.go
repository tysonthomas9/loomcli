package publish

import (
	"context"
	"errors"

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

func (backend LoomStackBackend) Capabilities() StackCapabilities { return StackCapabilities{} }

func (backend LoomStackBackend) Publish(ctx context.Context, request StackRequest) ([]loomgit.Revision, error) {
	if len(request.Changes) == 0 || request.StackID == "" {
		return nil, errors.New("stack ID and changes are required")
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

func chooseStackBackend(ctx context.Context, recorder backendRecorder, workspace, stackID string, forge Forge, loom, native StackBackend) (StackBackend, error) {
	if recorder == nil || workspace == "" || stackID == "" || loom == nil {
		return nil, errors.New("stack backend selection requires a recorder, workspace, stack and Loom backend")
	}
	if _, err := refname.ChangeBranch(workspace, stackID); err != nil {
		return nil, err
	}
	backend, name := loom, "loom"
	if capabilityForge, ok := forge.(interface{ SupportsNativeStacks() bool }); ok && capabilityForge.SupportsNativeStacks() {
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
