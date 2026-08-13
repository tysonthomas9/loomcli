package middleware

import (
	"context"
	"testing"
)

func TestWithActor_DoesNotSynthesizeUserIdentity(t *testing.T) {
	actor, err := OccupantActorFor("lead-occupant:p1")
	if err != nil {
		t.Fatalf("OccupantActorFor: %v", err)
	}

	ctx := WithActor(context.Background(), actor)
	if _, ok := UserIdentityFromContext(ctx); ok {
		t.Fatal("UserIdentityFromContext found an identity for an occupant actor")
	}
}

func TestWithActor_DoesNotClobberExistingUserIdentity(t *testing.T) {
	identity := UserIdentity{UserID: "user-1", Email: "user@example.com", Name: "User One"}
	actor, err := OccupantActorFor("lead-occupant:p1")
	if err != nil {
		t.Fatalf("OccupantActorFor: %v", err)
	}

	ctx := WithActor(WithUserIdentity(context.Background(), identity), actor)
	got, ok := UserIdentityFromContext(ctx)
	if !ok {
		t.Fatal("UserIdentityFromContext did not find the existing identity")
	}
	if got != identity {
		t.Fatalf("UserIdentityFromContext = %#v, want %#v", got, identity)
	}
}

func TestActorFromContext_AbsentIsFalse(t *testing.T) {
	if got, ok := ActorFromContext(context.Background()); ok {
		t.Fatalf("ActorFromContext = %#v, true; want zero Actor, false", got)
	}
}

func TestActorShapes(t *testing.T) {
	t.Run("web-ui", func(t *testing.T) {
		actor := WebUIActor()
		if err := actor.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if got := actor.Kind(); got != ActorKindWebUI {
			t.Fatalf("Kind() = %q, want %q", got, ActorKindWebUI)
		}
		if got := actor.BackendActor(); got != "" {
			t.Fatalf("BackendActor() = %q, want empty", got)
		}
		if got := actor.Attribution(); got != "web-ui" {
			t.Fatalf("Attribution() = %q, want web-ui", got)
		}
		if actor.OverridesClientAttribution() {
			t.Fatal("OverridesClientAttribution() = true, want false")
		}
	})

	t.Run("occupant", func(t *testing.T) {
		actor, err := OccupantActorFor("lead-occupant:p1")
		if err != nil {
			t.Fatalf("OccupantActorFor: %v", err)
		}
		if err := actor.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if got := actor.Kind(); got != ActorKindOccupant {
			t.Fatalf("Kind() = %q, want %q", got, ActorKindOccupant)
		}
		if got := actor.BackendActor(); got != "lead-occupant:p1" {
			t.Fatalf("BackendActor() = %q, want lead-occupant:p1", got)
		}
		if got := actor.Attribution(); got != "lead-occupant:p1" {
			t.Fatalf("Attribution() = %q, want lead-occupant:p1", got)
		}
		if !actor.OverridesClientAttribution() {
			t.Fatal("OverridesClientAttribution() = false, want true")
		}
	})
}

func TestValidate_Table(t *testing.T) {
	tests := []struct {
		name  string
		actor Actor
	}{
		{name: "zero", actor: Actor{}},
		{name: "unknown kind", actor: Actor{kind: "driver"}},
		{name: "web-ui with id", actor: Actor{kind: ActorKindWebUI, id: "x"}},
		{name: "occupant empty id", actor: Actor{kind: ActorKindOccupant}},
		{name: "occupant web-ui id", actor: Actor{kind: ActorKindOccupant, id: "web-ui"}},
		{name: "occupant no prefix", actor: Actor{kind: ActorKindOccupant, id: "no-prefix"}},
		{name: "occupant empty suffix", actor: Actor{kind: ActorKindOccupant, id: "lead-occupant:"}},
		{name: "occupant whitespace suffix", actor: Actor{kind: ActorKindOccupant, id: "lead-occupant:   "}},
		{name: "occupant leading whitespace", actor: Actor{kind: ActorKindOccupant, id: "lead-occupant: p1"}},
		{name: "occupant trailing whitespace", actor: Actor{kind: ActorKindOccupant, id: "lead-occupant:p1 "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.actor.Validate(); err == nil {
				t.Fatalf("Validate() = nil for %#v, want error", tt.actor)
			}
		})
	}
}

func TestOccupantActorFor_RejectsNoncanonical(t *testing.T) {
	for _, subject := range []string{
		"web-ui",
		"no-prefix",
		"lead-occupant:",
		"lead-occupant:   ",
		"lead-occupant: p1",
		"lead-occupant:p1 ",
	} {
		t.Run(subject, func(t *testing.T) {
			if actor, err := OccupantActorFor(subject); err == nil {
				t.Fatalf("OccupantActorFor(%q) = %#v, nil; want error", subject, actor)
			}
		})
	}

	actor, err := OccupantActorFor("lead-occupant:p1")
	if err != nil {
		t.Fatalf("OccupantActorFor canonical subject: %v", err)
	}
	if err := actor.Validate(); err != nil {
		t.Fatalf("canonical actor Validate: %v", err)
	}
}
