package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Disk headroom policy. The absolute floors are what one fleet-wide gate wave
// costs regardless of how big the volume is; the percentage floors keep the
// check meaningful on volumes much larger than this one. The operative
// threshold is max(absolute, percentage).
const (
	defaultDiskFailGiB = 10 // below one wave of gate builds; ENOSPC imminent
	defaultDiskWarnGiB = 25 // roughly one day of headroom at 17 GB/day
	diskFailPercent    = 5
	diskWarnPercent    = 10

	bytesPerGiB = 1 << 30

	// A janitor sidecar older than this tells us nothing about the disk now.
	janitorStatusMaxAge = 2 * time.Hour

	// defaultJanitorStatusPath is the sidecar written by the local-stack
	// janitor. Overridable via LOOM_JANITOR_STATUS.
	defaultJanitorStatusPath = "/Users/oleh/local-stack/janitor/status.json"

	// diskCheckPath is the volume that actually fills up on this machine.
	diskCheckPath = "/System/Volumes/Data"
)

// janitorSwept records what the janitor reclaimed on its last pass.
type janitorSwept struct {
	GocacheBytes uint64 `json:"gocache_bytes"`
	GocacheFiles int    `json:"gocache_files"`
	PM2Bytes     uint64 `json:"pm2_bytes"`
	PM2Files     int    `json:"pm2_files"`
}

// janitorReport is the sidecar schema written by the size-capped janitor.
// The key names are a cross-repo contract; do not rename them.
type janitorReport struct {
	TS             string            `json:"ts"`
	FreeBytes      uint64            `json:"free_bytes"`
	TotalBytes     uint64            `json:"total_bytes"`
	Consumers      map[string]uint64 `json:"consumers"`
	Swept          janitorSwept      `json:"swept"`
	ShortfallBytes uint64            `json:"shortfall_bytes"`
	Errors         int               `json:"errors"`
	DryRun         bool              `json:"dry_run"`
}

// consumerLabels maps sidecar consumer keys to a human label and the path
// behind it. An unknown key falls back to the raw key with no path.
var consumerLabels = map[string]struct{ label, path string }{
	"gocache":        {"go build cache", "~/Library/Caches/go-build"},
	"gomodcache":     {"go module cache", "~/go/pkg/mod"},
	"pm2_logs":       {"pm2 logs", "~/.pm2/logs"},
	"loom_worktrees": {"loom worktrees", "~/.loom/workspaces"},
	"loom_deploys":   {"loom deploys", "~/Work/loom/deploys"},
}

// Package-level seams, swapped in tests. Same shape as listLoomTmuxSessions.
var diskUsage = defaultDiskUsage
var janitorStatus = defaultJanitorStatus

// defaultDiskUsage reports free and total bytes for the filesystem holding
// path. x/sys/unix hides the darwin/linux Statfs_t field-type divergence that
// bare syscall would need build tags for.
func defaultDiskUsage(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	// Bsize is int32 on darwin and int64 on linux; Bavail/Blocks are uint64
	// on both.
	bsize := uint64(st.Bsize)
	return st.Bavail * bsize, st.Blocks * bsize, nil
}

// defaultJanitorStatus reads the janitor sidecar. Missing, unparseable or
// stale (>2h, by file mtime or by its own timestamp) all degrade to
// (zero, false) — never an error, because a dead janitor must not break the
// disk reading that does not depend on it.
func defaultJanitorStatus() (janitorReport, bool) {
	path := defaultJanitorStatusPath
	if override := os.Getenv("LOOM_JANITOR_STATUS"); override != "" {
		path = override
	}

	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > janitorStatusMaxAge {
		return janitorReport{}, false
	}

	data, err := os.ReadFile(path) //nolint:gosec // operator-configured status path
	if err != nil {
		return janitorReport{}, false
	}
	var rep janitorReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return janitorReport{}, false
	}
	if ts, err := time.Parse(time.RFC3339, rep.TS); err == nil &&
		time.Since(ts) > janitorStatusMaxAge {
		return janitorReport{}, false
	}
	return rep, true
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

// checkDiskHeadroom reports free space on the volume that fills up, and names
// the largest consumer when the janitor sidecar is fresh enough to say.
//
// Unconditional: it never returns the zero CheckResult, so the skip
// convention in runDoctor never applies to it.
func checkDiskHeadroom() CheckResult {
	free, total, err := diskUsage(diskCheckPath)
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
	rep, haveRep := janitorStatus()
	return evaluateDiskHeadroom(free, total, rep, haveRep,
		diskThresholdGiB("LOOM_DISK_FAIL_GIB", defaultDiskFailGiB),
		diskThresholdGiB("LOOM_DISK_WARN_GIB", defaultDiskWarnGiB))
}

// evaluateDiskHeadroom is the pure policy used by checkDiskHeadroom.
func evaluateDiskHeadroom(free, total uint64, rep janitorReport, haveRep bool,
	failGiB, warnGiB uint64) CheckResult {
	failFloor := headroomFloor(failGiB, total, diskFailPercent)
	warnFloor := headroomFloor(warnGiB, total, diskWarnPercent)

	result := CheckResult{
		Name:    "disk_headroom",
		Status:  StatusPass,
		Summary: diskSummary(free, total),
	}
	switch {
	case free < failFloor:
		result.Status = StatusFail
	case free < warnFloor:
		result.Status = StatusWarn
	default:
		return result
	}
	result.Detail = strings.Join(diskDetail(rep, haveRep), "\n")
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

// diskDetail builds the WARN/FAIL detail lines: who is eating the disk, and
// whether the thing meant to bound it is even running.
func diskDetail(rep janitorReport, haveRep bool) []string {
	if !haveRep {
		return []string{"janitor status stale/absent — the cache bound may not be running"}
	}

	var lines []string
	if key, size := largestConsumer(rep.Consumers); key != "" {
		info, known := consumerLabels[key]
		if !known {
			lines = append(lines, fmt.Sprintf("largest consumer: %s %d GiB", key, size/bytesPerGiB))
		} else {
			lines = append(lines, fmt.Sprintf("largest consumer: %s %d GiB (%s)",
				info.label, size/bytesPerGiB, expandHome(info.path)))
		}
	}
	lines = append(lines, fmt.Sprintf("janitor last swept %s, freed %d GiB", rep.TS,
		(rep.Swept.GocacheBytes+rep.Swept.PM2Bytes)/bytesPerGiB))
	if rep.ShortfallBytes > 0 {
		lines = append(lines, fmt.Sprintf(
			"shortfall: janitor could not reach its low-water mark by %d GiB",
			rep.ShortfallBytes/bytesPerGiB))
	}
	return lines
}

// largestConsumer picks the biggest entry, breaking ties by key so the
// output is stable.
func largestConsumer(consumers map[string]uint64) (string, uint64) {
	keys := make([]string, 0, len(consumers))
	for k := range consumers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var bestKey string
	var bestSize uint64
	for _, k := range keys {
		if consumers[k] > bestSize {
			bestKey, bestSize = k, consumers[k]
		}
	}
	return bestKey, bestSize
}

func expandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, rest)
}
