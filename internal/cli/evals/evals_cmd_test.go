package evals

import (
	"strings"
	"testing"
)

func TestEvalsCommandShape(t *testing.T) {
	if evalsCmd.Use != "evals" || evalsCmd.GroupID != "workspace" {
		t.Fatalf("evals command = use %q group %q", evalsCmd.Use, evalsCmd.GroupID)
	}
	want := map[string]bool{"enable": false, "disable": false, "rejudge": false}
	for _, cmd := range evalsCmd.Commands() {
		if _, ok := want[cmd.Name()]; ok {
			want[cmd.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("evals command missing %s subcommand", name)
		}
	}
	if evalsEnableCmd.Flags().Lookup("schedule") == nil {
		t.Fatal("evals enable missing --schedule flag")
	}
	if !strings.Contains(evalsEnableCmd.Long, "loom doctor --fix") {
		t.Fatal("evals enable help must mention loom doctor --fix backfill")
	}
}
