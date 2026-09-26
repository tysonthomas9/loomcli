package sandbox

// Exit-code propagation proof, loom's half: RunOpenshellExit must return the
// EXACT exit status of the `openshell` process for arbitrary codes, with a nil
// error on a clean non-zero exit (so callers treat it as "agent failed with
// code N", not "orchestration error"). Uses a real /bin/sh process on PATH as
// the stand-in openshell — this is a genuine os/exec round-trip, not a mock.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunOpenshellExit_ForwardsProcessExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake openshell is a /bin/sh script")
	}
	// A fake `openshell` that exits with whatever $FAKE_EXIT says. This is the
	// exact process boundary RunOpenshellExit crosses in production.
	binDir := t.TempDir()
	script := "#!/bin/sh\nexit ${FAKE_EXIT:-0}\n"
	if err := os.WriteFile(filepath.Join(binDir, "openshell"), []byte(script), 0o755); err != nil { //nolint:gosec // test helper
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, code := range []int{0, 1, 17, 42, 255} {
		t.Setenv("FAKE_EXIT", itoa(code))
		got, err := RunOpenshellExit([]string{"sandbox", "exec", "-n", "s", "--", "sh", "/x"})
		if err != nil {
			t.Errorf("code %d: unexpected error %v (a clean non-zero exit must yield nil err)", code, err)
			continue
		}
		if got != code {
			t.Errorf("RunOpenshellExit forwarded %d, want %d", got, code)
		} else {
			t.Logf("VERIFIED: openshell process exit %d -> RunOpenshellExit returned %d (err=nil)", code, got)
		}
	}
}

// itoa avoids importing strconv just for the test loop.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
