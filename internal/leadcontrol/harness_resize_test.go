package leadcontrol

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

type fakeResizeSignal string

func (s fakeResizeSignal) Signal()        {}
func (s fakeResizeSignal) String() string { return string(s) }

type fakeHarnessTerminalSize struct {
	mu         sync.Mutex
	cols, rows uint16
	available  bool
}

func (s *fakeHarnessTerminalSize) current() (uint16, uint16, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows, s.available
}

func (s *fakeHarnessTerminalSize) set(cols, rows uint16, available bool) {
	s.mu.Lock()
	s.cols = cols
	s.rows = rows
	s.available = available
	s.mu.Unlock()
}

func TestShouldForwardHarnessResizeClaudeAdapterOnly(t *testing.T) {
	tests := []struct {
		harnessName string
		want        bool
	}{
		{harnessName: HarnessNameClaudeCode, want: true},
		{harnessName: HarnessNameCodex, want: false},
		{harnessName: HarnessNameGemini, want: false},
		{harnessName: HarnessNameGeneric, want: false},
		{harnessName: "claude", want: false},
		{harnessName: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.harnessName, func(t *testing.T) {
			if got := shouldForwardHarnessResize(test.harnessName); got != test.want {
				t.Fatalf("shouldForwardHarnessResize(%q) = %v, want %v", test.harnessName, got, test.want)
			}
		})
	}
}

func TestForwardHarnessResizeEventsTracksWidthHeightAndCombinedChanges(t *testing.T) {
	size := &fakeHarnessTerminalSize{cols: 144, rows: 85, available: true}
	fake := newFakeHarnessConversation()
	signals, _, _ := startTestHarnessResizeForwarder(t, size.current, fake)

	waitForResizeCalls(t, fake, 1)
	steps := [][2]uint16{
		{86, 85},   // width only
		{86, 51},   // height only
		{211, 101}, // width and height
	}
	for index, next := range steps {
		size.set(next[0], next[1], true)
		signals <- fakeResizeSignal("winch")
		waitForResizeCalls(t, fake, index+2)
	}

	want := [][2]uint16{{144, 85}, {86, 85}, {86, 51}, {211, 101}}
	if got := fake.resizeCalls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Resize() calls = %v, want %v", got, want)
	}
}

func TestForwardHarnessResizeEventsCoalescesBurstAndSuppressesDuplicate(t *testing.T) {
	var samples atomic.Int32
	currentSize := func() (uint16, uint16, bool) {
		if samples.Add(1) == 1 {
			return 144, 85, true
		}
		return 211, 101, true
	}

	// Queue the full burst before starting so the forwarder deterministically
	// drains it and samples the terminal only once.
	signals := make(chan os.Signal, 4)
	for range 3 {
		signals <- fakeResizeSignal("winch")
	}
	fake := newFakeHarnessConversation()
	_, cancel, done := startTestHarnessResizeForwarderWithSignals(t, signals, currentSize, fake)
	waitForResizeCalls(t, fake, 2)
	waitForResizeSamples(t, &samples, 2)

	// A later signal samples the same size but must not call Resize again.
	signals <- fakeResizeSignal("winch")
	waitForResizeSamples(t, &samples, 3)
	cancel()
	waitForResizeForwarderDone(t, done)

	want := [][2]uint16{{144, 85}, {211, 101}}
	if got := fake.resizeCalls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Resize() calls = %v, want coalesced/deduplicated %v", got, want)
	}
	if got := samples.Load(); got != 3 {
		t.Fatalf("terminal size samples = %d, want 3 (initial, burst, duplicate)", got)
	}
}

func TestForwardHarnessResizeEventsRetriesUnavailableSizeWithoutAnotherSignal(t *testing.T) {
	var samples atomic.Int32
	currentSize := func() (uint16, uint16, bool) {
		if samples.Add(1) == 1 {
			return 0, 0, false
		}
		return 144, 85, true
	}

	fake := newFakeHarnessConversation()
	startTestHarnessResizeForwarder(t, currentSize, fake)
	waitForResizeCalls(t, fake, 1)
	if got := fake.resizeCalls()[0]; got != [2]uint16{144, 85} {
		t.Fatalf("recovered Resize() = %v, want [144 85]", got)
	}
	if got := samples.Load(); got != 2 {
		t.Fatalf("terminal size samples = %d, want initial read plus one retry", got)
	}
}

func TestForwardHarnessResizeEventsRetriesAfterTransientResizeError(t *testing.T) {
	fake := newFakeHarnessConversation()
	fake.enqueueResizeErrors(errors.New("temporary resize failure"))
	startTestHarnessResizeForwarder(
		t,
		func() (uint16, uint16, bool) { return 144, 85, true },
		fake,
	)

	waitForResizeCalls(t, fake, 2)

	want := [][2]uint16{{144, 85}, {144, 85}}
	if got := fake.resizeCalls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Resize() calls = %v, want failed attempt followed by retry %v", got, want)
	}
}

func TestForwardHarnessResizeEventsRetriesOnlyOnceWithoutAnotherSignal(t *testing.T) {
	fake := newFakeHarnessConversation()
	fake.enqueueResizeErrors(
		errors.New("first resize failure"),
		errors.New("retry resize failure"),
	)
	startTestHarnessResizeForwarder(
		t,
		func() (uint16, uint16, bool) { return 144, 85, true },
		fake,
	)

	waitForResizeCalls(t, fake, 2)
	assertResizeCallCountStays(t, fake, 2, 3*harnessResizeRetryDelay)
}

func TestForwardHarnessResizeEventsExitsOnTerminalResizeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "session terminated", err: wrapper.ErrSessionTerminated},
		{name: "conversation closed", err: chat.ErrClosed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeHarnessConversation()
			fake.enqueueResizeErrors(test.err)
			_, _, done := startTestHarnessResizeForwarder(
				t,
				func() (uint16, uint16, bool) { return 144, 85, true },
				fake,
			)

			waitForResizeForwarderDone(t, done)
			if got := fake.resizeCalls(); !reflect.DeepEqual(got, [][2]uint16{{144, 85}}) {
				t.Fatalf("Resize() calls = %v, want one terminal attempt", got)
			}
		})
	}
}

func TestForwardHarnessResizeEventsExitsWhenSignalChannelCloses(t *testing.T) {
	signals := make(chan os.Signal)
	close(signals)
	fake := newFakeHarnessConversation()
	_, _, done := startTestHarnessResizeForwarderWithSignals(
		t,
		signals,
		func() (uint16, uint16, bool) { return 144, 85, true },
		fake,
	)

	waitForResizeForwarderDone(t, done)
	if got := fake.resizeCalls(); !reflect.DeepEqual(got, [][2]uint16{{144, 85}}) {
		t.Fatalf("Resize() calls = %v, want initial sample before closed-channel exit", got)
	}
}

func startTestHarnessResizeForwarder(
	t *testing.T,
	currentSize func() (uint16, uint16, bool),
	fake *fakeHarnessConversation,
) (chan os.Signal, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	return startTestHarnessResizeForwarderWithSignals(t, make(chan os.Signal, 16), currentSize, fake)
}

func startTestHarnessResizeForwarderWithSignals(
	t *testing.T,
	signals chan os.Signal,
	currentSize func() (uint16, uint16, bool),
	fake *fakeHarnessConversation,
) (chan os.Signal, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardHarnessResizeEvents(ctx, signals, currentSize, fake, resizeTestLogger())
	}()
	t.Cleanup(func() {
		cancel()
		waitForResizeForwarderDone(t, done)
	})
	return signals, cancel, done
}

func resizeTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitForResizeCalls(t *testing.T, fake *fakeHarnessConversation, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(fake.resizeCalls()) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Resize() call count = %d, want at least %d", len(fake.resizeCalls()), want)
}

func waitForResizeSamples(t *testing.T, samples *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if samples.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("terminal size sample count = %d, want at least %d", samples.Load(), want)
}

func assertResizeCallCountStays(t *testing.T, fake *fakeHarnessConversation, want int, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			if got := len(fake.resizeCalls()); got != want {
				t.Fatalf("Resize() call count = %d, want %d", got, want)
			}
			return
		default:
			if got := len(fake.resizeCalls()); got != want {
				t.Fatalf("Resize() call count grew to %d, want it to stay %d", got, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func waitForResizeForwarderDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("resize forwarder did not stop")
	}
}
