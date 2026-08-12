package memstore

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func hookPipeline() *domain.AgentHooks {
	return &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
	}}
}

func TestAgentStore_HooksRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newAgentStore()

	created, err := s.Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "critic", RoleName: "critic", Hooks: hookPipeline(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.Hooks.Equal(hookPipeline()) {
		t.Fatalf("created hooks = %+v", created.Hooks)
	}

	got, err := s.Get(ctx, "WS", "critic")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Hooks.Equal(hookPipeline()) {
		t.Errorf("get hooks = %+v", got.Hooks)
	}
	// Order is load-bearing: the comment must stay first.
	if got.Hooks.OnComplete[0].Type != domain.AgentHookActionComment {
		t.Errorf("stored order changed: %+v", got.Hooks.OnComplete)
	}

	list, err := s.List(ctx, "WS")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || !list[0].Hooks.Equal(hookPipeline()) {
		t.Errorf("list hooks = %+v", list)
	}
}

func TestAgentStore_CreateNormalizesEmptyHooks(t *testing.T) {
	ctx := context.Background()
	s := newAgentStore()

	created, err := s.Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "plain", RoleName: "critic", Hooks: &domain.AgentHooks{},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Hooks != nil {
		t.Errorf("Hooks = %+v, want nil so an empty pipeline is never stored", created.Hooks)
	}
}

func TestAgentStore_UpdateHooks(t *testing.T) {
	ctx := context.Background()
	s := newAgentStore()
	if _, err := s.Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "critic", RoleName: "critic", Hooks: hookPipeline(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("nil patch leaves the pipeline untouched", func(t *testing.T) {
		auto := true
		got, err := s.Update(ctx, "WS", "critic", store.AgentUpdate{CrossRepo: &auto})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !got.Hooks.Equal(hookPipeline()) {
			t.Errorf("Hooks = %+v, want unchanged", got.Hooks)
		}
	})

	t.Run("replace", func(t *testing.T) {
		replacement := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
			{Type: domain.AgentHookActionAddLabel, Value: "done"},
		}}
		got, err := s.Update(ctx, "WS", "critic", store.AgentUpdate{Hooks: replacement})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !got.Hooks.Equal(replacement) {
			t.Errorf("Hooks = %+v, want the replacement", got.Hooks)
		}
	})

	t.Run("clear", func(t *testing.T) {
		got, err := s.Update(ctx, "WS", "critic", store.AgentUpdate{Hooks: &domain.AgentHooks{}})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if got.Hooks != nil {
			t.Errorf("Hooks = %+v, want nil after a clear", got.Hooks)
		}
		reread, err := s.Get(ctx, "WS", "critic")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if reread.Hooks != nil {
			t.Errorf("re-read Hooks = %+v, want nil", reread.Hooks)
		}
	})
}

func TestAgentStore_HooksCloneIsolation(t *testing.T) {
	ctx := context.Background()
	s := newAgentStore()

	input := hookPipeline()
	if _, err := s.Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "critic", RoleName: "critic", Hooks: input,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Mutating the caller's input must not reach the store.
	input.OnComplete[1].Value = "mutated-input"
	got, err := s.Get(ctx, "WS", "critic")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Hooks.OnComplete[1].Value != "criticized" {
		t.Error("the store aliased the caller's create slice")
	}

	// Mutating a returned agent must not reach the store either.
	got.Hooks.OnComplete[1].Value = "mutated-read"
	reread, err := s.Get(ctx, "WS", "critic")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if reread.Hooks.OnComplete[1].Value != "criticized" {
		t.Error("a returned agent aliased the stored slice")
	}
}
