package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// loom --help lists only the small Git CLI (S3). The plumbing commands are
// hidden but still registered, so they still run; the removed ones are gone.
func TestHelpListsOnlyTheSmallGitCLI(t *testing.T) {
	root := cli.GetRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"--help"})
	t.Cleanup(func() { root.SetOut(nil); root.SetArgs(nil) })
	if err := cli.Execute(); err != nil {
		t.Fatal(err)
	}
	// No line anywhere in the help, including the hand-written overview, lists a
	// hidden or removed command.
	for _, line := range strings.Split(output.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(line, "  ") {
			continue
		}
		for _, name := range []string{"apply", "pr", "pr-stack", "pull", "reset", "restack", "push", "delivery-mode",
			"lead-may-merge", "retention-sweep", "merge-up-to", "request-merge", "confirm-merge"} {
			if fields[0] == name {
				t.Fatalf("help lists %s: %q", name, line)
			}
		}
	}
	_, section, found := strings.Cut(output.String(), "Git Operations:\n")
	if !found {
		t.Fatalf("no Git section in help:\n%s", output.String())
	}
	section, _, _ = strings.Cut(section, "\n\n")
	var shown []string
	for _, line := range strings.Split(section, "\n") {
		shown = append(shown, strings.Fields(line)[0])
	}
	if got := strings.Join(shown, " "); got != "abandon approve git-settings merge reject sync unapply" {
		t.Fatalf("Git help shows %q", got)
	}
	for _, hidden := range []string{"apply", "pr", "pr-stack", "pull", "reset", "restack"} {
		if command, _, err := root.Find([]string{hidden}); err != nil || command.Name() != hidden || !command.Hidden {
			t.Fatalf("%s is not a hidden command: %v", hidden, err)
		}
	}
	for _, removed := range []string{"push", "delivery-mode", "lead-may-merge", "retention-sweep", "merge-up-to", "request-merge", "confirm-merge"} {
		if command, _, _ := root.Find([]string{removed}); command != root {
			t.Fatalf("%s is still a command", removed)
		}
	}
}
