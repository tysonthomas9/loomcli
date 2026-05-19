package ops

import "testing"

func TestGitResetLockedError(t *testing.T) {
	err := &GitResetLockedError{AgentName: "nova", PID: 123, Duration: "1m", TaskID: "TASK-1"}
	if err.Error() != "agent locked" {
		t.Fatalf("Error() = %q", err.Error())
	}
}
