package generic

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/harness/turns"
	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

func TestOnWrapperStatusMapping(t *testing.T) {
	a := New()
	cases := []struct {
		status wrapper.Status
		want   turns.Kind
	}{
		{wrapper.StatusWaitingForInput, turns.TurnComplete},
		{wrapper.StatusBlockedByCost, turns.Blocked},
		{wrapper.StatusRetryLater, turns.Blocked},
		{wrapper.StatusFailed, turns.Errored},
		{wrapper.StatusInterrupted, turns.Errored},
		{wrapper.StatusIdle, turns.Errored},
	}
	for _, tc := range cases {
		evs := a.OnWrapperStatus(tc.status, "reason")
		if len(evs) != 1 {
			t.Errorf("status=%s: want 1 event, got %d", tc.status, len(evs))
			continue
		}
		if evs[0].Kind != tc.want {
			t.Errorf("status=%s: want Kind=%s, got %s", tc.status, tc.want, evs[0].Kind)
		}
	}
}

func TestOnWrapperStatusIgnoresAdvisory(t *testing.T) {
	a := New()
	for _, s := range []wrapper.Status{wrapper.StatusStale, wrapper.StatusUnknown, wrapper.Status("")} {
		if evs := a.OnWrapperStatus(s, ""); len(evs) != 0 {
			t.Errorf("status=%s should emit no events, got %d", s, len(evs))
		}
	}
}
