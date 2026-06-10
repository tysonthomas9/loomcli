package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
	"github.com/tysonthomas9/loomcli/internal/workflows/execplane/fake"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

// startReconciler runs r.Run in a goroutine and returns a stop func.
func startReconciler(t *testing.T, r *EpicReconciler) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("reconciler exited with error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("reconciler did not stop")
		}
	})
	return cancel
}

func testConfig(m *platform.MemStore, plane execplane.ExecutionPlane) EpicReconcilerConfig {
	return EpicReconcilerConfig{
		Workspace:             ws,
		NodeID:                "node-test",
		Store:                 m,
		Plane:                 plane,
		PollTimeout:           50 * time.Millisecond,
		RetryDelay:            50 * time.Millisecond,
		StaleRecoveryInterval: time.Hour,
	}
}

// waitForRun polls until a run for the epic reaches the wanted status.
func waitForRun(t *testing.T, m *platform.MemStore, epicID string, status platform.DriverRunStatus) *platform.DriverRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := m.DriverRuns().List(context.Background(), ws, platform.DriverRunFilter{EpicID: epicID, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 0 {
			return runs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no run for epic %s reached status %s", epicID, status)
	return nil
}

func countRuns(t *testing.T, m *platform.MemStore, epicID string) int {
	t.Helper()
	runs, err := m.DriverRuns().List(context.Background(), ws, platform.DriverRunFilter{EpicID: epicID})
	if err != nil {
		t.Fatal(err)
	}
	return len(runs)
}

func TestEpicReconciler_ExecutesAdmittedRun(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	plane := fake.New(fake.IdleScript(
		execplane.Event{Type: "text_delta", Data: json.RawMessage(`{"type":"text_delta","text":"advanced"}`)},
	))
	r, err := NewEpicReconciler(testConfig(m, plane))
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)

	// Wait for the dev version stamp, then admit a run externally (the
	// CLI path: source_kind != reconciler).
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := m.Drivers().Get(context.Background(), ws, "epic-runner"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("driver never stamped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	d, err := m.Drivers().Get(context.Background(), ws, "epic-runner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-cli-1", DriverID: "epic-runner", DriverVersionID: d.ActiveVersionID,
		EpicID: "EPIC-1", SourceKind: "cli",
	}); err != nil {
		t.Fatal(err)
	}

	run := waitForRun(t, m, "EPIC-1", platform.DriverRunCompleted)
	if run.RunID != "run-cli-1" {
		t.Fatalf("completed run: %+v", run)
	}
	invs := plane.Invocations()
	if len(invs) != 1 || invs[0].Agent != "epic-runner" || invs[0].InstanceID != "EPIC-1" {
		t.Fatalf("invocations: %+v", invs)
	}
	var msg map[string]string
	if err := json.Unmarshal([]byte(invs[0].Request.Message), &msg); err != nil {
		t.Fatal(err)
	}
	if msg["epic_id"] != "EPIC-1" || msg["run_id"] != "run-cli-1" || msg["workspace"] != ws {
		t.Fatalf("wake message: %v", msg)
	}
	if run.Output["tool_calls"] == "" || run.Output["events"] == "" {
		t.Fatalf("run output not captured: %+v", run.Output)
	}
}

func TestEpicReconciler_IssueEventWakesDrivenEpic(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	plane := fake.New(fake.IdleScript())
	cfg := testConfig(m, plane)
	cfg.ResolveEpic = func(_ context.Context, issueID string) (string, bool) {
		if issueID == "T1" {
			return "EPIC-2", true
		}
		return "", false
	}
	r, err := NewEpicReconciler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)

	// A prior completed run marks the epic as runner-driven.
	deadline := time.Now().Add(5 * time.Second)
	var d *platform.Driver
	for {
		var derr error
		d, derr = m.Drivers().Get(context.Background(), ws, "epic-runner")
		if derr == nil && d.ActiveVersionID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("driver never stamped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-prior", DriverID: "epic-runner", DriverVersionID: d.ActiveVersionID,
		EpicID: "EPIC-2", SourceKind: "cli",
	}); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, m, "EPIC-2", platform.DriverRunCompleted)

	// Task close event → new wake.
	m.AppendEvent(platform.MutationEvent{Action: "issue.close", EntityType: "issue", EntityID: "T1"})
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if countRuns(t, m, "EPIC-2") >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("issue event did not wake the epic")
}

func TestEpicReconciler_OrphanRecoveryOnStartup(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	// Seed: driver + version + a running run claimed by node-test (the
	// "previous process").
	ctx := context.Background()
	if _, err := m.Drivers().Create(ctx, ws, platform.Driver{DriverID: "epic-runner", Name: "epic-runner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Drivers().CreateVersion(ctx, ws, "epic-runner", platform.DriverVersion{
		VersionID: "ver-old", Version: 1, SourceDigest: "sha256:dev", BundleDigest: "sha256:dev",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DriverRuns().Create(ctx, ws, platform.DriverRunCreate{
		RunID: "run-orphan", DriverID: "epic-runner", DriverVersionID: "ver-old", EpicID: "EPIC-3", SourceKind: "cli",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DriverRuns().Claim(ctx, ws, "run-orphan", "node-test", "lease-dead"); err != nil {
		t.Fatal(err)
	}

	plane := fake.New(fake.IdleScript())
	r, err := NewEpicReconciler(testConfig(m, plane))
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)

	// The orphan is failed with a clear class…
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, err := m.DriverRuns().Get(ctx, ws, "run-orphan")
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == platform.DriverRunFailed {
			if run.ErrorClass != ErrorClassLoomRestart {
				t.Fatalf("error class: %s", run.ErrorClass)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan not recovered: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// …and the epic is re-woken to completion with a fresh run.
	waitForRun(t, m, "EPIC-3", platform.DriverRunCompleted)
}

// gatedPlane keeps invocation streams open until released, so tests
// can hold an epic's wake active.
type gatedPlane struct {
	mu       sync.Mutex
	release  chan struct{}
	invoked  chan string // receives instance IDs
	finished *fake.Plane
}

func newGatedPlane() *gatedPlane {
	return &gatedPlane{
		release: make(chan struct{}),
		invoked: make(chan string, 16),
	}
}

func (g *gatedPlane) Healthy(context.Context) error { return nil }

func (g *gatedPlane) Invoke(ctx context.Context, agent, instanceID string, _ execplane.InvokeRequest) (execplane.StreamHandle, error) {
	g.invoked <- instanceID
	s := &gatedStream{events: make(chan execplane.Event, 1), done: make(chan struct{})}
	go func() {
		defer close(s.events)
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-g.release:
			s.events <- execplane.Event{Type: execplane.EventIdle, Data: json.RawMessage(`{"type":"idle"}`)}
		}
	}()
	return s, nil
}

type gatedStream struct {
	events chan execplane.Event
	done   chan struct{}
	once   sync.Once
}

func (s *gatedStream) Events() <-chan execplane.Event { return s.events }
func (s *gatedStream) Cancel()                        { s.once.Do(func() { close(s.done) }) }
func (s *gatedStream) Err() error                     { return nil }

func TestEpicReconciler_MissedSignalReplaysAfterFinish(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	plane := newGatedPlane()
	cfg := testConfig(m, plane)
	cfg.ResolveEpic = func(_ context.Context, issueID string) (string, bool) { return "EPIC-4", true }
	r, err := NewEpicReconciler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)

	// Wait for the stamp, admit a run, and hold its wake open.
	deadline := time.Now().Add(5 * time.Second)
	var d *platform.Driver
	for {
		var derr error
		d, derr = m.Drivers().Get(context.Background(), ws, "epic-runner")
		if derr == nil && d.ActiveVersionID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("driver never stamped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-held", DriverID: "epic-runner", DriverVersionID: d.ActiveVersionID,
		EpicID: "EPIC-4", SourceKind: "cli",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-plane.invoked:
	case <-time.After(5 * time.Second):
		t.Fatal("run never invoked")
	}

	// Signal while the wake is active: admission would dedupe, so the
	// reconciler must remember it…
	m.AppendEvent(platform.MutationEvent{Action: "issue.close", EntityType: "issue", EntityID: "T9"})
	time.Sleep(200 * time.Millisecond) // let the event be absorbed
	close(plane.release)               // finish the held wake

	// …and replay it after the finish: a second run appears.
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if countRuns(t, m, "EPIC-4") >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missed signal not replayed; runs=%d", countRuns(t, m, "EPIC-4"))
}

func TestEpicReconciler_PlaneFailureRetries(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	var calls int
	var mu sync.Mutex
	plane := &flakyPlane{inner: fake.New(fake.IdleScript()), failFirst: 1, calls: &calls, mu: &mu}
	r, err := NewEpicReconciler(testConfig(m, plane))
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)

	deadline := time.Now().Add(5 * time.Second)
	var d *platform.Driver
	for {
		var derr error
		d, derr = m.Drivers().Get(context.Background(), ws, "epic-runner")
		if derr == nil && d.ActiveVersionID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("driver never stamped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-flaky", DriverID: "epic-runner", DriverVersionID: d.ActiveVersionID,
		EpicID: "EPIC-5", SourceKind: "cli",
	}); err != nil {
		t.Fatal(err)
	}

	// First run fails with a clear class, then the retry completes.
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ := m.DriverRuns().List(context.Background(), ws, platform.DriverRunFilter{EpicID: "EPIC-5"})
		var failed, completed bool
		for _, run := range runs {
			if run.Status == platform.DriverRunFailed && run.ErrorClass == ErrorClassPlaneUnavailable {
				failed = true
			}
			if run.Status == platform.DriverRunCompleted {
				completed = true
			}
		}
		if failed && completed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("plane failure was not retried to completion")
}

type flakyPlane struct {
	inner     *fake.Plane
	failFirst int
	calls     *int
	mu        *sync.Mutex
}

func (f *flakyPlane) Healthy(context.Context) error { return nil }

func (f *flakyPlane) Invoke(ctx context.Context, agent, instanceID string, req execplane.InvokeRequest) (execplane.StreamHandle, error) {
	f.mu.Lock()
	*f.calls++
	n := *f.calls
	f.mu.Unlock()
	if n <= f.failFirst {
		return nil, errors.New("connection refused")
	}
	return f.inner.Invoke(ctx, agent, instanceID, req)
}

func TestEpicReconciler_ConfigValidation(t *testing.T) {
	t.Parallel()
	if _, err := NewEpicReconciler(EpicReconcilerConfig{}); err == nil {
		t.Fatal("want config error")
	}
}
