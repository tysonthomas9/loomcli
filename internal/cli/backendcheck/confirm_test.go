package backendcheck

import (
	"errors"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"
)

// stubConfirm swaps CheckBackend and records every sleep ConfirmBackend asks
// for, so a test can assert the retry budget without spending it.
func stubConfirm(t *testing.T, fn func(string) (discovery.Info, error)) (calls *int, slept *[]time.Duration) {
	t.Helper()
	n := 0
	var waits []time.Duration

	prev := CheckBackend
	CheckBackend = func(name string) (discovery.Info, error) {
		n++
		return fn(name)
	}
	t.Cleanup(func() { CheckBackend = prev })

	prevSleep := ConfirmSleep
	ConfirmSleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { ConfirmSleep = prevSleep })

	return &n, &waits
}

func TestConfirmBackend_InstalledFirstProbe_NoRetry(t *testing.T) {
	calls, slept := stubConfirm(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Installed: true}, nil
	})

	info, misses, err := ConfirmBackend("claude")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Installed {
		t.Error("expected Installed")
	}
	if misses != 0 {
		t.Errorf("misses = %d, want 0", misses)
	}
	if *calls != 1 {
		t.Errorf("CheckBackend called %d times, want 1 — the hot path must not retry", *calls)
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v, want no sleeps on the installed path", *slept)
	}
}

func TestConfirmBackend_MissThenHit(t *testing.T) {
	// Miss on attempts 1-2, hit on 3 — the symlink-swap window.
	seen := 0
	calls, _ := stubConfirm(t, func(name string) (discovery.Info, error) {
		seen++
		return discovery.Info{Name: name, Installed: seen >= 3}, nil
	})

	info, misses, err := ConfirmBackend("claude")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected the third probe to confirm Installed")
	}
	if misses != 2 {
		t.Errorf("misses = %d, want 2", misses)
	}
	if *calls != 3 {
		t.Errorf("CheckBackend called %d times, want 3 — the retry must not be dropped", *calls)
	}
}

func TestConfirmBackend_AllMisses(t *testing.T) {
	const hint = `"claude" not on PATH`
	calls, slept := stubConfirm(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Installed: false, InstallHint: hint}, nil
	})

	info, misses, err := ConfirmBackend("claude")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Installed {
		t.Error("expected !Installed after every attempt missed")
	}
	if info.InstallHint != hint {
		t.Errorf("InstallHint = %q, want %q — the hint must survive the retries", info.InstallHint, hint)
	}
	if misses != confirmAttempts {
		t.Errorf("misses = %d, want %d", misses, confirmAttempts)
	}
	if *calls != confirmAttempts {
		t.Errorf("CheckBackend called %d times, want %d", *calls, confirmAttempts)
	}
	if len(*slept) != confirmAttempts-1 {
		t.Errorf("slept %d times, want %d", len(*slept), confirmAttempts-1)
	}
}

func TestConfirmBackend_LookupError_NotRetried(t *testing.T) {
	wantErr := errors.New("versions.json unreadable")
	calls, slept := stubConfirm(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name}, wantErr
	})

	_, misses, err := ConfirmBackend("claude")

	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if misses != 0 {
		t.Errorf("misses = %d, want 0 — a discovery failure is not a PATH miss", misses)
	}
	if *calls != 1 {
		t.Errorf("CheckBackend called %d times, want 1 — an error must not be retried", *calls)
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v, want no sleeps on the error path", *slept)
	}
}

func TestConfirmBackend_SleepsBetweenAttempts(t *testing.T) {
	_, slept := stubConfirm(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Installed: false}, nil
	})

	ConfirmBackend("claude")

	if len(*slept) != len(confirmDelays) {
		t.Fatalf("slept %d times, want %d", len(*slept), len(confirmDelays))
	}
	var total time.Duration
	for i, d := range *slept {
		if d != confirmDelays[i] {
			t.Errorf("sleep[%d] = %v, want %v", i, d, confirmDelays[i])
		}
		total += d
	}
	// Keeps the worst-case bound honest: it must ride out a ~1-2s symlink swap
	// while staying far under the 30s recheck it replaces.
	if total < 1500*time.Millisecond || total > 5*time.Second {
		t.Errorf("total retry budget %v is outside the intended 1.5s-5s band", total)
	}
}
