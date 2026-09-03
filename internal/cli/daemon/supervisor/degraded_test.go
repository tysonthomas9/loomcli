package supervisor

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemonregistry"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// The transition returns are the contract the callers depend on: they log and
// publish only when the return is true, so a "true" on every failure would
// restore exactly the per-tick noise this design removed.
func TestRecordDegradation_OnlyFirstFailureTransitions(t *testing.T) {
	s := &Supervisor{}

	if !s.RecordDegradation(DegradationStateWrite, errors.New("no space left on device")) {
		t.Fatal("first RecordDegradation should report the 0->1 transition")
	}

	d, ok := s.Degradation(DegradationStateWrite)
	if !ok {
		t.Fatal("degradation should be active after Record")
	}
	since := d.Since
	if d.Count != 1 {
		t.Errorf("Count = %d, want 1", d.Count)
	}
	if d.LastErr != "no space left on device" {
		t.Errorf("LastErr = %q", d.LastErr)
	}

	for i := 2; i <= 4; i++ {
		if s.RecordDegradation(DegradationStateWrite, errors.New("still full")) {
			t.Fatalf("RecordDegradation #%d reported a transition", i)
		}
	}

	d, _ = s.Degradation(DegradationStateWrite)
	if d.Count != 4 {
		t.Errorf("Count = %d, want 4", d.Count)
	}
	if !d.Since.Equal(since) {
		t.Errorf("Since moved: %v -> %v; the episode start must be preserved", since, d.Since)
	}
	if d.LastErr != "still full" {
		t.Errorf("LastErr = %q, want the most recent error", d.LastErr)
	}
}

func TestClearDegradation_OnlyFirstClearTransitions(t *testing.T) {
	s := &Supervisor{}

	// Clearing a healthy kind is a no-op, so the success path can call it
	// unconditionally on every tick.
	if s.ClearDegradation(DegradationStateWrite) {
		t.Error("clearing a kind that was never degraded reported a transition")
	}

	s.RecordDegradation(DegradationStateWrite, errors.New("boom"))
	if !s.ClearDegradation(DegradationStateWrite) {
		t.Fatal("first ClearDegradation should report the 1->0 transition")
	}
	if s.ClearDegradation(DegradationStateWrite) {
		t.Error("second ClearDegradation reported a transition")
	}
	if _, ok := s.Degradation(DegradationStateWrite); ok {
		t.Error("degradation still active after Clear")
	}
}

// A recurrence after recovery is a NEW episode: Since restarts and Count
// resets, so an operator reading it is not told the daemon has been broken
// since the first blip hours ago.
func TestRecordDegradation_RecurrenceStartsNewEpisode(t *testing.T) {
	s := &Supervisor{}
	s.RecordDegradation(DegradationStateWrite, errors.New("boom"))
	s.RecordDegradation(DegradationStateWrite, errors.New("boom"))
	first, _ := s.Degradation(DegradationStateWrite)
	s.ClearDegradation(DegradationStateWrite)

	time.Sleep(2 * time.Millisecond)
	if !s.RecordDegradation(DegradationStateWrite, errors.New("boom again")) {
		t.Fatal("recurrence after recovery should report a transition")
	}
	second, _ := s.Degradation(DegradationStateWrite)
	if second.Count != 1 {
		t.Errorf("Count = %d, want 1 for a fresh episode", second.Count)
	}
	if !second.Since.After(first.Since) {
		t.Errorf("Since not restarted: %v vs %v", second.Since, first.Since)
	}
}

func TestDegradations_SortedAndCopied(t *testing.T) {
	s := &Supervisor{}
	// Record in reverse-sorted order so an unsorted implementation is caught
	// even when Go's map iteration happens to return insertion order.
	s.RecordDegradation(DegradationStateWrite, errors.New("a"))
	s.RecordDegradation(DegradationLogWrite, errors.New("b"))

	got := s.Degradations()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Kind != DegradationLogWrite || got[1].Kind != DegradationStateWrite {
		t.Errorf("not sorted by kind: %v, %v", got[0].Kind, got[1].Kind)
	}

	// The elements must be copies; a caller that renders them must not be able
	// to corrupt the supervisor's own record.
	got[0].Count = 9999
	got[0].LastErr = "clobbered"
	fresh, _ := s.Degradation(DegradationLogWrite)
	if fresh.Count != 1 || fresh.LastErr != "b" {
		t.Errorf("mutating the returned slice reached the supervisor: %+v", fresh)
	}
}

func TestDegradations_EmptyWhenHealthy(t *testing.T) {
	s := &Supervisor{}
	if got := s.Degradations(); len(got) != 0 {
		t.Errorf("Degradations() = %v, want empty", got)
	}
	if got := s.DegradedLabels(); got != nil {
		t.Errorf("DegradedLabels() = %v, want nil so NodeUpdate drops the label", got)
	}
}

func TestDegradedLabels_Shape(t *testing.T) {
	s := &Supervisor{}
	s.RecordDegradation(DegradationStateWrite, errors.New("x"))
	s.RecordDegradation(DegradationLogWrite, errors.New("y"))

	got := s.DegradedLabels()
	want := []string{
		daemonregistry.LabelDegraded + "log_write",
		daemonregistry.LabelDegraded + "state_write",
	}
	if len(got) != len(want) {
		t.Fatalf("DegradedLabels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// And the labels must actually reach the Node label set.
	labels := s.daemonRuntimeLabels()
	var degraded int
	for _, l := range labels {
		if strings.HasPrefix(l, daemonregistry.LabelDegraded) {
			degraded++
		}
	}
	if degraded != 2 {
		t.Errorf("daemonRuntimeLabels() carried %d degraded labels, want 2: %v", degraded, labels)
	}

	s.ClearDegradation(DegradationLogWrite)
	s.ClearDegradation(DegradationStateWrite)
	for _, l := range s.daemonRuntimeLabels() {
		if strings.HasPrefix(l, daemonregistry.LabelDegraded) {
			t.Errorf("recovered daemon still publishes %q", l)
		}
	}
}

func TestDegradationsNeedingReannounce(t *testing.T) {
	s := &Supervisor{}
	s.RecordDegradation(DegradationStateWrite, errors.New("x"))

	// Just recorded, so it was just announced: nothing is due yet.
	if due := s.degradationsNeedingReannounce(time.Hour); len(due) != 0 {
		t.Errorf("freshly recorded degradation was due for re-announce: %v", due)
	}

	// A zero interval makes everything active due, and the re-stamp means a
	// second call under a long interval finds nothing.
	if due := s.degradationsNeedingReannounce(0); len(due) != 1 || due[0].Kind != DegradationStateWrite {
		t.Fatalf("due = %v, want the state_write episode", due)
	}
	if due := s.degradationsNeedingReannounce(time.Hour); len(due) != 0 {
		t.Errorf("re-announced twice inside one interval: %v", due)
	}

	s.ClearDegradation(DegradationStateWrite)
	if due := s.degradationsNeedingReannounce(0); len(due) != 0 {
		t.Errorf("recovered degradation still re-announcing: %v", due)
	}
}

// The state updater writes these while the health checker and control plane
// read them, on different goroutines and behind a lock that is deliberately not
// AgentsMu. Run under -race.
func TestDegradation_ConcurrentAccess(t *testing.T) {
	s := &Supervisor{}
	kinds := []DegradationKind{DegradationStateWrite, DegradationLogWrite}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		kind := kinds[i%len(kinds)]
		wg.Add(4)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.RecordDegradation(kind, errors.New("e"))
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.ClearDegradation(kind)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = s.Degradations()
				_ = s.DegradedLabels()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = s.Degradation(kind)
				_ = s.degradationsNeedingReannounce(0)
			}
		}()
	}
	wg.Wait()
}

// PublishDegradation goes out on the events bus and the Node labels — the two
// handles that do not share a failure mode with a broken state file.
func TestPublishDegradation_EmitsEventAndLabels(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)

	var mu sync.Mutex
	var got []events.Event
	s.EmitEvent = func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	s.RecordDegradation(DegradationStateWrite, errors.New("no space left on device"))
	s.PublishDegradation(DegradationStateWrite)

	mu.Lock()
	evts := append([]events.Event(nil), got...)
	mu.Unlock()

	var degraded *events.Event
	for i := range evts {
		if evts[i].Type == events.DaemonDegraded {
			degraded = &evts[i]
		}
	}
	if degraded == nil {
		t.Fatalf("no %s event emitted; got %v", events.DaemonDegraded, evts)
	}
	decoded, err := degraded.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	d := decoded.(*events.DaemonDegradedData)
	if d.Kind != string(DegradationStateWrite) || !d.Active || d.Count != 1 {
		t.Errorf("unexpected payload: %+v", d)
	}
	if d.LastErr != "no space left on device" {
		t.Errorf("LastErr = %q", d.LastErr)
	}

	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	want := daemonregistry.LabelDegraded + "state_write"
	if !containsString(node.Labels, want) {
		t.Errorf("node labels = %v, want to contain %q", node.Labels, want)
	}

	// Recovery: the event flips to inactive and the label is dropped, because
	// NodeUpdate replaces the label set wholesale.
	s.ClearDegradation(DegradationStateWrite)
	s.PublishDegradation(DegradationStateWrite)

	mu.Lock()
	last := got[len(got)-1]
	mu.Unlock()
	decoded, err = last.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	if d := decoded.(*events.DaemonDegradedData); d.Active || d.Kind != string(DegradationStateWrite) {
		t.Errorf("recovery payload = %+v, want Active false", d)
	}

	node, err = st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if containsString(node.Labels, want) {
		t.Errorf("node labels still carry %q after recovery: %v", want, node.Labels)
	}
}

// A daemon running without a control plane (and a supervisor built bare, as in
// most tests) must not panic or block when the state updater reports.
func TestPublishDegradation_NoControlPlaneIsNoOp(t *testing.T) {
	s := &Supervisor{}
	s.RecordDegradation(DegradationStateWrite, errors.New("boom"))
	s.PublishDegradation(DegradationStateWrite)
	s.reannounceDegradations()
}
