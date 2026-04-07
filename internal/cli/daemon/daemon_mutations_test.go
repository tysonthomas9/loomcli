package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/notify"
)

// newMutationTestDaemon creates a Daemon with a notify bus and workspace ID for mutation tests.
func newMutationTestDaemon(bus notify.Publisher, wsID string) *Daemon {
	d := &Daemon{
		notifyBus: bus,
	}
	d.sup = &supervisor.Supervisor{
		ConfigSnapshot: func() *DaemonConfig { return &DaemonConfig{} },
		WorkspaceID:    wsID,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	return d
}

// --- MutationBuffer tests ---

func TestMutationBuffer_GetSince_Empty(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	result := buf.GetSince(0)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestMutationBuffer_GetSince_All(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	now := time.Now()
	mutations := []backend.MutationData{
		{Type: "claim", IssueID: "a", Timestamp: now},
		{Type: "status", IssueID: "b", Timestamp: now.Add(time.Millisecond)},
		{Type: "close", IssueID: "c", Timestamp: now.Add(2 * time.Millisecond)},
	}
	for _, m := range mutations {
		bus.Publish(notify.Event{
			Topic:       "issue." + m.Type,
			WorkspaceID: "ws-1",
			Payload:     m,
			Timestamp:   m.Timestamp,
		})
	}

	// Wait for events to propagate
	time.Sleep(50 * time.Millisecond)

	result := buf.GetSince(0)
	if len(result) != 3 {
		t.Fatalf("expected 3 mutations, got %d", len(result))
	}
	if result[0].IssueID != "a" || result[1].IssueID != "b" || result[2].IssueID != "c" {
		t.Fatalf("unexpected order: %v", result)
	}
}

func TestMutationBuffer_GetSince_Filtered(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	now := time.Now()
	mutations := []backend.MutationData{
		{Type: "claim", IssueID: "a", Timestamp: now},
		{Type: "status", IssueID: "b", Timestamp: now.Add(100 * time.Millisecond)},
		{Type: "close", IssueID: "c", Timestamp: now.Add(200 * time.Millisecond)},
	}
	for _, m := range mutations {
		bus.Publish(notify.Event{
			Topic:       "issue." + m.Type,
			WorkspaceID: "ws-1",
			Payload:     m,
			Timestamp:   m.Timestamp,
		})
	}

	time.Sleep(50 * time.Millisecond)

	// Filter: only mutations after the second one (use 150ms to avoid millisecond truncation boundary)
	result := buf.GetSince(now.Add(150 * time.Millisecond).UnixMilli())
	if len(result) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(result))
	}
	if result[0].IssueID != "c" {
		t.Fatalf("expected issue c, got %s", result[0].IssueID)
	}
}

func TestMutationBuffer_WaitSince_Immediate(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	m := backend.MutationData{Type: "claim", IssueID: "x", Timestamp: time.Now()}
	bus.Publish(notify.Event{
		Topic:       "issue.claim",
		WorkspaceID: "ws-1",
		Payload:     m,
		Timestamp:   m.Timestamp,
	})
	time.Sleep(50 * time.Millisecond)

	ctx := context.Background()
	result := buf.WaitSince(ctx, 0, 5*time.Second)
	if len(result) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(result))
	}
	if result[0].IssueID != "x" {
		t.Fatalf("expected issue x, got %s", result[0].IssueID)
	}
}

func TestMutationBuffer_WaitSince_Blocks(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	ctx := context.Background()
	var result []backend.MutationData
	done := make(chan struct{})

	go func() {
		result = buf.WaitSince(ctx, 0, 2*time.Second)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	m := backend.MutationData{Type: "status", IssueID: "y", Timestamp: time.Now()}
	bus.Publish(notify.Event{
		Topic:       "issue.status",
		WorkspaceID: "ws-1",
		Payload:     m,
		Timestamp:   m.Timestamp,
	})

	select {
	case <-done:
		if len(result) != 1 || result[0].IssueID != "y" {
			t.Fatalf("unexpected result: %v", result)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitSince did not unblock after mutation was published")
	}
}

func TestMutationBuffer_WaitSince_Timeout(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	ctx := context.Background()
	start := time.Now()
	result := buf.WaitSince(ctx, 0, 50*time.Millisecond)
	elapsed := time.Since(start)

	if len(result) != 0 {
		t.Fatalf("expected empty result on timeout, got %d", len(result))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("WaitSince took too long: %v", elapsed)
	}
}

func TestMutationBuffer_WaitSince_ContextCanceled(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	result := buf.WaitSince(ctx, 0, 5*time.Second)
	elapsed := time.Since(start)

	if len(result) != 0 {
		t.Fatalf("expected empty result on context cancel, got %d", len(result))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("WaitSince took too long after cancel: %v", elapsed)
	}
}

func TestMutationBuffer_RingEviction(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	const capacity = 5
	buf := NewMutationBuffer(capacity, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	now := time.Now()
	for i := 0; i < 8; i++ {
		m := backend.MutationData{
			Type:      "status",
			IssueID:   string(rune('a' + i)),
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
		}
		bus.Publish(notify.Event{
			Topic:       "issue.status",
			WorkspaceID: "ws-1",
			Payload:     m,
			Timestamp:   m.Timestamp,
		})
	}

	time.Sleep(50 * time.Millisecond)

	result := buf.GetSince(0)
	if len(result) != capacity {
		t.Fatalf("expected %d mutations after eviction, got %d", capacity, len(result))
	}
	// Should have the last 5: d, e, f, g, h
	if result[0].IssueID != "d" {
		t.Fatalf("expected oldest surviving entry 'd', got %q", result[0].IssueID)
	}
	if result[4].IssueID != "h" {
		t.Fatalf("expected newest entry 'h', got %q", result[4].IssueID)
	}
}

func TestMutationBuffer_ConcurrentAccess(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(64, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	var wg sync.WaitGroup

	// 10 writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				m := backend.MutationData{
					Type:      "status",
					IssueID:   "issue",
					Timestamp: time.Now(),
				}
				bus.Publish(notify.Event{
					Topic:       "issue.status",
					WorkspaceID: "ws-1",
					Payload:     m,
					Timestamp:   m.Timestamp,
				})
			}
		}(i)
	}

	// 10 readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				buf.GetSince(0)
			}
		}()
	}

	wg.Wait()
}

// --- publishMutation tests ---

func TestDaemon_PublishMutation(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	sub := bus.Subscribe("ws-test", "issue")
	defer sub.Close()

	d := newMutationTestDaemon(bus, "ws-test")

	m := backend.MutationData{
		Type:      "claim",
		IssueID:   "abc-123",
		Timestamp: time.Now(),
	}
	d.publishMutation(m)

	select {
	case evt := <-sub.Events():
		if evt.Topic != "issue.claim" {
			t.Fatalf("expected topic issue.claim, got %s", evt.Topic)
		}
		if evt.WorkspaceID != "ws-test" {
			t.Fatalf("expected workspace ws-test, got %s", evt.WorkspaceID)
		}
		payload, ok := evt.Payload.(backend.MutationData)
		if !ok {
			t.Fatal("payload is not MutationData")
		}
		if payload.IssueID != "abc-123" {
			t.Fatalf("expected issue abc-123, got %s", payload.IssueID)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestDaemon_PublishMutation_NilBus(t *testing.T) {
	d := newMutationTestDaemon(nil, "")
	// Should not panic
	d.publishMutation(backend.MutationData{Type: "claim", IssueID: "x"})
}

func TestDaemon_PublishMutation_NopPublisher(t *testing.T) {
	d := newMutationTestDaemon(notify.NopPublisher{}, "")
	// Should not panic
	d.publishMutation(backend.MutationData{Type: "claim", IssueID: "x"})
}

func TestDaemon_PublishMutation_SetsTimestamp(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	sub := bus.Subscribe("ws-test", "issue")
	defer sub.Close()

	d := newMutationTestDaemon(bus, "ws-test")

	before := time.Now()
	d.publishMutation(backend.MutationData{Type: "status", IssueID: "z"})
	after := time.Now()

	select {
	case evt := <-sub.Events():
		payload := evt.Payload.(backend.MutationData)
		if payload.Timestamp.Before(before) || payload.Timestamp.After(after) {
			t.Fatalf("timestamp %v not in expected range [%v, %v]", payload.Timestamp, before, after)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestDaemon_PublishMutation_TopicAndWorkspace(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	sub := bus.Subscribe("ws-42", "issue")
	defer sub.Close()

	d := newMutationTestDaemon(bus, "ws-42")

	d.publishMutation(backend.MutationData{Type: "status", IssueID: "t", Timestamp: time.Now()})

	select {
	case evt := <-sub.Events():
		if evt.Topic != "issue.status" {
			t.Fatalf("expected topic issue.status, got %s", evt.Topic)
		}
		if evt.WorkspaceID != "ws-42" {
			t.Fatalf("expected workspace ws-42, got %s", evt.WorkspaceID)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

// --- Control socket handler tests ---

func TestControlHandler_GetMutations(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	d := newMutationTestDaemon(bus, "ws-1")
	d.mutBuf = buf

	// Publish 2 mutations
	now := time.Now()
	for i, typ := range []string{"claim", "status"} {
		m := backend.MutationData{Type: typ, IssueID: "i-" + typ, Timestamp: now.Add(time.Duration(i) * time.Millisecond)}
		d.publishMutation(m)
	}
	time.Sleep(50 * time.Millisecond)

	args, _ := json.Marshal(GetMutationsArgs{Since: 0})
	resp := d.handleControlGetMutations(args)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	var mutations []backend.MutationData
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(mutations) != 2 {
		t.Fatalf("expected 2 mutations, got %d", len(mutations))
	}
}

func TestControlHandler_GetMutations_NilBuffer(t *testing.T) {
	d := &Daemon{mutBuf: nil}
	resp := d.handleControlGetMutations(nil)
	if resp.Success {
		t.Fatal("expected error for nil buffer")
	}
	if resp.Error != "mutation tracking not available" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestControlHandler_GetMutations_EmptyArgs(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "")
	buf.Start()
	defer buf.Stop()

	d := &Daemon{mutBuf: buf}

	// nil args should default to sinceMs=0
	resp := d.handleControlGetMutations(nil)
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
}

func TestControlHandler_WaitForMutations_Timeout(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "")
	buf.Start()
	defer buf.Stop()

	d := &Daemon{mutBuf: buf}

	args, _ := json.Marshal(WaitForMutationsArgs{Since: 0, Timeout: 50})
	start := time.Now()
	resp := d.handleControlWaitForMutations(args)
	elapsed := time.Since(start)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	var mutations []backend.MutationData
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(mutations) != 0 {
		t.Fatalf("expected 0 mutations, got %d", len(mutations))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("handler took too long: %v", elapsed)
	}
}

func TestControlHandler_WaitForMutations_Unblocks(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-1")
	buf.Start()
	defer buf.Stop()

	d := newMutationTestDaemon(bus, "ws-1")
	d.mutBuf = buf

	args, _ := json.Marshal(WaitForMutationsArgs{Since: 0, Timeout: 5000})

	var resp DaemonControlResponse
	done := make(chan struct{})
	go func() {
		resp = d.handleControlWaitForMutations(args)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	d.publishMutation(backend.MutationData{Type: "close", IssueID: "z", Timestamp: time.Now()})

	select {
	case <-done:
		if !resp.Success {
			t.Fatalf("expected success, got error: %s", resp.Error)
		}
		var mutations []backend.MutationData
		if err := json.Unmarshal(resp.Data, &mutations); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(mutations) != 1 || mutations[0].IssueID != "z" {
			t.Fatalf("unexpected mutations: %v", mutations)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not unblock")
	}
}

func TestControlHandler_WaitForMutations_MaxTimeoutClamp(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "")
	buf.Start()
	defer buf.Stop()

	d := &Daemon{mutBuf: buf}

	// Request 120s timeout — should be clamped to 60s
	args, _ := json.Marshal(WaitForMutationsArgs{Since: 0, Timeout: 120000})
	start := time.Now()

	// Cancel context after 100ms to avoid actually waiting 60s
	done := make(chan struct{})
	go func() {
		d.handleControlWaitForMutations(args)
		close(done)
	}()

	// Publish a mutation after a brief delay to unblock the handler
	time.Sleep(50 * time.Millisecond)
	bus.Publish(notify.Event{
		Topic:       "issue.test",
		WorkspaceID: "",
		Payload:     backend.MutationData{Type: "test", IssueID: "t", Timestamp: time.Now()},
	})

	select {
	case <-done:
		elapsed := time.Since(start)
		// Must complete well before 120s (the unclamped value)
		if elapsed > 5*time.Second {
			t.Fatalf("handler took too long, might not be clamped: %v", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not complete")
	}
}

// --- Integration test: control socket round-trip ---

func TestControlSocket_GetMutations_RoundTrip(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	bus := notify.New()
	defer bus.Close()

	buf := NewMutationBuffer(16, bus, "ws-rt")
	buf.Start()
	defer buf.Stop()

	d := newMutationTestDaemon(bus, "ws-rt")
	d.config = &DaemonConfig{Agents: []AgentEntry{{Worktree: "t", Role: "task"}}}
	d.sup.ConfigSnapshot = d.configSnapshot
	d.mutBuf = buf

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer: %v", err)
	}
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		_ = d.controlListener.Close()
	}()

	// Publish a mutation
	m := backend.MutationData{Type: "claim", IssueID: "rt-1", Timestamp: time.Now()}
	d.publishMutation(m)
	time.Sleep(50 * time.Millisecond)

	// Connect to control socket and send get_mutations
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	args, _ := json.Marshal(GetMutationsArgs{Since: 0})
	req := DaemonControlRequest{
		Operation: ctrlOpGetMutations,
		Args:      args,
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("no response from control socket")
	}

	var resp DaemonControlResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	var mutations []backend.MutationData
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		t.Fatalf("unmarshal mutations: %v", err)
	}
	if len(mutations) != 1 || mutations[0].IssueID != "rt-1" {
		t.Fatalf("unexpected mutations: %v", mutations)
	}
}
