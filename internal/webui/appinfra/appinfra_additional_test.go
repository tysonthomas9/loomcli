package appinfra

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

func TestAppInfraSimpleConstructors(t *testing.T) {
	if ShortWorkspaceID("workspace-abcdef123456") == "" {
		t.Fatalf("ShortWorkspaceID returned empty")
	}
	if InitProtectedPool(nil, nil) == nil {
		t.Fatalf("InitProtectedPool returned nil")
	}
	if NewFleetClaimMetrics() == nil {
		t.Fatalf("NewFleetClaimMetrics returned nil")
	}

	tokenCfg := NewFleetTokenConfig([]byte("secret"), time.Minute)
	if string(tokenCfg.SigningKey) != "secret" || tokenCfg.Expiry != time.Minute {
		t.Fatalf("token config = %+v, want signing key and expiry", tokenCfg)
	}

	regCfg, cleanup := NewFleetRegisterConfig("api-key", nil, slog.Default())
	if regCfg == nil || regCfg.APIKey != "api-key" {
		t.Fatalf("register config = %+v, want api key", regCfg)
	}
	if cleanup != nil {
		t.Fatalf("cleanup was non-nil without redis")
	}
	if NewRedisClient("127.0.0.1:6379", "") == nil {
		t.Fatalf("NewRedisClient returned nil")
	}
	regCfg, cleanup = NewFleetRegisterConfig("api-key", &fleet.RedisConfig{Address: "127.0.0.1:1"}, nil)
	if regCfg == nil || regCfg.RateLimiter == nil || cleanup == nil {
		t.Fatalf("redis register config = %+v cleanup=%t", regCfg, cleanup != nil)
	}
	cleanup()
	if cwd, err := GetCwd(); err != nil || cwd == "" {
		t.Fatalf("GetCwd = %q, %v; want cwd", cwd, err)
	}
}

func TestReconcileNewWorkspacesRegistersOnlyUnknownWorkspaces(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	knownPath := t.TempDir()
	newPath := t.TempDir()
	if err := registry.Register("known", knownPath); err != nil {
		t.Fatalf("register known: %v", err)
	}

	reconcileNewWorkspaces(func() (map[string]string, error) {
		return map[string]string{
			"known": knownPath,
			"new":   newPath,
		}, nil
	}, registry, slog.Default())

	ids := registry.WorkspaceIDs()
	if len(ids) != 2 {
		t.Fatalf("WorkspaceIDs = %#v, want two registered workspaces", ids)
	}

	reconcileNewWorkspaces(func() (map[string]string, error) {
		return nil, context.Canceled
	}, registry, slog.Default())
	reconcileNewWorkspaces(func() (map[string]string, error) {
		return map[string]string{"": t.TempDir()}, nil
	}, registry, slog.Default())
}

func TestStartPeriodicWorkspaceReconcileHandlesNilAndRegisters(t *testing.T) {
	StartPeriodicWorkspaceReconcile(context.Background(), nil, NewWorkspaceRegistry(slog.Default()), time.Millisecond, nil)
	StartPeriodicWorkspaceReconcile(context.Background(), func() (map[string]string, error) {
		return nil, nil
	}, nil, time.Millisecond, nil)
	canceled, stop := context.WithCancel(context.Background())
	stop()
	StartPeriodicWorkspaceReconcile(canceled, func() (map[string]string, error) {
		return nil, nil
	}, NewWorkspaceRegistry(slog.Default()), 0, nil)

	registry := NewWorkspaceRegistry(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartPeriodicWorkspaceReconcile(ctx, func() (map[string]string, error) {
		return map[string]string{"periodic": t.TempDir()}, nil
	}, registry, time.Millisecond, nil)

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(registry.WorkspaceIDs()) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("periodic reconcile did not register workspace")
}

func TestFleetRegistryAndModuleConstructors(t *testing.T) {
	registry, err := InitFleetRegistry(fleet.RedisConfig{Address: "127.0.0.1:1"}, slog.Default())
	if err != nil {
		t.Fatalf("InitFleetRegistry: %v", err)
	}
	if registry == nil {
		t.Fatal("InitFleetRegistry returned nil registry")
	}
	module := NewFleetModule(
		registry,
		NewFleetTokenConfig([]byte("secret"), time.Minute),
		nil,
		NewFleetClaimMetrics(),
		&FleetRegisterConfig{APIKey: "key"},
	)
	if module == nil {
		t.Fatal("NewFleetModule returned nil")
	}
}
