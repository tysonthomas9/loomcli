package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/archtest"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "check" {
		return fmt.Errorf("usage: archcheck check")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	manifestDir := filepath.Join(root, "internal", "archtest", "testdata")
	report, err := archtest.CheckRepository(root, manifestDir)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Architecture guardrails passed\n"+
		"  composite Store files: %d/%d\n"+
		"  outside composition: %d/%d\n"+
		"  legacy handler imports: %d/%d\n"+
		"  capability module roots: %d\n"+
		"  pending architecture decisions: %d\n"+
		"  build profiles deferred: %d/%d (bootstrap AST checks active)\n",
		len(report.CompositeStoreFiles), report.CompositeStoreMaximum,
		len(report.CompositeStoreOutside), report.CompositeStoreOutsideMaximum,
		len(report.LegacyHandlerImports), report.LegacyHandlerImportMaximum,
		len(report.ModuleRoots), len(report.PendingDecisions),
		report.AnalysisProfileTotal-report.AnalysisProfilesEnforced, report.AnalysisProfileTotal)
	return err
}
