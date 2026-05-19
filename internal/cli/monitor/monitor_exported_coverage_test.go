package monitor

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestCollectMonitorDataForServerAndRunMonitorNoWatch(t *testing.T) {
	// not parallel: uses os.Chdir and default issue backend globals.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ResetWorkspaceRuntimeDirCache()

	mock := NewMockIssueBackend()
	mock.StatsResult = &backend.StatsData{TotalIssues: 4, OpenIssues: 1, ClosedIssues: 3}
	setDefaultIssueBackend(mock)
	t.Cleanup(func() { resetDefaultIssueBackend() })

	data := CollectMonitorDataForServer("main")
	if data == nil || data.Stats.Total != 4 {
		t.Fatalf("CollectMonitorDataForServer = %+v", data)
	}

	oldNoWatch, oldBranch := monitorNoWatch, monitorBranch
	monitorNoWatch, monitorBranch = true, "main"
	t.Cleanup(func() {
		monitorNoWatch, monitorBranch = oldNoWatch, oldBranch
	})
	stdout, _ := captureMonitorOutput(t, func() {
		runMonitor(nil, nil)
	})
	if !strings.Contains(stdout, "AGENTS") && stdout == "" {
		t.Fatalf("expected dashboard output, got stdout=%q", stdout)
	}
}

func captureMonitorOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	fn()
	_ = outW.Close()
	_ = errW.Close()
	outData, _ := io.ReadAll(outR)
	errData, _ := io.ReadAll(errR)
	_ = outR.Close()
	_ = errR.Close()
	return string(outData), string(errData)
}
