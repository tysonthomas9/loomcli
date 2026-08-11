package agent

import "testing"

func TestDaemonCheckoutCleanIgnoresOnlyUntrackedLoomRuntimeFiles(t *testing.T) {
	t.Parallel()

	status := "?? .agent.lock\n" +
		"?? .agent.lock.flock\n" +
		"?? .agent.checkpoint.json\n" +
		"?? .agent.checkpoint.json.tmp\n"
	if !daemonCheckoutClean(status) {
		t.Fatalf("daemonCheckoutClean() = false for Loom-owned runtime files:\n%s", status)
	}
}

func TestDaemonCheckoutCleanRejectsUserAndTrackedChanges(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]string{
		"untracked user file":         "?? notes.txt\n",
		"tracked source change":       " M internal/worker.go\n",
		"tracked runtime file":        " M .agent.lock\n",
		"nested runtime-looking file": "?? nested/.agent.lock\n",
	} {
		t.Run(name, func(t *testing.T) {
			if daemonCheckoutClean(status) {
				t.Fatalf("daemonCheckoutClean(%q) = true, want false", status)
			}
		})
	}
}
