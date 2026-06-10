package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
	"github.com/tysonthomas9/loomcli/internal/workflows/execplane/fake"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

const ws = "test-ws"

func seedRun(t *testing.T, m *platform.MemStore, runID, epicID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := m.Drivers().Create(ctx, ws, platform.Driver{DriverID: "epic-runner", Name: "epic-runner"}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatal(err)
	}
	if _, err := m.Drivers().CreateVersion(ctx, ws, "epic-runner", platform.DriverVersion{
		VersionID: "ver-1", Version: 1, SourceDigest: "sha256:dev", BundleDigest: "sha256:dev",
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatal(err)
	}
	if _, err := m.DriverRuns().Create(ctx, ws, platform.DriverRunCreate{
		RunID: runID, DriverID: "epic-runner", DriverVersionID: "ver-1", EpicID: epicID,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunLifecycle_ClaimHeartbeatFinish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := platform.NewMemStore()
	seedRun(t, m, "run-1", "")

	l, err := claimRunWithInterval(ctx, m, ws, "run-1", "node-a", slog.Default(), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if l.Run().Status != platform.DriverRunRunning || l.Run().FencingToken == 0 {
		t.Fatalf("claimed run: %+v", l.Run())
	}

	// Let a few heartbeats land, verify last_heartbeat advances.
	before := l.Run().LastHeartbeat
	time.Sleep(50 * time.Millisecond)
	got, err := m.DriverRuns().Get(ctx, ws, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastHeartbeat.After(before) {
		t.Fatal("heartbeat did not advance")
	}

	run, err := l.Finish(ctx, platform.DriverRunFinish{Status: platform.DriverRunCompleted, Summary: "ok"})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if run.Status != platform.DriverRunCompleted || run.Summary != "ok" {
		t.Fatalf("finished run: %+v", run)
	}
}

func TestRunLifecycle_ClaimConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := platform.NewMemStore()
	seedRun(t, m, "run-1", "")

	if _, err := ClaimRun(ctx, m, ws, "run-1", "node-a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimRun(ctx, m, ws, "run-1", "node-b", nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second claim: want conflict, got %v", err)
	}
}

func TestCaptureStream_AggregatesAndTerminates(t *testing.T) {
	t.Parallel()
	plane := fake.New(func(fake.Invocation) []execplane.Event {
		return []execplane.Event{
			{Type: "agent_start", Data: json.RawMessage(`{"type":"agent_start"}`)},
			{Type: "text_delta", Data: json.RawMessage(`{"type":"text_delta","text":"hello "}`)},
			{Type: "text_delta", Data: json.RawMessage(`{"type":"text_delta","text":"world"}`)},
			{Type: "tool_call", Data: json.RawMessage(`{"type":"tool_call","toolName":"advance_epic"}`)},
			{Type: "turn", Data: json.RawMessage(`{"type":"turn","usage":{"inputTokens":100,"outputTokens":42}}`)},
			{Type: "idle", Data: json.RawMessage(`{"type":"idle"}`)},
		}
	})
	h, err := plane.Invoke(context.Background(), "epic-runner", "E1", execplane.InvokeRequest{Message: "advance"})
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	res := CaptureStream(context.Background(), h, CaptureOptions{OnEvent: func(execplane.Event) { seen++ }})
	if !res.Terminal || res.StreamErr != nil || res.ErrorMessage != "" {
		t.Fatalf("result: %+v", res)
	}
	if res.Events != 6 || seen != 6 || res.ToolCalls != 1 || res.LastText != "hello world" {
		t.Fatalf("aggregates: %+v", res)
	}
	if res.Usage.InputTokens != 100 || res.Usage.OutputTokens != 42 {
		t.Fatalf("usage: %+v", res.Usage)
	}
}

func TestCaptureStream_ErrorFrame(t *testing.T) {
	t.Parallel()
	plane := fake.New(func(fake.Invocation) []execplane.Event {
		return []execplane.Event{
			{Type: "error", Data: json.RawMessage(`{"type":"error","error":{"type":"x","message":"boom"}}`)},
		}
	})
	h, _ := plane.Invoke(context.Background(), "epic-runner", "E1", execplane.InvokeRequest{})
	res := CaptureStream(context.Background(), h, CaptureOptions{})
	if !res.Terminal || res.ErrorMessage != "boom" {
		t.Fatalf("result: %+v", res)
	}
}

func TestCaptureStream_ContextCancel(t *testing.T) {
	t.Parallel()
	// A script that never sends a terminal frame: the stream stays
	// open until the context cancels.
	plane := fake.New(func(fake.Invocation) []execplane.Event {
		return []execplane.Event{
			{Type: "agent_start", Data: json.RawMessage(`{"type":"agent_start"}`)},
		}
	})
	h, _ := plane.Invoke(context.Background(), "epic-runner", "E1", execplane.InvokeRequest{})
	// Consume the only event, then cancel.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res := CaptureStream(ctx, h, CaptureOptions{})
	// The fake closes its channel after delivering all scripted events,
	// so this either ends cleanly (channel closed) or via ctx — both
	// must leave Terminal=false.
	if res.Terminal {
		t.Fatalf("result: %+v", res)
	}
}
