package sandbox

// Live end-to-end: loom's own RunOpenshellExit driving the REAL NVIDIA OpenShell
// CLI against a REAL running supervisor sandbox. Gated on OSH_REAL_SANDBOX so it
// only runs when a gateway + sandbox are up (set by the proof harness). Proves
// loom reads the true in-sandbox exit code with no fake in the path.

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRealOpenshellExit_EndToEnd(t *testing.T) {
	name := os.Getenv("OSH_REAL_SANDBOX")
	if name == "" {
		// The live matrix target has an operator-managed gateway but no pre-created
		// sandbox. Create one with the real CLI so this existing regression remains
		// part of the per-version target instead of being duplicated in agent tests.
		if os.Getenv("OPENSHELL_GATEWAY_ENDPOINT") == "" || os.Getenv("FLEET_DB_BIN") == "" {
			t.Skip("set OSH_REAL_SANDBOX, or all of OPENSHELL_GATEWAY_ENDPOINT + FLEET_DB_BIN + real openshell on PATH, to run the live test")
		}
		path, err := exec.LookPath("openshell")
		if err != nil {
			t.Skip("live exit-code regression requires real openshell on PATH")
		}
		version, err := exec.Command(path, "--version").CombinedOutput()
		if err != nil {
			t.Fatalf("openshell --version: %v: %s", err, version)
		}
		t.Logf("OpenShell exit-code contract under test: %s", strings.TrimSpace(string(version)))
		name = "loom-exit-regression-" + itoaReal(int(time.Now().Unix()))
		cfg := DefaultConfig()
		cfg.Providers = nil
		if err := RunOpenshell(BuildCreateArgs(name, cfg)); err != nil {
			t.Fatalf("create exit-code regression sandbox: %v", err)
		}
		t.Cleanup(func() { DeleteSandbox(name) })
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
