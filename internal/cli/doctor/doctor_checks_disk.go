package doctor

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// Disk headroom policy. The absolute floors are what one fleet-wide gate wave
// costs regardless of how big the volume is; the percentage floors keep the
// check meaningful on large volumes. The operative threshold is
// max(absolute, percentage).
const (
	defaultDiskFailGiB = 10 // below one wave of gate builds; ENOSPC imminent
	defaultDiskWarnGiB = 25 // roughly a day of headroom for a busy fleet
	diskFailPercent    = 5
	diskWarnPercent    = 10

	bytesPerGiB = 1 << 30
)

// errDiskUsageUnsupported is returned by defaultDiskUsage on platforms where
// free space is not measured. The check skips rather than warns there.
var errDiskUsageUnsupported = errors.New("free disk space is not measured on this platform")

// diskUsage is the package-level seam swapped in tests. Same shape as
// listLoomTmuxSessions. defaultDiskUsage lives in the per-platform files.
var diskUsage = defaultDiskUsage

// diskCheckTarget picks the path whose filesystem is measured: the loom
// workspace root, which holds the worktrees and runtime state, or the home
// directory when no workspace is configured. runtimeDir is
// cli.GetWorkspaceRuntimeDir() in production; it is "." when unconfigured.
func diskCheckTarget(runtimeDir string) string {
	if runtimeDir != "" && runtimeDir != "." {
		return runtimeDir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}

// diskThresholdGiB resolves an env-tunable threshold. A missing, unparseable
// or zero value falls back to the default: a bad env var must not fail the
// check.
func diskThresholdGiB(env string, def uint64) uint64 {
	raw := os.Getenv(env)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || v == 0 {
		return def
	}
	return v
}

// checkDiskHeadroom reports free space on the filesystem holding the loom
// workspace. It skips on platforms where free space is not measured.
func checkDiskHeadroom() CheckResult {
	target := diskCheckTarget(cli.GetWorkspaceRuntimeDir())
	free, total, err := diskUsage(target)
	if errors.Is(err, errDiskUsageUnsupported) {
		return CheckResult{}
	}
	if err != nil {
		// An unmeasurable disk is not a known-full disk, and a spurious FAIL
		// exits the whole command non-zero.
		return CheckResult{
			Name:    "disk_headroom",
			Status:  StatusWarn,
			Summary: "could not determine free disk space",
			Detail:  err.Error(),
		}
	}
	return evaluateDiskHeadroom(target, free, total,
		diskThresholdGiB("LOOM_DISK_FAIL_GIB", defaultDiskFailGiB),
		diskThresholdGiB("LOOM_DISK_WARN_GIB", defaultDiskWarnGiB))
}

// evaluateDiskHeadroom is the pure policy used by checkDiskHeadroom. target
// is the measured path, named in the WARN/FAIL detail.
func evaluateDiskHeadroom(target string, free, total, failGiB, warnGiB uint64) CheckResult {
	failFloor := headroomFloor(failGiB, total, diskFailPercent)
	warnFloor := headroomFloor(warnGiB, total, diskWarnPercent)

	result := CheckResult{
		Name:    "disk_headroom",
		Status:  StatusPass,
		Summary: diskSummary(free, total),
	}
	floor, env, verb := warnFloor, "LOOM_DISK_WARN_GIB", "warns"
	switch {
	case free < failFloor:
		result.Status = StatusFail
		floor, env, verb = failFloor, "LOOM_DISK_FAIL_GIB", "fails"
	case free < warnFloor:
		result.Status = StatusWarn
	default:
		return result
	}
	// Round the floor up, so a 45.99 GiB floor reads as "below 46 GiB".
	floorGiB := (floor + bytesPerGiB - 1) / bytesPerGiB
	result.Detail = fmt.Sprintf("measured the filesystem holding %s; the check %s below %d GiB free (set %s to change it)",
		target, verb, floorGiB, env)
	return result
}

// headroomFloor is max(absolute, percentage). total == 0 (unknown volume
// size) falls back to the absolute floor alone.
func headroomFloor(absGiB, total uint64, percent uint64) uint64 {
	floor := absGiB * bytesPerGiB
	if total == 0 {
		return floor
	}
	if pct := total / 100 * percent; pct > floor {
		return pct
	}
	return floor
}

func diskSummary(free, total uint64) string {
	if total == 0 {
		return fmt.Sprintf("%d GiB free (total unknown)", free/bytesPerGiB)
	}
	return fmt.Sprintf("%d GiB free of %d GiB (%d%%)",
		free/bytesPerGiB, total/bytesPerGiB, free*100/total)
}
