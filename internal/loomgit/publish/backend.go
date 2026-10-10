package publish

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
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

func (GitHubStackBackend) Restack(context.Context, StackRequest, string) error {
	return loomgit.NewError(loomgit.AttentionRequired, "native restack must adopt provider heads through landing", nil)
}

func (backend GitHubStackBackend) MergeUpTo(ctx context.Context, request StackRequest, target string) error {
	if err := requireMergeAuthority(ctx, request, target); err != nil {
		return err
	}
	if err := requireDependenciesLanded(ctx, backend.Store, request, target); err != nil {
		return err
	}
	if request.forge == nil {
		token := request.token
		if token == "" {
			token = githubtoken.GitHub(ctx)
		}
		if token == "" {
			return errors.New("GitHub host credential unavailable")
		}
		request.forge = stackpublish.NewConfiguredGitHubForge(token)
	}
	forge, ok := request.forge.(nativeMergeForge)
	if !ok {
		return errors.New("forge cannot merge native stacks")
	}
	store, ok := backend.Store.(nativeMergeStore)
	if !ok {
		return errors.New("store cannot journal native merges")
	}
	if err := beginNativeMerge(ctx, backend.Store, request, target); err != nil {
		return err
	}
	return ReconcileNativeMerges(ctx, store, forge)
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

func (backend LoomStackBackend) MergeUpTo(ctx context.Context, request StackRequest, target string) error {
	return beginLoomMerge(ctx, backend.Store, request, target)
}

type backendRecorder interface {
	RecordStackBackend(context.Context, string, string, string) error
	DeliveryMode(context.Context, string) (string, error)
}

func requireStackMode(ctx context.Context, store interface {
	DeliveryMode(context.Context, string) (string, error)
}, workspace string) error {
	mode, err := store.DeliveryMode(ctx, workspace)
	if err != nil {
		return err
	}
	if mode != "stack" {
		return loomgit.NewError(loomgit.ModeMismatch, "stack publication requires stack delivery mode", nil)
	}
	return nil
}

func chooseStackBackend(ctx context.Context, recorder backendRecorder, workspace, stackID, slug string, forge Forge, loom, native StackBackend) (StackBackend, error) {
	if recorder == nil || workspace == "" || stackID == "" || loom == nil {
		return nil, errors.New("stack backend selection requires a recorder, workspace, stack and Loom backend")
	}
	if err := requireStackMode(ctx, recorder, workspace); err != nil {
		return nil, err
	}
	if _, err := refname.ChangeBranch(workspace, stackID); err != nil {
		return nil, err
	}
	backend, name := loom, "loom"
	// GitHub (the forge that reports native stack support) always publishes
	// natively (D41); other providers and local mode use Loom's publisher.
	if github, ok := forge.(interface {
		NativeStacksEnabled(context.Context, string, string) (bool, error)
	}); ok {
		parts := strings.Split(slug, "/")
		if len(parts) != 2 {
			return nil, errors.New("native stack repository slug is invalid")
		}
		enabled, err := github.NativeStacksEnabled(ctx, parts[0], parts[1])
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, nativeStacksOff(slug)
		}
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

// errNativeStacksOff marks a GitHub repository without native stacked pull
// requests: nothing publishes until the user turns them on and retries.
var errNativeStacksOff = errors.New("native stacks are off")

func nativeStacksOff(slug string) error {
	return loomgit.NewError(loomgit.AttentionRequired, "GitHub native stacks are off for "+slug+
		". Turn on stacked pull requests for "+slug+" in GitHub, then retry", errNativeStacksOff)
}

// nativeStacksOffReason returns the user-facing text of a native-stacks-off
// error, or "" for any other error.
func nativeStacksOffReason(err error) string {
	var coded *loomgit.Error
	if errors.Is(err, errNativeStacksOff) && errors.As(err, &coded) {
		return coded.Message
	}
	return ""
}
