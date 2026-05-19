package opsimpl

import "testing"

func TestBackendOpsListsRegisteredBackends(t *testing.T) {
	ops := NewBackendOps()
	health, err := ops.ListBackendsHealth()
	if err != nil {
		t.Fatalf("ListBackendsHealth: %v", err)
	}
	if len(health) == 0 {
		t.Fatal("expected at least one registered backend")
	}
	seen := make(map[string]bool, len(health))
	for _, entry := range health {
		if entry.Name == "" {
			t.Fatalf("backend health entry missing name: %+v", entry)
		}
		seen[entry.Name] = true
	}
	if !seen["codex"] && !seen["claude"] && !seen["shell"] {
		t.Fatalf("registered backend names look unexpected: %+v", health)
	}
}
