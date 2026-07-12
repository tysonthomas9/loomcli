package sandbox

// Live end-to-end: loom's own RunOpenshellExit driving the REAL NVIDIA OpenShell
// CLI against a REAL running supervisor sandbox. Gated on OSH_REAL_SANDBOX so it
// only runs when a gateway + sandbox are up (set by the proof harness). Proves
// loom reads the true in-sandbox exit code with no fake in the path.

import (
	"os"
	"testing"
)

func TestRealOpenshellExit_EndToEnd(t *testing.T) {
	name := os.Getenv("OSH_REAL_SANDBOX")
	if name == "" {
		t.Skip("set OSH_REAL_SANDBOX (+ real openshell on PATH, OPENSHELL_GATEWAY_ENDPOINT) to run the live test")
	}
	for _, code := range []int{0, 1, 17, 42, 100, 255} {
		got, err := RunOpenshellExit([]string{
			"sandbox", "exec", "-n", name, "--", "sh", "-c", "exit " + itoaReal(code),
		})
		if err != nil {
			t.Errorf("code %d: RunOpenshellExit error %v", code, err)
			continue
		}
		if got != code {
			t.Errorf("loom RunOpenshellExit got %d, want %d", got, code)
		} else {
			t.Logf("VERIFIED (real openshell): in-sandbox exit %d -> loom RunOpenshellExit returned %d", code, got)
		}
	}
}

func itoaReal(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
