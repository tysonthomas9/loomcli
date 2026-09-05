package realtime

import "testing"

// No hub goroutine or timing assumptions: make room in the fast queue while
// an older accepted event is in retry, then admit a newer source event.
func TestHub_NewBroadcastCannotOvertakeRetryBacklog(t *testing.T) {
	h := NewHub()
	h.addClient(NewClient(1, 1, "", nil, "ws"))
	for i := 0; i < cap(h.broadcast); i++ {
		h.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "c1.prefix"})
	}
	h.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "c1.older"})
	for len(h.broadcast) > 0 {
		<-h.broadcast
	}
	h.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "c1.newer"})
	h.drainRetryQueue()
	for _, want := range []string{"c1.older", "c1.newer"} {
		select {
		case event := <-h.broadcast:
			if event.Cursor != want {
				t.Fatalf("delivered %q before expected %q", event.Cursor, want)
			}
		default:
			t.Fatalf("missing accepted event %q", want)
		}
	}
}

func TestHub_PartialRetryDrainKeepsAdmissionOrder(t *testing.T) {
	h := NewHub()
	h.broadcast = make(chan *MutationPayload, 2)
	h.addClient(NewClient(1, 1, "", nil, "ws"))
	for _, cursor := range []string{"prefix-a", "prefix-b", "retry-a", "retry-b"} {
		h.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: cursor})
	}
	<-h.broadcast // Allow only the first retry to move to the fast queue.
	h.drainRetryQueue()
	<-h.broadcast
	h.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "newer"})
	if got := (<-h.broadcast).Cursor; got != "retry-a" {
		t.Fatalf("first retried event = %q", got)
	}
	h.drainRetryQueue()
	for _, want := range []string{"retry-b", "newer"} {
		select {
		case event := <-h.broadcast:
			if event.Cursor != want {
				t.Fatalf("got %q, want %q", event.Cursor, want)
			}
		default:
			t.Fatalf("missing %q", want)
		}
	}
	if got := h.GetRetryQueueDepth(); got != 0 {
		t.Fatalf("retry depth = %d", got)
	}
	if got := h.GetDroppedCount(); got != 0 {
		t.Fatalf("unexpected drops = %d", got)
	}
}
