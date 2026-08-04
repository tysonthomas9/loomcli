package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/infra/sessionstoreadapter"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// --- stale signal file check ---

const staleSignalThreshold = 1 * time.Hour

type staleSignalFile struct {
	name string
	age  time.Duration
	path string
}

// getSignalDir returns the loom signal directory path.
// Package-level variable for testability.
var getSignalDir = defaultGetSignalDir

func defaultGetSignalDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("loom-signals-%d", os.Getuid()))
}

// scanStaleSignalFiles reads the signal directory and returns stale files and total file count.
func scanStaleSignalFiles(signalDir string) (stale []staleSignalFile, totalFiles int, err error) {
	entries, err := os.ReadDir(signalDir)
	if err != nil {
		return nil, 0, err
	}

	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		totalFiles++
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		if age > staleSignalThreshold {
			stale = append(stale, staleSignalFile{
				name: entry.Name(),
				age:  age.Truncate(time.Second),
				path: filepath.Join(signalDir, entry.Name()),
			})
		}
	}
	return stale, totalFiles, nil
}

func checkStaleSignalFiles() CheckResult {
	signalDir := getSignalDir()

	stale, totalFiles, err := scanStaleSignalFiles(signalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{} // skip — no signal directory
		}
		return CheckResult{
			Name:    "stale_signal_files",
			Status:  StatusWarn,
			Summary: "could not read signal directory",
			Detail:  err.Error(),
		}
	}
	if totalFiles == 0 {
		return CheckResult{} // skip — empty directory
	}
	if len(stale) == 0 {
		return CheckResult{
			Name:    "stale_signal_files",
			Status:  StatusPass,
			Summary: "no stale signal files",
		}
	}

	var details []string
	for _, s := range stale {
		details = append(details, fmt.Sprintf("%s (age=%s)", s.name, s.age))
	}

	if doctorFix {
		return fixStaleSignalFiles(stale, details)
	}

	return CheckResult{
		Name:    "stale_signal_files",
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d stale signal file(s) found", len(stale)),
		Detail:  strings.Join(details, "\n") + "\nRun: loom doctor --fix",
	}
}

func fixStaleSignalFiles(stale []staleSignalFile, details []string) CheckResult {
	fixed := 0
	var failures []string
	for _, s := range stale {
		if err := os.Remove(s.path); err != nil {
			if !os.IsNotExist(err) {
				failures = append(failures, fmt.Sprintf("%s: %v", s.name, err))
			} else {
				fixed++ // already gone
			}
		} else {
			fixed++
		}
	}
	if len(failures) > 0 {
		return CheckResult{
			Name:    "stale_signal_files",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("fixed %d stale signal file(s), %d failed", fixed, len(failures)),
			Detail:  strings.Join(append(details, failures...), "\n"),
		}
	}
	return CheckResult{
		Name:    "stale_signal_files",
		Status:  StatusPass,
		Summary: fmt.Sprintf("fixed %d stale signal file(s)", fixed),
		Detail:  strings.Join(details, "\n"),
	}
}

// --- stale session records check ---

type sessionScanResult struct {
	halfWritten   []string
	orphanedDirs  []string
	staleTmpFiles []string
}

// scanSessionDirs checks session directories for anomalies: missing metadata,
// missing index entries, and leftover temp files.
func scanSessionDirs(sessDir string, indexedIDs map[string]bool) (sessionScanResult, error) {
	dirEntries, err := os.ReadDir(sessDir)
	if err != nil {
		if os.IsNotExist(err) {
			return sessionScanResult{}, nil
		}
		return sessionScanResult{}, err
	}

	var result sessionScanResult
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dirPath := filepath.Join(sessDir, name)

		metaPath := filepath.Join(dirPath, "metadata.json")
		if _, statErr := os.Stat(metaPath); os.IsNotExist(statErr) {
			result.halfWritten = append(result.halfWritten, name)
			continue
		}

		// Check orphaned first — if not in index, that's the primary issue.
		// Skip tmp check for orphaned dirs to avoid double-counting.
		if !indexedIDs[name] {
			result.orphanedDirs = append(result.orphanedDirs, name)
			continue
		}

		tmpPath := filepath.Join(dirPath, "metadata.json.tmp")
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			result.staleTmpFiles = append(result.staleTmpFiles, name)
		}
	}
	return result, nil
}

// queryIndexedSessionIDs returns all session IDs present in the index.
// Also triggers auto-healing of stale running sessions as a side effect of Query.
func queryIndexedSessionIDs(store *sessions.Store) (map[string]bool, error) {
	records, err := store.Query(sessions.Filter{})
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(records))
	for _, rec := range records {
		ids[rec.SessionID] = true
	}
	return ids, nil
}

func checkStaleSessionRecords() CheckResult {
	sessStore, err := sessionstoreadapter.New(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return CheckResult{} // skip — sessions store not available
	}
	indexedIDs, err := queryIndexedSessionIDs(sessStore)
	if err != nil {
		return CheckResult{
			Name:    "stale_sessions",
			Status:  StatusWarn,
			Summary: "could not query session index",
			Detail:  err.Error(),
		}
	}
	scan, scanErr := scanSessionDirs(sessStore.Dir(), indexedIDs)
	if scanErr != nil {
		return CheckResult{
			Name:    "stale_sessions",
			Status:  StatusWarn,
			Summary: "could not scan sessions directory",
			Detail:  scanErr.Error(),
		}
	}
	totalIssues := len(scan.orphanedDirs) + len(scan.halfWritten) + len(scan.staleTmpFiles)
	if totalIssues == 0 {
		return CheckResult{
			Name:    "stale_sessions",
			Status:  StatusPass,
			Summary: "no stale or orphaned sessions",
		}
	}
	details := formatSessionIssues(scan)
	if doctorFix {
		return fixStaleSessionRecords(sessStore, sessStore.Dir(), scan.halfWritten, scan.orphanedDirs, scan.staleTmpFiles, details)
	}
	return CheckResult{
		Name:    "stale_sessions",
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d session issue(s) found", totalIssues),
		Detail:  strings.Join(details, "\n") + "\nRun: loom doctor --fix",
	}
}

func formatSessionIssues(scan sessionScanResult) []string {
	var details []string
	for _, name := range scan.halfWritten {
		details = append(details, fmt.Sprintf("half-written (no metadata.json): %s", name))
	}
	for _, name := range scan.orphanedDirs {
		details = append(details, fmt.Sprintf("orphaned (not in index): %s", name))
	}
	for _, name := range scan.staleTmpFiles {
		details = append(details, fmt.Sprintf("leftover tmp file: %s/metadata.json.tmp", name))
	}
	return details
}

func fixStaleSessionRecords(sessStore *sessions.Store, sessDir string, halfWritten, orphanedDirs, staleTmpFiles []string, details []string) CheckResult {
	fixed := 0
	var failures []string

	for _, name := range halfWritten {
		dirPath := filepath.Join(sessDir, name)
		if rmErr := os.RemoveAll(dirPath); rmErr != nil {
			failures = append(failures, fmt.Sprintf("remove %s: %v", name, rmErr))
		} else {
			fixed++
		}
	}

	for _, name := range staleTmpFiles {
		tmpPath := filepath.Join(sessDir, name, "metadata.json.tmp")
		if rmErr := os.Remove(tmpPath); rmErr != nil && !os.IsNotExist(rmErr) {
			failures = append(failures, fmt.Sprintf("remove %s/metadata.json.tmp: %v", name, rmErr))
		} else {
			fixed++
		}
	}

	for _, name := range orphanedDirs {
		meta, loadErr := sessStore.LoadMetadata(name)
		if loadErr != nil {
			failures = append(failures, fmt.Sprintf("re-index %s: %v", name, loadErr))
			continue
		}
		meta.NormalizeAfterLoad()
		if appendErr := sessionstoreadapter.ReIndex(sessStore, meta.SessionRecord); appendErr != nil {
			failures = append(failures, fmt.Sprintf("re-index %s: %v", name, appendErr))
		} else {
			fixed++
		}
	}

	if len(failures) > 0 {
		return CheckResult{
			Name:    "stale_sessions",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("fixed %d session issue(s), %d failed", fixed, len(failures)),
			Detail:  strings.Join(append(details, failures...), "\n"),
		}
	}
	return CheckResult{
		Name:    "stale_sessions",
		Status:  StatusPass,
		Summary: fmt.Sprintf("fixed %d session issue(s)", fixed),
		Detail:  strings.Join(details, "\n"),
	}
}
