package leaddispatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestWait_PollsToTerminalAndPrintsTransitions(t *testing.T) {
	statuses := []RunStatus{
		{Status: "queued"}, {Status: "queued"}, {Status: "running"},
		{Status: "completed", Terminal: true},
	}
	var out bytes.Buffer
	got, err := Wait(context.Background(), "run-1", func(context.Context) (RunStatus, error) {
		status := statuses[0]
		statuses = statuses[1:]
		return status, nil
	}, WaitOptions{Out: &out, QueuedWarnAfter: -1, sleep: noWait})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("final = %+v", got)
	}
	want := "[epic-run] run run-1 status: queued\n" +
		"[epic-run] run run-1 status: running\n" +
		"[epic-run] run run-1 status: completed\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestWait_ToleratesTransientErrorsThenAborts(t *testing.T) {
	t.Run("resets after success", func(t *testing.T) {
		calls := 0
		_, err := Wait(context.Background(), "run-1", func(context.Context) (RunStatus, error) {
			calls++
			switch calls {
			case 1, 2:
				return RunStatus{}, fmt.Errorf("connection reset")
			case 4, 5:
				return RunStatus{}, &APIError{Status: 503, Message: "busy"}
			case 3:
				return RunStatus{Status: "running"}, nil
			default:
				return RunStatus{Status: "completed", Terminal: true}, nil
			}
		}, WaitOptions{MaxTransientErrs: 2, QueuedWarnAfter: -1, sleep: noWait})
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	})

	t.Run("N plus one aborts", func(t *testing.T) {
		calls := 0
		_, err := Wait(context.Background(), "run-1", func(context.Context) (RunStatus, error) {
			calls++
			return RunStatus{}, &APIError{Status: 503, Message: "busy"}
		}, WaitOptions{MaxTransientErrs: 2, QueuedWarnAfter: -1, sleep: noWait})
		if err == nil || calls != 3 {
			t.Fatalf("error = %v, calls = %d, want abort on third", err, calls)
		}
	})

	t.Run("non retryable aborts immediately", func(t *testing.T) {
		calls := 0
		_, err := Wait(context.Background(), "run-1", func(context.Context) (RunStatus, error) {
			calls++
			return RunStatus{}, &APIError{Status: 403, Message: "denied"}
		}, WaitOptions{MaxTransientErrs: 2, QueuedWarnAfter: -1, sleep: noWait})
		if err == nil || calls != 1 {
			t.Fatalf("error = %v, calls = %d, want immediate abort", err, calls)
		}
	})
}

func TestWait_ContextCancelReportsRunContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Wait(ctx, "run-1", func(context.Context) (RunStatus, error) {
		t.Fatal("status called after cancellation")
		return RunStatus{}, nil
	}, WaitOptions{})
	if err == nil || !strings.Contains(err.Error(), "run continues on the server") || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestWait_NeverTreatsSuspendedAsTerminal(t *testing.T) {
	calls := 0
	got, err := Wait(context.Background(), "run-1", func(context.Context) (RunStatus, error) {
		calls++
		if calls == 1 {
			return RunStatus{Status: "suspended_awaiting_event", Terminal: false}, nil
		}
		return RunStatus{Status: "completed", Terminal: true}, nil
	}, WaitOptions{QueuedWarnAfter: -1, sleep: noWait})
	if err != nil || calls != 2 || got.Status != "completed" {
		t.Fatalf("final = %+v, calls = %d, err = %v", got, calls, err)
	}
}

func noWait(context.Context, time.Duration) error { return nil }
