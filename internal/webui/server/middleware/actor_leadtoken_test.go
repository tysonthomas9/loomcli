package middleware_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestOccupantPrefixMatchesLeadtoken(t *testing.T) {
	want := leadtoken.OccupantActor("p1")
	actor, err := middleware.OccupantActorFor(want)
	if err != nil {
		t.Fatalf("OccupantActorFor(%q): %v", want, err)
	}
	if got := actor.BackendActor(); got != want {
		t.Fatalf("BackendActor() = %q, want %q", got, want)
	}
}
