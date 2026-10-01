//go:build daemon_bugreplay

package subscription

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

// historyCursorBackend serves a retained log whose entries the browser has
// already applied. A long-poll from cursor "0" returns that whole history.
type historyCursorBackend struct {
	*fakeBackend
	mu      sync.Mutex
	cursors []string
}

func (b *historyCursorBackend) GetMutationsAfter(context.Context, string) ([]backend.MutationData, error) {
	return nil, nil
}

func (b *historyCursorBackend) WaitForMutationsAfter(ctx context.Context, since string, _ int64) ([]backend.MutationData, error) {
	b.mu.Lock()
	b.cursors = append(b.cursors, since)
	b.mu.Unlock()
	if since == "0" {
		now := time.Now()
		return []backend.MutationData{
			{Cursor: "1-0", Type: "update", IssueID: "issue-1", Timestamp: now},
			{Cursor: "2-0", Type: "update", IssueID: "issue-2", Timestamp: now},
		}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBugReplay610a(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()
	client := realtime.NewClient(1, realtime.ClientSendBuf, "2-0", nil, "ws-replay")
	hub.RegisterClient(client)

	fake := &historyCursorBackend{fakeBackend: newFakeBackend()}
	subscribers := NewMultiWorkspaceSubscriber(hub, nil)
	defer subscribers.Stop()
	if err := subscribers.EnsureActive(context.Background(), "ws-replay", fake, ActivationReasonSSE); err != nil {
		t.Fatal(err)
	}

	select {
	case payload := <-client.Send():
		fake.mu.Lock()
		cursors := append([]string(nil), fake.cursors...)
		fake.mu.Unlock()
		t.Fatalf("#610a: activated subscriber long-polled from %v and re-broadcast retained history cursor %q to a client resumed at 2-0", cursors, payload.Cursor)
	case <-time.After(500 * time.Millisecond):
	}
}
