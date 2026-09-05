package realtime

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransientMutationOmitsIDRegardlessOfTimestamp(t *testing.T) {
	for _, stamp := range []string{"", "invalid", "2026-09-05T12:00:00Z"} {
		rr := httptest.NewRecorder()
		writer, err := NewWriter(rr)
		if err != nil {
			t.Fatal(err)
		}
		mutation := &MutationPayload{Type: "refresh", Timestamp: stamp, deliveryCursor: "c1.internal-only"}
		if err := writeSSEEvent(writer, mutation); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rr.Body.String(), "id:") {
			t.Fatalf("transient frame changed checkpoint: %q", rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "event: mutation") {
			t.Fatal(rr.Body.String())
		}
	}
}

func TestTransientOverflowPreservesResumeAcrossReplay(t *testing.T) {
	for _, withDurable := range []bool{false, true} {
		client := NewClient(1, 4, "c1.old-resume", nil, "WS")
		first := client.prepareDelivery(&MutationPayload{Type: "refresh", WorkspaceID: "WS"})
		if withDurable {
			first = client.prepareDelivery(&MutationPayload{Type: "update", Cursor: "c1.queued", WorkspaceID: "WS"})
		}
		last := client.prepareDelivery(&MutationPayload{Type: "refresh", WorkspaceID: "WS"})
		if last.deliveryCursor != "" {
			t.Fatal("transient delivery inherited a cursor")
		}
		rr := httptest.NewRecorder()
		writer, err := NewWriter(rr)
		if err != nil {
			t.Fatal(err)
		}
		// A completed replay already put a newer durable checkpoint on the wire.
		if err := writer.WriteEventID("c1.replay-head", "checkpoint", "{}"); err != nil {
			t.Fatal(err)
		}
		rr.Body.Reset()
		seq := uint64(0)
		h := NewHandler(HandlerConfig{})
		_, err = h.writeOverflowResync(writer, client, first, resyncPoint{seq: last.deliverySeq}, &seq)
		if err != nil {
			t.Fatal(err)
		}
		if seq != last.deliverySeq {
			t.Fatalf("drained sequence=%d", seq)
		}
		if strings.Contains(rr.Body.String(), "id:") || !strings.Contains(rr.Body.String(), "event: resync") {
			t.Fatalf("overflow changed checkpoint: %q", rr.Body.String())
		}
	}
}

func TestOverflowBeforeAnyOfferDoesNotReuseInitialResume(t *testing.T) {
	client := NewClient(1, 1, "c1.resume", nil, "WS")
	client.markCurrentDropped()
	point, ok := client.beginResync()
	if !ok || point.cursor != "" {
		t.Fatalf("point=%+v pending=%v", point, ok)
	}
}

func TestPreparedTransientMutationOmitsID(t *testing.T) {
	rr := httptest.NewRecorder()
	writer, err := NewWriter(rr)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePreparedMutation(writer, preparedMutation{payload: &MutationPayload{Type: "refresh"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rr.Body.String(), "id:") || !strings.Contains(rr.Body.String(), "event: mutation") {
		t.Fatalf("prepared transient changed checkpoint: %q", rr.Body.String())
	}
}
