package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestEnsureStoreBackedSSESubscriberActivatesBackendOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hub := appstores.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	multiSub := appstores.NewMultiSub(ctx, hub, slog.Default())
	t.Cleanup(multiSub.Stop)

	calls := 0
	app := &Server{
		multiSub: multiSub,
		config: webui.ServerConfig{
			IssueBackendFn: func(ctx context.Context) backend.IssueBackend {
				calls++
				if got := middleware.WorkspaceFromContext(ctx); got != "WS2" {
					t.Fatalf("workspace in backend context = %q, want WS2", got)
				}
				return appTestIssueBackend{}
			},
		},
	}

	app.ensureStoreBackedSSESubscriber(context.Background(), "WS2")
	if calls != 1 {
		t.Fatalf("IssueBackendFn calls = %d, want 1", calls)
	}
	if !multiSub.HasSubscriber("WS2") {
		t.Fatal("subscriber was not registered")
	}

	app.ensureStoreBackedSSESubscriber(context.Background(), "WS2")
	if calls != 1 {
		t.Fatalf("IssueBackendFn calls after existing subscriber = %d, want 1", calls)
	}
}

func TestEnsureStoreBackedSSESubscriberGuardBranches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hub := appstores.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	multiSub := appstores.NewMultiSub(ctx, hub, slog.Default())
	t.Cleanup(multiSub.Stop)

	(*Server)(nil).ensureStoreBackedSSESubscriber(ctx, "WS")
	(&Server{multiSub: multiSub}).ensureStoreBackedSSESubscriber(ctx, "WS")
	(&Server{
		multiSub: multiSub,
		config: webui.ServerConfig{
			IssueBackendFn: func(context.Context) backend.IssueBackend { return nil },
		},
	}).ensureStoreBackedSSESubscriber(ctx, "WS")

	if multiSub.HasSubscriber("WS") {
		t.Fatal("guard branches should not register a subscriber")
	}
}

func TestActivateSSESubscriberRegisteredAndStoreBackedBranches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hub := appstores.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	multiSub := appstores.NewMultiSub(ctx, hub, slog.Default())
	t.Cleanup(multiSub.Stop)

	registry := appinfra.NewWorkspaceRegistry(slog.Default())
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register("WS1", t.TempDir()); err != nil {
		t.Fatalf("register workspace: %v", err)
	}

	app := &Server{multiSub: multiSub, registry: registry}
	app.activateSSESubscriber(ctx, "")
	app.activateSSESubscriber(ctx, "WS1")
	if multiSub.HasSubscriber("WS1") {
		t.Fatal("registered workspace without activation hook should not fall through to store-backed subscriber")
	}

	app.config.IssueBackendFn = func(context.Context) backend.IssueBackend {
		return appTestIssueBackend{}
	}
	app.activateSSESubscriber(ctx, "WS2")
	if !multiSub.HasSubscriber("WS2") {
		t.Fatal("store-backed subscriber was not activated")
	}
}

type appTestIssueBackend struct {
	backend.IssueBackend
}

func (appTestIssueBackend) GetMutations(context.Context, int64) ([]backend.MutationData, error) {
	return nil, nil
}

func (appTestIssueBackend) WaitForMutations(ctx context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, nil
	}
}

func TestShouldRegisterInitialWorkspaceBranches(t *testing.T) {
	cases := []struct {
		name        string
		workspaceID string
		fn          func() (map[string]string, error)
		want        bool
	}{
		{name: "empty workspace", fn: func() (map[string]string, error) { return map[string]string{"WS": "/repo"}, nil }},
		{name: "nil paths", workspaceID: "WS"},
		{name: "list error", workspaceID: "WS", fn: func() (map[string]string, error) { return nil, errors.New("boom") }},
		{name: "missing workspace", workspaceID: "WS", fn: func() (map[string]string, error) { return map[string]string{"OTHER": "/repo"}, nil }},
		{name: "empty path", workspaceID: "WS", fn: func() (map[string]string, error) { return map[string]string{"WS": ""}, nil }},
		{name: "registered", workspaceID: "WS", fn: func() (map[string]string, error) { return map[string]string{"WS": "/repo"}, nil }, want: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRegisterInitialWorkspace(tt.fn, tt.workspaceID); got != tt.want {
				t.Fatalf("shouldRegisterInitialWorkspace() = %t, want %t", got, tt.want)
			}
		})
	}
}
