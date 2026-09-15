package cli

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

// TestRootPersistentPreRunSilencesUsage exercises the real hook installed on
// rootCmd with rootPreRun stubbed out, so ResolveAndSetBackend and DefaultDeps
// side effects stay out of the test. It mutates a package var, so no t.Parallel.
func TestRootPersistentPreRunSilencesUsage(t *testing.T) {
	orig := rootPreRun
	t.Cleanup(func() { rootPreRun = orig })

	sentinel := errors.New("resolve backend failed")
	rootPreRun = func(_ *cobra.Command, _ []string) error { return sentinel }

	dummy := &cobra.Command{Use: "dummy"}
	err := rootCmd.PersistentPreRunE(dummy, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("PersistentPreRunE() error = %v, want %v", err, sentinel)
	}
	if !dummy.SilenceUsage {
		t.Error("a pre-run failure must set SilenceUsage on the command")
	}

	rootPreRun = func(_ *cobra.Command, _ []string) error { return nil }
	ok := &cobra.Command{Use: "ok"}
	if err := rootCmd.PersistentPreRunE(ok, nil); err != nil {
		t.Fatalf("PersistentPreRunE() error = %v, want nil", err)
	}
	if ok.SilenceUsage {
		t.Error("a successful pre-run must leave SilenceUsage untouched")
	}
}
