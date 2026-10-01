package clock

import (
	"testing"
	"time"
)

var epoch = time.Date(2026, 9, 28, 4, 17, 33, 0, time.UTC)

func TestFakeFiresTimersInDeadlineOrder(t *testing.T) {
	f := NewFake(epoch)
	late := f.NewTimer(3 * time.Second)
	early := f.NewTimer(1 * time.Second)
	tick := f.NewTicker(2 * time.Second)

	f.Advance(1 * time.Second)
	if got := <-early.C(); !got.Equal(epoch.Add(time.Second)) {
		t.Fatalf("early fired at %v", got)
	}
	select {
	case <-late.C():
		t.Fatal("late timer fired before its deadline")
	default:
	}

	f.Advance(2 * time.Second)
	if got := <-tick.C(); !got.Equal(epoch.Add(2 * time.Second)) {
		t.Fatalf("ticker fired at %v, want +2s", got)
	}
	if got := <-late.C(); !got.Equal(epoch.Add(3 * time.Second)) {
		t.Fatalf("late fired at %v", got)
	}
	if got := f.Now(); !got.Equal(epoch.Add(3 * time.Second)) {
		t.Fatalf("Now = %v", got)
	}
	if f.Waiters() != 1 {
		t.Fatalf("Waiters = %d, want only the ticker", f.Waiters())
	}
}

func TestFakeStopResetAndBlockUntil(t *testing.T) {
	f := NewFake(epoch)
	tm := f.NewTimer(time.Second)
	if !tm.Stop() {
		t.Fatal("Stop on armed timer returned false")
	}
	f.Advance(time.Hour)
	select {
	case <-tm.C():
		t.Fatal("stopped timer fired")
	default:
	}
	if tm.Reset(time.Second) {
		t.Fatal("Reset on stopped timer reported armed")
	}

	parked := make(chan time.Time)
	go func() {
		parked <- <-f.After(5 * time.Second)
	}()
	f.BlockUntilWaiters(2)
	f.Advance(5 * time.Second)
	if got := <-parked; !got.Equal(epoch.Add(time.Hour + 5*time.Second)) {
		t.Fatalf("After delivered %v", got)
	}
	if got := <-tm.C(); !got.Equal(epoch.Add(time.Hour + time.Second)) {
		t.Fatalf("reset timer delivered %v", got)
	}
}

func TestOrDefaultsToReal(t *testing.T) {
	if Or(nil) != Real {
		t.Fatal("Or(nil) is not Real")
	}
	f := NewFake(epoch)
	if Or(f) != Clock(f) {
		t.Fatal("Or(fake) did not return the fake")
	}
}
