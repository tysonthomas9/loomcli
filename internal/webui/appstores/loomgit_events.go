package appstores

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/outbox"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type loomGitEventSink struct {
	bus   *events.Bus
	hub   *realtime.Hub
	store outbox.JSONLStore
}

func (sink loomGitEventSink) Emit(ctx context.Context, event loomgit.OutboxEvent) error {
	var subject struct {
		Workspace string `json:"workspace"`
		ChangeID  string `json:"change_id"`
	}
	if err := json.Unmarshal(event.Payload, &subject); err != nil {
		return err
	}
	if subject.Workspace == "" {
		return fmt.Errorf("loom git event %d has no workspace", event.ID)
	}
	id := "loomgit:" + strconv.FormatInt(event.ID, 10)
	if err := outbox.EmitJSONL(ctx, sink.store, sink.bus, event); err != nil {
		return err
	}
	if !sink.hub.TryBroadcast(&realtime.MutationPayload{EventID: id, Type: "update", EntityType: "change",
		EntityID: subject.ChangeID, Action: event.Kind, WorkspaceID: subject.Workspace,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}) {
		return fmt.Errorf("loom git event %d SSE queue is full", event.ID)
	}
	return nil
}

func StartLoomGitEvents(ctx context.Context, hub *realtime.Hub, logger *slog.Logger) func() {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoomGitEvents(ctx, hub, logger)
	}()
	return func() { cancel(); <-done }
}

func runLoomGitEvents(ctx context.Context, hub *realtime.Hub, logger *slog.Logger) {
	root := bootstrap.LoomDir()
	if root == "" {
		logger.Error("loom git event store location unavailable")
		return
	}
	dir := os.Getenv("LOOM_EVENTS_DIR")
	if dir == "" {
		dir = filepath.Join(root, "events")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Error("loom git event log unavailable", "err", err)
		return
	}
	bus := events.NewBus(dir)
	defer func() { _ = bus.Close() }()
	path := filepath.Join(root, "loomgit", "store.db")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := dispatchLoomGitEvents(ctx, path, loomGitEventSink{bus: bus, hub: hub}); err != nil && ctx.Err() == nil {
			logger.Warn("loom git event delivery failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func dispatchLoomGitEvents(ctx context.Context, path string, sink loomGitEventSink) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	store, err := outbox.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	sink.store = store
	if err := outbox.Dispatch(ctx, store, sink); err != nil {
		return err
	}
	return publish.ReconcileEpicPublicationsAt(ctx, path)
}
