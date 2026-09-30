package git

import (
	"strings"
	"testing"
)

func TestPrCmdRequiresLeadAndChange(t *testing.T) {
	for _, args := range [][]string{nil, {"lead"}, {"lead", "change", "main"}} {
		if err := prCmd.Args(prCmd, args); err == nil {
			t.Fatalf("accepted legacy PR arguments %v", args)
		}
	}
	if err := prCmd.Args(prCmd, []string{"lead", "change"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prCmd.Use, "<change>") || prCmd.Flags().Lookup("all") != nil {
		t.Fatal("CLI still advertises legacy branch or all-workspace PR mode")
	}
}

func TestPrStackCmdRequiresStableStackAndOrderedChanges(t *testing.T) {
	for _, args := range [][]string{nil, {"stack"}, {"stack", "lead"}} {
		if err := prStackCmd.Args(prStackCmd, args); err == nil {
			t.Fatalf("accepted incomplete stack arguments %v", args)
		}
	}
	if err := prStackCmd.Args(prStackCmd, []string{"feature-1", "lead", "A", "B"}); err != nil {
		t.Fatal(err)
	}
}
