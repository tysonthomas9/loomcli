package appstores

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/outbox"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestLoomGitEventGoesToJSONLAndSSEWithSameID(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus(dir)
	t.Cleanup(func() { _ = bus.Close() })
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	client := realtime.NewClient(1, realtime.ClientSendBuf, "", nil, "W")
	hub.RegisterClient(client)
	event := loomgit.OutboxEvent{ID: 42, Kind: "git.integrated",
		Payload: []byte(`{"workspace":"W","change_id":"C","revision":3,"workspace_sha":"abc"}`)}
	if err := (loomGitEventSink{bus: bus, hub: hub}).Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-client.Send():
		if got.EventID != "loomgit:42" || got.Action != "git.integrated" || got.EntityID != "C" {
			t.Fatalf("SSE event: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("SSE event was not broadcast")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("event logs: %+v, %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil || !strings.Contains(string(data), `"event_id":"loomgit:42"`) {
		t.Fatalf("JSONL event: %s, %v", data, err)
	}
}

func TestDispatchLoomGitEventsDrainsDurableStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.db")
	opened, err := outbox.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := opened.(loomgit.Store)
	entry, _, err := store.Begin(ctx, "integrate-1", "integrate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(ctx, entry, "done", nil, []loomgit.OutboxEvent{{Kind: "git.integrated",
		Payload: []byte(`{"workspace":"W","change_id":"C"}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(t.TempDir())
	defer func() { _ = bus.Close() }()
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()
	client := realtime.NewClient(1, realtime.ClientSendBuf, "", nil, "W")
	hub.RegisterClient(client)
	if err := dispatchLoomGitEvents(ctx, path, loomGitEventSink{bus: bus, hub: hub}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-client.Send():
		if got.EventID != "loomgit:1" {
			t.Fatalf("SSE event: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("durable event was not broadcast")
	}
	opened, err = outbox.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	if pending, err := opened.PendingEvents(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("event not marked delivered: %+v, %v", pending, err)
	}
}

func TestDispatchLoomGitEventsRetriesFullHub(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.db")
	store, err := outbox.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	entry, _, err := store.(loomgit.Store).Begin(ctx, "integrate-1", "integrate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(loomgit.Store).Advance(ctx, entry, "done", nil, []loomgit.OutboxEvent{{Kind: "git.integrated",
		Payload: []byte(`{"workspace":"W","change_id":"C"}`)}}); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(t.TempDir())
	defer func() { _ = bus.Close() }()
	hub := realtime.NewHub()
	for range 1280 {
		if !hub.TryBroadcast(&realtime.MutationPayload{WorkspaceID: "W"}) {
			t.Fatal("hub rejected mutation before capacity")
		}
	}
	if err := outbox.Dispatch(ctx, store, loomGitEventSink{bus: bus, hub: hub}); err == nil {
		t.Fatal("dispatch accepted a full hub")
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 1 {
		t.Fatalf("event lost after full hub: %+v, %v", pending, err)
	}
	go hub.Run()
	defer hub.Stop()
	deadline := time.Now().Add(time.Second)
	for {
		if err := outbox.Dispatch(ctx, store, loomGitEventSink{bus: bus, hub: hub}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hub did not regain capacity")
		}
		time.Sleep(time.Millisecond)
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("event not delivered after retry: %+v, %v", pending, err)
	}
}
