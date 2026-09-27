//go:build daemon_bugreplay

package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestBugReplay626dSubscriberPropagatesCatchUpFailure(t *testing.T) {
	backendFailure := errors.New("durable replay unavailable")
	fake := newFakeBackend()
	fake.getFn = func(context.Context, int64) ([]backend.MutationData, error) {
		return []backend.MutationData{{Cursor: "1-0"}}, backendFailure
	}
	subscriber := NewBackendMutationSubscriber(fake, realtime.NewHub(), "ws-replay")
	partial, err := subscriber.GetMutationDataSince("0")
	if !errors.Is(err, backendFailure) {
		t.Fatalf("catch-up error = %v, want %v; a connected frame would claim recovery", err, backendFailure)
	}
	if len(partial) != 1 || partial[0].Cursor != "1-0" {
		t.Fatalf("catch-up prefix = %v, want successful page before failure", partial)
	}
}
