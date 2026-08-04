package supervisor

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newHarnessSupervisor returns a Supervisor configured for harness tests.
func newHarnessSupervisor() *Supervisor {
	return &Supervisor{
		Shutdown: make(chan struct{}),
		FatalCh:  make(chan error, 1),
	}
}

func TestRunCriticalPanicSignalsFatal(t *testing.T) {
	s := newHarnessSupervisor()
	s.RunCritical("test_panic", func() {
		panic("synthetic panic")
	})

	select {
	case err := <-s.FatalChannel():
		if err == nil {
			t.Fatal("FatalChannel delivered nil error")
		}
		if !strings.Contains(err.Error(), "test_panic") {
			t.Errorf("error missing goroutine name: %v", err)
		}
		if !strings.Contains(err.Error(), "synthetic panic") {
			t.Errorf("error missing panic value: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FatalChannel did not receive within 2s")
	}

	done := make(chan struct{})
	go func() {
		s.Wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wg did not drain after panic")
	}
}

func TestRunCriticalReturnWithoutShutdownSignalsFatal(t *testing.T) {
	s := newHarnessSupervisor()
	s.RunCritical("test_early_return", func() {
		// returns immediately without observing Shutdown
	})

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), "test_early_return") {
			t.Errorf("error missing goroutine name: %v", err)
		}
		if !strings.Contains(err.Error(), "returned without shutdown") {
			t.Errorf("error missing reason: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FatalChannel did not receive within 2s after early return")
	}
}

func TestRunCriticalNormalShutdownDoesNotSignalFatal(t *testing.T) {
	s := newHarnessSupervisor()
	s.RunCritical("test_clean_shutdown", func() {
		<-s.Shutdown
	})
	close(s.Shutdown)

	select {
	case <-s.FatalChannel():
		t.Fatal("FatalChannel fired during clean shutdown")
	case <-time.After(200 * time.Millisecond):
	}

	done := make(chan struct{})
	go func() {
		s.Wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Wg did not drain after Shutdown")
	}
}

func TestSignalFatalFiresOnlyOnce(t *testing.T) {
	s := newHarnessSupervisor()
	s.SignalFatal("first", errors.New("first error"))
	s.SignalFatal("second", errors.New("second error"))

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), "first") {
			t.Errorf("expected first error to win, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("FatalChannel did not receive first error")
	}

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("second error leaked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSignalFatalConcurrentRacers(t *testing.T) {
	s := newHarnessSupervisor()

	var wg sync.WaitGroup
	const racers = 32
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.SignalFatal("racer", errors.New("racer error"))
		}()
	}
	wg.Wait()

	count := 0
	for {
		select {
		case <-s.FatalChannel():
			count++
		default:
			if count != 1 {
				t.Fatalf("expected exactly 1 fatal, got %d", count)
			}
			return
		}
	}
}

func TestRegisterAndRecordTick(t *testing.T) {
	s := newHarnessSupervisor()

	s.RegisterTick("loop_a")
	t0, ok := s.LoadTick("loop_a")
	if !ok {
		t.Fatal("LoadTick returned !ok after RegisterTick")
	}
	if t0.IsZero() {
		t.Fatal("RegisterTick left tick zero")
	}

	time.Sleep(5 * time.Millisecond)
	s.RecordTick("loop_a")
	t1, _ := s.LoadTick("loop_a")
	if !t1.After(t0) {
		t.Errorf("RecordTick did not advance tick: t0=%v t1=%v", t0, t1)
	}

	if _, ok := s.LoadTick("never_registered"); ok {
		t.Error("LoadTick returned ok for never-registered slot")
	}
	s.RecordTick("never_registered") // must not panic
}

func TestRangeTicksSeesAllRegistered(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick("a")
	s.RegisterTick("b")
	s.RegisterTick("c")

	var count int32
	seen := map[string]bool{}
	var mu sync.Mutex
	s.RangeTicks(func(name string, _ time.Time) {
		atomic.AddInt32(&count, 1)
		mu.Lock()
		seen[name] = true
		mu.Unlock()
	})

	if count != 3 {
		t.Errorf("RangeTicks visited %d slots, want 3", count)
	}
	for _, name := range []string{"a", "b", "c"} {
		if !seen[name] {
			t.Errorf("RangeTicks did not visit %q", name)
		}
	}
}

func TestRecoverAndSignalCatchesPanic(t *testing.T) {
	s := newHarnessSupervisor()

	func() {
		defer s.RecoverAndSignal("inline_test")
		panic("inline panic")
	}()

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), "inline_test") {
			t.Errorf("error missing goroutine name: %v", err)
		}
		if !strings.Contains(err.Error(), "inline panic") {
			t.Errorf("error missing panic value: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("FatalChannel did not receive after RecoverAndSignal")
	}
}

func TestRecoverAndSignalIsNoOpWithoutPanic(t *testing.T) {
	s := newHarnessSupervisor()

	func() {
		defer s.RecoverAndSignal("no_panic")
	}()

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("FatalChannel fired without panic: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}
