package automode

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// recordingBus captures every emitted event.
type recordingBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (b *recordingBus) Emit(e events.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	return nil
}

func (b *recordingBus) Close() error { return nil }

// A task.failed event names a class; without the provenance beside it, a
// spurious AuthFailure and a genuine one are indistinguishable in the daily
// events file, which is the surface the ticket measures.
func TestEmitTaskFailedEvent_CarriesEvidence(t *testing.T) {
	bus := &recordingBus{}
	ctx := &autoLoopCtx{opts: AutoModeOptions{AgentName: "falcon", EventBus: bus}}
	ae := &agenterr.AgentError{
		Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth),
		Evidence: agenterr.Evidence{
			Source: agenterr.EvidenceHarnessMarker,
			Rule:   "AuthRequiredMarker",
		},
	}

	emitTaskFailedEvent(ctx, ae, errors.New("boom"), "PUPPET-579")

	if len(bus.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(bus.events))
	}
	v, err := bus.events[0].DecodeData()
	if err != nil {
		t.Fatalf("DecodeData() error = %v", err)
	}
	data, ok := v.(*events.TaskFailedData)
	if !ok {
		t.Fatalf("DecodeData() = %T, want *events.TaskFailedData", v)
	}
	if data.ErrorClass == "" {
		t.Error("ErrorClass is empty")
	}
	if !strings.Contains(data.Evidence, "rule=AuthRequiredMarker") {
		t.Errorf("Evidence = %q, want it to name rule=AuthRequiredMarker", data.Evidence)
	}
}

// An error with no recorded provenance must not invent one: the field simply
// stays empty and omitempty drops it.
func TestEmitTaskFailedEvent_NoEvidenceStaysEmpty(t *testing.T) {
	bus := &recordingBus{}
	ctx := &autoLoopCtx{opts: AutoModeOptions{AgentName: "falcon", EventBus: bus}}
	ae := &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)}

	emitTaskFailedEvent(ctx, ae, errors.New("boom"), "PUPPET-579")

	v, err := bus.events[0].DecodeData()
	if err != nil {
		t.Fatalf("DecodeData() error = %v", err)
	}
	if data := v.(*events.TaskFailedData); data.Evidence != "" {
		t.Errorf("Evidence = %q, want empty", data.Evidence)
	}
}
