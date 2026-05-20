package cli

import "testing"

func TestExitWithFlushUsesInjectedExit(t *testing.T) {
	var got int
	TestingSetExitProcess(t, func(code int) { got = code })
	ExitWithFlush(7)
	if got != 7 {
		t.Fatalf("exit code = %d, want 7", got)
	}
}
