package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			// The real 2026-08-31 10:49 low point.
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
			got := evaluateDiskHeadroom(tt.free, tt.total, janitorReport{}, false, failGiB, warnGiB)
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

	rep := janitorReport{
		TS: "2026-08-31T10:49:00Z",
		Consumers: map[string]uint64{
			"gocache":        34 * gib,
			"gomodcache":     12 * gib,
			"pm2_logs":       2 * gib,
			"loom_worktrees": 8 * gib,
		},
		Swept: janitorSwept{GocacheBytes: 20 * gib, PM2Bytes: 1 * gib},
	}

	t.Run("pass has no detail", func(t *testing.T) {
		t.Parallel()
		got := evaluateDiskHeadroom(300*gib, 460*gib, rep, true,
			defaultDiskFailGiB, defaultDiskWarnGiB)
		if got.Detail != "" {
			t.Errorf("Detail = %q, want empty on pass", got.Detail)
		}
	})

	t.Run("names the largest consumer", func(t *testing.T) {
		t.Parallel()
		got := evaluateDiskHeadroom(11*gib, 460*gib, rep, true,
			defaultDiskFailGiB, defaultDiskWarnGiB)
		if !strings.Contains(got.Detail, "largest consumer: go build cache 34 GiB") {
			t.Errorf("Detail = %q, want the go build cache named", got.Detail)
		}
		if !strings.Contains(got.Detail, "janitor last swept 2026-08-31T10:49:00Z, freed 21 GiB") {
			t.Errorf("Detail = %q, want the sweep line", got.Detail)
		}
		if strings.Contains(got.Detail, "shortfall") {
			t.Errorf("Detail = %q, want no shortfall line", got.Detail)
		}
	})

	t.Run("reports a shortfall", func(t *testing.T) {
		t.Parallel()
		short := rep
		short.ShortfallBytes = 7 * gib
		got := evaluateDiskHeadroom(11*gib, 460*gib, short, true,
			defaultDiskFailGiB, defaultDiskWarnGiB)
		if !strings.Contains(got.Detail,
			"shortfall: janitor could not reach its low-water mark by 7 GiB") {
			t.Errorf("Detail = %q, want the shortfall line", got.Detail)
		}
	})

	t.Run("absent sidecar is called out on a warn", func(t *testing.T) {
		t.Parallel()
		got := evaluateDiskHeadroom(40*gib, 460*gib, janitorReport{}, false,
			defaultDiskFailGiB, defaultDiskWarnGiB)
		if got.Status != StatusWarn {
			t.Fatalf("Status = %v, want warn", got.Status)
		}
		if !strings.Contains(got.Detail,
			"janitor status stale/absent — the cache bound may not be running") {
			t.Errorf("Detail = %q, want the stale/absent sentence", got.Detail)
		}
	})

	t.Run("unknown consumer key falls back to the raw key", func(t *testing.T) {
		t.Parallel()
		odd := janitorReport{Consumers: map[string]uint64{"mystery": 99 * gib}}
		got := evaluateDiskHeadroom(11*gib, 460*gib, odd, true,
			defaultDiskFailGiB, defaultDiskWarnGiB)
		if !strings.Contains(got.Detail, "largest consumer: mystery 99 GiB") {
			t.Errorf("Detail = %q, want the raw key named", got.Detail)
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

func TestDefaultJanitorStatus(t *testing.T) {
	fresh := `{"ts":"` + time.Now().UTC().Format(time.RFC3339) + `",` +
		`"free_bytes":1,"total_bytes":2,"consumers":{"gocache":3},` +
		`"shortfall_bytes":4,"errors":0,"dry_run":false}`

	t.Run("valid file is reported", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "status.json")
		if err := os.WriteFile(path, []byte(fresh), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_JANITOR_STATUS", path)

		rep, ok := defaultJanitorStatus()
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if rep.Consumers["gocache"] != 3 || rep.ShortfallBytes != 4 {
			t.Errorf("report = %+v, want the file's values", rep)
		}
	})

	t.Run("missing file degrades silently", func(t *testing.T) {
		t.Setenv("LOOM_JANITOR_STATUS", filepath.Join(t.TempDir(), "nope.json"))
		if rep, ok := defaultJanitorStatus(); ok || rep.TS != "" {
			t.Errorf("got (%+v, %v), want (zero, false)", rep, ok)
		}
	})

	t.Run("malformed JSON degrades silently", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "status.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_JANITOR_STATUS", path)
		if rep, ok := defaultJanitorStatus(); ok || rep.TS != "" {
			t.Errorf("got (%+v, %v), want (zero, false)", rep, ok)
		}
	})

	t.Run("stale mtime degrades silently", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "status.json")
		if err := os.WriteFile(path, []byte(fresh), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-3 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_JANITOR_STATUS", path)
		if rep, ok := defaultJanitorStatus(); ok || rep.TS != "" {
			t.Errorf("got (%+v, %v), want (zero, false)", rep, ok)
		}
	})

	t.Run("stale timestamp degrades silently", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "status.json")
		stale := `{"ts":"` + time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339) + `"}`
		if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_JANITOR_STATUS", path)
		if rep, ok := defaultJanitorStatus(); ok || rep.TS != "" {
			t.Errorf("got (%+v, %v), want (zero, false)", rep, ok)
		}
	})
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

func TestCheckDiskHeadroomUsesSeams(t *testing.T) {
	origDisk, origJanitor := diskUsage, janitorStatus
	t.Cleanup(func() { diskUsage, janitorStatus = origDisk, origJanitor })
	diskUsage = func(string) (uint64, uint64, error) { return 11 * gib, 460 * gib, nil }
	janitorStatus = func() (janitorReport, bool) { return janitorReport{}, false }
	t.Setenv("LOOM_DISK_FAIL_GIB", "")
	t.Setenv("LOOM_DISK_WARN_GIB", "")

	got := checkDiskHeadroom()
	if got.Status != StatusFail {
		t.Errorf("Status = %v, want fail", got.Status)
	}
	if got.Summary != "11 GiB free of 460 GiB (2%)" {
		t.Errorf("Summary = %q", got.Summary)
	}
	if !strings.Contains(got.Detail, "janitor status stale/absent") {
		t.Errorf("Detail = %q, want the stale/absent sentence", got.Detail)
	}
}
