package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/archtest"
)

type snapshotProvenance struct {
	SourceHead  string `yaml:"source_head"`
	SourceDirty bool   `yaml:"source_dirty"`
}

type directWriteSnapshot struct {
	SourceHead       string                    `yaml:"source_head"`
	SourceDirty      bool                      `yaml:"source_dirty"`
	AnalysisProfiles []string                  `yaml:"analysis_profiles"`
	Writes           []archtest.DirectWriteUse `yaml:"writes"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) != 1 || (args[0] != "check" && args[0] != "snapshot-direct-writes") {
		return fmt.Errorf("usage: archcheck check|snapshot-direct-writes")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	manifestDir := filepath.Join(root, "internal", "archtest", "testdata")
	if args[0] == "snapshot-direct-writes" {
		return runDirectWriteSnapshot(root, manifestDir, out)
	}
	return runRepositoryCheck(root, manifestDir, out)
}

func runDirectWriteSnapshot(root, manifestDir string, out io.Writer) error {
	matrix, err := archtest.LoadAnalysisMatrix(filepath.Join(manifestDir, "analysis-matrix.yaml"))
	if err != nil {
		return err
	}
	inventory, err := archtest.LoadDirectWriteInventory(filepath.Join(manifestDir, "direct-writes.yaml"))
	if err != nil {
		return err
	}
	writes, err := archtest.SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		return err
	}
	provenance, err := gitSourceProvenance(root)
	if err != nil {
		return err
	}
	return encodeDirectWriteSnapshot(out, directWriteSnapshot{
		SourceHead:       provenance.SourceHead,
		SourceDirty:      provenance.SourceDirty,
		AnalysisProfiles: inventory.AnalysisProfiles,
		Writes:           writes,
	})
}

func runRepositoryCheck(root, manifestDir string, out io.Writer) error {
	report, err := archtest.CheckRepository(root, manifestDir)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Architecture guardrails passed\n"+
		"  composite Store files: %d/%d\n"+
		"  outside composition: %d/%d\n"+
		"  legacy handler imports: %d/%d\n"+
		"  direct persistence-write rows: %d\n"+
		"  capability module roots: %d\n"+
		"  reviewed mutation commands: %d\n"+
		"  named runtime components: %d\n"+
		"  in-scope non-test goroutine launch definitions: %d\n"+
		"  performance records: %d (%d measured, %d explicitly deferred)\n"+
		"  pending architecture decisions: %d\n"+
		"  build profiles enforced: %d/%d (all-files AST checks active)\n",
		len(report.CompositeStoreFiles), report.CompositeStoreMaximum,
		len(report.CompositeStoreOutside), report.CompositeStoreOutsideMaximum,
		len(report.LegacyHandlerImports), report.LegacyHandlerImportMaximum,
		report.DirectPersistenceWrites,
		len(report.ModuleRoots), report.MutationCommands, report.RuntimeComponents, report.RuntimeGoroutineLaunches,
		report.PerformanceMetrics, report.PerformanceMetricsMeasured, report.PerformanceMetricsDeferred,
		len(report.PendingDecisions), report.AnalysisProfilesEnforced, report.AnalysisProfileTotal)
	return err
}

func gitSourceProvenance(root string) (snapshotProvenance, error) {
	headCommand := exec.Command("git", "rev-parse", "--verify", "HEAD")
	headCommand.Dir = root
	head, err := headCommand.Output()
	if err != nil {
		return snapshotProvenance{}, fmt.Errorf("read snapshot source HEAD: %w", err)
	}
	statusCommand := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=normal")
	statusCommand.Dir = root
	status, err := statusCommand.Output()
	if err != nil {
		return snapshotProvenance{}, fmt.Errorf("read snapshot source status: %w", err)
	}
	return parseSnapshotProvenance(head, status)
}

func parseSnapshotProvenance(head, status []byte) (snapshotProvenance, error) {
	trimmedHead := strings.TrimSpace(string(head))
	if len(trimmedHead) != 40 {
		return snapshotProvenance{}, fmt.Errorf("snapshot source HEAD must be a full lowercase SHA, got %q", trimmedHead)
	}
	for _, character := range trimmedHead {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return snapshotProvenance{}, fmt.Errorf("snapshot source HEAD must be a full lowercase SHA, got %q", trimmedHead)
		}
	}
	return snapshotProvenance{
		SourceHead:  trimmedHead,
		SourceDirty: len(bytes.TrimSpace(status)) > 0,
	}, nil
}

func encodeDirectWriteSnapshot(out io.Writer, snapshot directWriteSnapshot) error {
	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(2)
	if err := encoder.Encode(snapshot); err != nil {
		_ = encoder.Close()
		return err
	}
	return encoder.Close()
}
