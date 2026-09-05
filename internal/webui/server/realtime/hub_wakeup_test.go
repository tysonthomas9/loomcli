package realtime

import "testing"

func TestHub_DurableWakeupsBypassSaturatedTransportQueues(t *testing.T) {
	h := NewHub()
	reader := NewClient(1, 1, "c1.start", []string{"repo-a"}, "ws")
	reader.authoritative = true
	other := NewClient(2, 1, "", nil, "other")
	other.authoritative = true
	h.addClient(reader)
	h.addClient(other)
	// Saturate transport with transient hints; durable wakeups must still arrive.
	for i := 0; i < cap(h.broadcast)+retryQueueCapacity; i++ {
		h.Broadcast(&MutationPayload{WorkspaceID: "ws", SourceRepo: "repo-a", Type: "refresh"})
	}
	for i := 0; i < 100; i++ {
		h.Broadcast(&MutationPayload{WorkspaceID: "ws", SourceRepo: "repo-b", Cursor: "opaque"})
	}
	if len(reader.wake) != 1 || len(other.wake) != 0 {
		t.Fatalf("wake counts: reader=%d other=%d", len(reader.wake), len(other.wake))
	}
	if h.GetDroppedCount() != 0 {
		t.Fatal("durable wakeups entered full payload queue")
	}
	if _, pending := reader.beginResync(); pending {
		t.Fatal("durable wakeups caused payload resync")
	}
	<-reader.wake
	// A wake arriving after consumption must survive for the next read.
	h.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "next"})
	if len(reader.wake) != 1 {
		t.Fatal("wake during read lost")
	}
}

func TestHub_MixedClientsReceiveDurableWakeOrPayload(t *testing.T) {
	h := NewHub()
	reader := NewClient(1, 2, "", nil, "ws")
	reader.authoritative = true
	payloadClient := NewClient(2, 2, "", nil, "ws")
	h.addClient(reader)
	h.addClient(payloadClient)
	event := &MutationPayload{WorkspaceID: "ws", Cursor: "opaque"}
	h.Broadcast(event)
	h.fanOutMutation(<-h.broadcast)
	if len(reader.wake) != 1 || len(reader.send) != 0 {
		t.Fatal("reader received durable payload or lost wake")
	}
	if got := (<-payloadClient.send).Cursor; got != "opaque" {
		t.Fatalf("payload cursor=%q", got)
	}
}
