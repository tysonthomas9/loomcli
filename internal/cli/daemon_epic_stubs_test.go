package cli

import "testing"

// stubEpicHasReadyTasks replaces epicHasReadyTasks for the duration of the test.
func stubEpicHasReadyTasks(t *testing.T, fn func(string) (bool, error)) {
	t.Helper()
	orig := epicHasReadyTasks
	epicHasReadyTasks = fn
	t.Cleanup(func() { epicHasReadyTasks = orig })
}
