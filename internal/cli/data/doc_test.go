package data

import "testing"

// TestCommandsShape asserts the exported Commands() contract that main.go
// depends on: returns exactly one top-level command (`data`) whose child
// set matches the documented 11 sub-commands.
func TestCommandsShape(t *testing.T) {
	t.Parallel()
	cmds := Commands()
	if len(cmds) != 1 {
		t.Fatalf("Commands() = %d commands, want 1", len(cmds))
	}
	root := cmds[0]
	if root.Use != "data" {
		t.Errorf("Commands()[0].Use = %q, want %q", root.Use, "data")
	}
	want := map[string]bool{
		"create":  false,
		"show":    false,
		"list":    false,
		"ready":   false,
		"blocked": false,
		"claim":   false,
		"close":   false,
		"comment": false,
		"monitor": false,
		"agents":  false,
		"agent":   false,
	}
	for _, c := range root.Commands() {
		name := c.Name()
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected sub-command %q not registered on `loom data`", name)
		}
	}
}

// TestAgentSubTreeShape asserts the four agent control verbs are wired
// under `loom data agent`.
func TestAgentSubTreeShape(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"stop": false, "start": false, "restart": false, "yield": false}
	for _, c := range agentCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected agent sub-command %q not registered", name)
		}
	}
}
