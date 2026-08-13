package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
