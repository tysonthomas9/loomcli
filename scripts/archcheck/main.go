package main

import (
	"bytes"
	"errors"
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

type repositoryCheckFunc func(root, manifestDir string) (archtest.Report, error)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	return runWithRepositoryCheck(args, out, archtest.CheckRepository)
}

func runWithRepositoryCheck(args []string, out io.Writer, checkRepository repositoryCheckFunc) error {
	if len(args) != 1 || (args[0] != "check" && args[0] != "snapshot-direct-writes" && args[0] != "refresh-direct-writes") {
		return fmt.Errorf("usage: archcheck check|snapshot-direct-writes|refresh-direct-writes")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	manifestDir := filepath.Join(root, "internal", "archtest", "testdata")
	if args[0] == "snapshot-direct-writes" {
		return runDirectWriteSnapshot(root, manifestDir, out)
	}
	if args[0] == "refresh-direct-writes" {
		return runDirectWriteRefresh(root, manifestDir, out)
	}
	return runRepositoryCheck(root, manifestDir, out, checkRepository)
}

func runDirectWriteSnapshot(root, manifestDir string, out io.Writer) error {
	snapshot, _, err := buildDirectWriteSnapshot(root, manifestDir)
	if err != nil {
		return err
	}
	return encodeDirectWriteSnapshot(out, snapshot)
}

func buildDirectWriteSnapshot(root, manifestDir string) (directWriteSnapshot, archtest.DirectWriteInventory, error) {
	matrix, err := archtest.LoadAnalysisMatrix(filepath.Join(manifestDir, "analysis-matrix.yaml"))
	if err != nil {
		return directWriteSnapshot{}, archtest.DirectWriteInventory{}, err
	}
	inventory, err := archtest.LoadDirectWriteSnapshotPolicy(filepath.Join(manifestDir, "direct-writes.yaml"))
	if err != nil {
		return directWriteSnapshot{}, archtest.DirectWriteInventory{}, err
	}
	writes, err := archtest.SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		return directWriteSnapshot{}, archtest.DirectWriteInventory{}, err
	}
	provenance, err := gitSourceProvenance(root)
	if err != nil {
		return directWriteSnapshot{}, archtest.DirectWriteInventory{}, err
	}
	return directWriteSnapshot{
		SourceHead:       provenance.SourceHead,
		SourceDirty:      provenance.SourceDirty,
		AnalysisProfiles: inventory.AnalysisProfiles,
		Writes:           writes,
	}, inventory, nil
}

func runDirectWriteRefresh(root, manifestDir string, out io.Writer) error {
	snapshot, inventory, err := buildDirectWriteSnapshot(root, manifestDir)
	if err != nil {
		return err
	}
	path := filepath.Join(manifestDir, "direct-writes.yaml")
	// #nosec G304 -- manifestDir is the repository-owned architecture fixture directory.
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read direct-write inventory for refresh: %w", err)
	}
	refreshed, legacyRemoved, err := refreshDirectWriteInventory(source, snapshot, inventory.LegacyDriver)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, refreshed, 0o644); err != nil {
		return fmt.Errorf("write refreshed direct-write inventory: %w", err)
	}
	_, err = fmt.Fprintf(out, "Refreshed direct-write inventory: %d rows; legacy baseline removed=%t; source_dirty=%t\n", len(snapshot.Writes), legacyRemoved, snapshot.SourceDirty)
	return err
}

func refreshDirectWriteInventory(source []byte, snapshot directWriteSnapshot, legacy *archtest.LegacyDirectWriteBaseline) ([]byte, bool, error) {
	const writesMarker = "writes:\n"
	const mechanismsMarker = "generic_mechanisms:\n"
	writesStart := bytes.Index(source, []byte(writesMarker))
	mechanismsStart := bytes.Index(source, []byte(mechanismsMarker))
	if writesStart < 0 || mechanismsStart < 0 || mechanismsStart <= writesStart {
		return nil, false, errors.New("direct-write inventory is missing ordered writes and generic_mechanisms blocks")
	}
	prefix := string(source[:writesStart])
	lines := strings.Split(prefix, "\n")
	foundHead := false
	for index, line := range lines {
		if strings.HasPrefix(line, "source_head: ") {
			lines[index] = "source_head: " + snapshot.SourceHead
			foundHead = true
			break
		}
	}
	if !foundHead {
		return nil, false, errors.New("direct-write inventory is missing source_head")
	}
	prefix = strings.Join(lines, "\n")
	renderedWrites, err := yaml.Marshal(struct {
		Writes []archtest.DirectWriteUse `yaml:"writes"`
	}{Writes: snapshot.Writes})
	if err != nil {
		return nil, false, fmt.Errorf("render refreshed direct writes: %w", err)
	}
	suffix := source[mechanismsStart:]
	legacyRemoved := false
	if legacy != nil && !snapshotContainsRoot(snapshot.Writes, legacy.Root) {
		if legacyStart := bytes.Index(suffix, []byte("legacy_driver:\n")); legacyStart >= 0 {
			suffix = bytes.TrimRight(suffix[:legacyStart], "\n")
			suffix = append(suffix, '\n')
			legacyRemoved = true
		}
	}
	result := make([]byte, 0, len(prefix)+len(renderedWrites)+len(suffix))
	result = append(result, prefix...)
	result = append(result, renderedWrites...)
	result = append(result, suffix...)
	return result, legacyRemoved, nil
}

func snapshotContainsRoot(writes []archtest.DirectWriteUse, root string) bool {
	root = strings.TrimSuffix(root, "/") + "/"
	for _, write := range writes {
		if strings.HasPrefix(write.File, root) {
			return true
		}
	}
	return false
}

func runRepositoryCheck(root, manifestDir string, out io.Writer, checkRepository repositoryCheckFunc) error {
	report, err := checkRepository(root, manifestDir)
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
