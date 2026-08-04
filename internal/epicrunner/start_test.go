package epicrunner

import (
	"strings"
	"testing"
	"time"
)

func TestAcquireBindLockTimesOutWhenHeld(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	unlock, err := AcquireBindLockWithTimeout("ws", "nova", time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("initial AcquireBindLockWithTimeout() error = %v", err)
	}
	defer unlock()

	_, err = AcquireBindLockWithTimeout("ws", "nova", 10*time.Millisecond, time.Millisecond)
	if ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("second AcquireBindLockWithTimeout() error = %v, want conflict timeout", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout message", err)
	}
}
