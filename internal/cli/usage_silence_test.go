package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var errRuntimeFailure = errors.New("backend unavailable")

// newSilenceTree builds a fresh command tree. Fresh values matter: SilenceUsage
// is sticky on the cobra.Command struct, so reusing a package-level command
// that already reached RunE once would make later assertions meaningless.
func newSilenceTree(runErr error) (root, child *cobra.Command, out *bytes.Buffer) {
	root = &cobra.Command{Use: "root", SilenceErrors: true}
	child = &cobra.Command{
		Use:  "child",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error { return runErr },
	}
	child.Flags().String("need", "", "a required flag")
	root.AddCommand(child)

	out = &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	return root, child, out
}

func TestSilenceUsageOnRunErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		runErr     error
		required   bool
		wantUsage  bool
		wantErr    bool
		wantInText string
	}{
		{name: "runtime failure", args: []string{"child", "ok"}, runErr: errRuntimeFailure, wantErr: true, wantInText: "backend unavailable"},
		{name: "unknown flag", args: []string{"child", "--nope"}, runErr: errRuntimeFailure, wantUsage: true, wantErr: true, wantInText: "unknown flag"},
		{name: "wrong arg count", args: []string{"child"}, runErr: errRuntimeFailure, wantUsage: true, wantErr: true, wantInText: "arg"},
		{name: "missing required flag", args: []string{"child", "x"}, runErr: errRuntimeFailure, required: true, wantUsage: true, wantErr: true, wantInText: `required flag(s) "need" not set`},
		// Cobra rejects an unknown subcommand in Find(), before any leaf
		// executes, and answers with a "Run 'root --help'" hint rather than
		// the usage block. Unaffected by the wrapper either way; asserted so a
		// cobra bump that changes it is visible.
		{name: "unknown subcommand", args: []string{"nosuch"}, runErr: errRuntimeFailure, wantErr: true, wantInText: "unknown command"},
		{name: "success", args: []string{"child", "ok"}, runErr: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, child, out := newSilenceTree(tc.runErr)
			if tc.required {
				if err := child.MarkFlagRequired("need"); err != nil {
					t.Fatalf("MarkFlagRequired: %v", err)
				}
			}
			SilenceUsageOnRunErrors(root)

			root.SetArgs(tc.args)
			err := root.Execute()

			if tc.wantErr != (err != nil) {
				t.Fatalf("Execute() error = %v, want error: %v", err, tc.wantErr)
			}
			gotUsage := strings.Contains(out.String(), "Usage:")
			if gotUsage != tc.wantUsage {
				t.Errorf("usage block present = %v, want %v; output:\n%s", gotUsage, tc.wantUsage, out.String())
			}
			if tc.wantInText != "" {
				text := out.String() + "\n"
				if err != nil {
					text += err.Error()
				}
				if !strings.Contains(text, tc.wantInText) {
					t.Errorf("output/error missing %q; got:\n%s", tc.wantInText, text)
				}
			}
		})
	}
}

func TestSilenceUsageSkipsRunOnlyCommands(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	group := &cobra.Command{Use: "group"}
	runOnly := &cobra.Command{Use: "runonly", Run: func(_ *cobra.Command, _ []string) {}}
	group.AddCommand(runOnly)
	root.AddCommand(group)

	SilenceUsageOnRunErrors(root)

	if IsUsageSilenced(group) {
		t.Error("group command with no RunE should not be wrapped")
	}
	if group.RunE != nil {
		t.Error("SilenceUsageOnRunErrors must not synthesize a RunE")
	}
	if IsUsageSilenced(runOnly) || runOnly.RunE != nil {
		t.Error("Run-only command should not be wrapped")
	}
	if !group.Runnable() && runOnly.Runnable() != true {
		t.Error("Runnable() semantics changed")
	}
}

func TestSilenceUsageIsIdempotent(t *testing.T) {
	root, child, _ := newSilenceTree(errRuntimeFailure)
	SilenceUsageOnRunErrors(root)
	first := IsUsageSilenced(child)
	wrapped := child.RunE
	SilenceUsageOnRunErrors(root)

	if !first || !IsUsageSilenced(child) {
		t.Fatal("child should be recorded as silenced")
	}
	if err := wrapped(child, []string{"x"}); !errors.Is(err, errRuntimeFailure) {
		t.Fatalf("re-wrapping changed behavior: %v", err)
	}
}

// TestPreRunFailureSilencesUsage mirrors the shape root.go installs: a failing
// persistent pre-run sets SilenceUsage, while the required-flag check — which
// cobra runs AFTER every pre-run hook — keeps its usage block.
func TestPreRunFailureSilencesUsage(t *testing.T) {
	errPreRun := errors.New("unknown backend \"nope\"")

	build := func(preRunErr error) (*cobra.Command, *bytes.Buffer) {
		root := &cobra.Command{Use: "root", SilenceErrors: true}
		root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
			if preRunErr != nil {
				cmd.SilenceUsage = true
				return preRunErr
			}
			return nil
		}
		child := &cobra.Command{Use: "child", RunE: func(_ *cobra.Command, _ []string) error { return nil }}
		child.Flags().String("need", "", "a required flag")
		if err := child.MarkFlagRequired("need"); err != nil {
			t.Fatalf("MarkFlagRequired: %v", err)
		}
		root.AddCommand(child)
		SilenceUsageOnRunErrors(root)

		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetErr(out)
		return root, out
	}

	root, out := build(errPreRun)
	root.SetArgs([]string{"child"})
	if err := root.Execute(); !errors.Is(err, errPreRun) {
		t.Fatalf("Execute() error = %v, want %v", err, errPreRun)
	}
	if strings.Contains(out.String(), "Usage:") {
		t.Errorf("pre-run failure printed usage block:\n%s", out.String())
	}

	root, out = build(nil)
	root.SetArgs([]string{"child"})
	if err := root.Execute(); err == nil {
		t.Fatal("missing required flag should fail")
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("missing required flag must still print usage:\n%s", out.String())
	}
}
