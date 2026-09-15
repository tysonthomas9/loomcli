package doctor

import (
	"os"
	"strings"
	"testing"
)

const gib = uint64(1 << 30)

func TestEvaluateDiskHeadroom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		free, total uint64
		failGiB     uint64
		warnGiB     uint64
		want        CheckStatus
		wantSummary string
	}{
		{
			name: "comfortable free space passes",
			free: 300 * gib, total: 460 * gib,
			want: StatusPass, wantSummary: "300 GiB free of 460 GiB (65%)",
		},
		{
			// max(25 GiB, 10% of 460 GiB) = 46 GiB, so 40 GiB is already
			// inside the warn band on this volume.
			name: "40 GiB of 460 GiB warns on the percentage floor",
			free: 40 * gib, total: 460 * gib,
			want: StatusWarn, wantSummary: "40 GiB free of 460 GiB (8%)",
		},
		{
			name: "20 GiB on a small volume warns on the absolute floor",
			free: 20 * gib, total: 100 * gib,
			want: StatusWarn, wantSummary: "20 GiB free of 100 GiB (20%)",
		},
		{
			name: "11 GiB of 460 GiB fails",
			free: 11 * gib, total: 460 * gib,
			want: StatusFail, wantSummary: "11 GiB free of 460 GiB (2%)",
		},
		{
			// Above the 10 GiB absolute floor, below 5% of the volume: the
			// percentage arm is what produces the FAIL.
			name: "percentage floor dominates the absolute floor",
			free: 20 * gib, total: 460 * gib,
			want: StatusFail, wantSummary: "20 GiB free of 460 GiB (4%)",
		},
		{
			name: "unknown total does not divide by zero",
			free: 5 * gib, total: 0,
			want: StatusFail, wantSummary: "5 GiB free (total unknown)",
		},
		{
			name: "env overrides raise the failure floor",
			free: 100 * gib, total: 460 * gib,
			failGiB: 999, warnGiB: 1000,
			want: StatusFail, wantSummary: "100 GiB free of 460 GiB (21%)",
		},
		{
			name: "env overrides lower the floors",
			free: 5 * gib, total: 20 * gib,
			failGiB: 1, warnGiB: 2,
			want: StatusPass, wantSummary: "5 GiB free of 20 GiB (25%)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			failGiB, warnGiB := tt.failGiB, tt.warnGiB
			if failGiB == 0 {
				failGiB = defaultDiskFailGiB
			}
			if warnGiB == 0 {
				warnGiB = defaultDiskWarnGiB
			}
			got := evaluateDiskHeadroom("/ws", tt.free, tt.total, failGiB, warnGiB)
			if got.Name != "disk_headroom" {
				t.Errorf("Name = %q, want %q", got.Name, "disk_headroom")
			}
			if got.Status != tt.want {
				t.Errorf("Status = %v, want %v", got.Status, tt.want)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
		})
	}
}

func TestEvaluateDiskHeadroomDetail(t *testing.T) {
	t.Parallel()

	t.Run("pass has no detail", func(t *testing.T) {
		t.Parallel()
		got := evaluateDiskHeadroom("/ws", 300*gib, 460*gib, defaultDiskFailGiB, defaultDiskWarnGiB)
		if got.Detail != "" {
			t.Errorf("Detail = %q, want empty on pass", got.Detail)
		}
	})

	t.Run("warn names the path, floor and tunable", func(t *testing.T) {
		t.Parallel()
		got := evaluateDiskHeadroom("/ws", 40*gib, 460*gib, defaultDiskFailGiB, defaultDiskWarnGiB)
		want := "measured the filesystem holding /ws; the check warns below 46 GiB free (set LOOM_DISK_WARN_GIB to change it)"
		if got.Detail != want {
			t.Errorf("Detail = %q, want %q", got.Detail, want)
		}
	})

	t.Run("fail names the fail floor and its tunable", func(t *testing.T) {
		t.Parallel()
		got := evaluateDiskHeadroom("/ws", 11*gib, 460*gib, defaultDiskFailGiB, defaultDiskWarnGiB)
		want := "measured the filesystem holding /ws; the check fails below 23 GiB free (set LOOM_DISK_FAIL_GIB to change it)"
		if got.Detail != want {
			t.Errorf("Detail = %q, want %q", got.Detail, want)
		}
	})
}

func TestDiskThresholdGiB(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		val  string
		want uint64
	}{
		{name: "unset uses the default", want: defaultDiskFailGiB},
		{name: "valid value is honored", set: true, val: "999", want: 999},
		{name: "whitespace is tolerated", set: true, val: " 42 ", want: 42},
		{name: "garbage falls back", set: true, val: "not-a-number", want: defaultDiskFailGiB},
		{name: "zero falls back", set: true, val: "0", want: defaultDiskFailGiB},
		{name: "negative falls back", set: true, val: "-5", want: defaultDiskFailGiB},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("LOOM_DISK_FAIL_GIB", tt.val)
			} else {
				t.Setenv("LOOM_DISK_FAIL_GIB", "")
			}
			if got := diskThresholdGiB("LOOM_DISK_FAIL_GIB", defaultDiskFailGiB); got != tt.want {
				t.Errorf("diskThresholdGiB() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDiskCheckTarget(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	tests := []struct {
		name, runtimeDir, want string
	}{
		{name: "workspace root is measured", runtimeDir: "/srv/loom/ws", want: "/srv/loom/ws"},
		{name: "unconfigured workspace falls back to home", runtimeDir: ".", want: home},
		{name: "empty falls back to home", runtimeDir: "", want: home},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskCheckTarget(tt.runtimeDir); got != tt.want {
				t.Errorf("diskCheckTarget(%q) = %q, want %q", tt.runtimeDir, got, tt.want)
			}
		})
	}
}

func TestCheckDiskHeadroomStatfsError(t *testing.T) {
	orig := diskUsage
	t.Cleanup(func() { diskUsage = orig })
	diskUsage = func(string) (uint64, uint64, error) {
		return 0, 0, os.ErrNotExist
	}

	got := checkDiskHeadroom()
	if got.Status != StatusWarn {
		t.Errorf("Status = %v, want warn (an unmeasurable disk is not a full disk)", got.Status)
	}
	if got.Summary != "could not determine free disk space" {
		t.Errorf("Summary = %q", got.Summary)
	}
	if got.Detail == "" {
		t.Error("Detail is empty, want the underlying error")
	}
}

// A platform with no free-space measurement skips the check instead of
// warning on every run.
func TestCheckDiskHeadroomSkipsWhenUnsupported(t *testing.T) {
	orig := diskUsage
	t.Cleanup(func() { diskUsage = orig })
	diskUsage = func(string) (uint64, uint64, error) {
		return 0, 0, errDiskUsageUnsupported
	}

	if got := checkDiskHeadroom(); got != (CheckResult{}) {
		t.Errorf("checkDiskHeadroom() = %+v, want the zero result (skipped)", got)
	}
}

func TestCheckDiskHeadroomUsesSeams(t *testing.T) {
	orig := diskUsage
	t.Cleanup(func() { diskUsage = orig })
	var measured string
	diskUsage = func(path string) (uint64, uint64, error) {
		measured = path
		return 11 * gib, 460 * gib, nil
	}
	t.Setenv("LOOM_DISK_FAIL_GIB", "")
	t.Setenv("LOOM_DISK_WARN_GIB", "")

	got := checkDiskHeadroom()
	if got.Status != StatusFail {
		t.Errorf("Status = %v, want fail", got.Status)
	}
	if got.Summary != "11 GiB free of 460 GiB (2%)" {
		t.Errorf("Summary = %q", got.Summary)
	}
	if measured == "" {
		t.Fatal("diskUsage was not called")
	}
	if !strings.Contains(got.Detail, "measured the filesystem holding "+measured) {
		t.Errorf("Detail = %q, want it to name the measured path %q", got.Detail, measured)
	}
}
