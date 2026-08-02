package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoWorkFile_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	report := &NoWorkReport{
		Reason:     "no design changed since my last critique",
		TaskID:     "loom-42",
		ReportedAt: time.Now(),
		ReportedBy: "critic",
	}
	if err := WriteNoWorkFile(tmpDir, report); err != nil {
		t.Fatalf("WriteNoWorkFile failed: %v", err)
	}

	got, err := ReadNoWorkFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadNoWorkFile failed: %v", err)
	}
	if got == nil {
		t.Fatal("ReadNoWorkFile: got nil, want a report")
	}
	if got.Reason != report.Reason || got.TaskID != report.TaskID || got.ReportedBy != report.ReportedBy {
		t.Errorf("ReadNoWorkFile: got %+v, want %+v", got, report)
	}

	if !IsNoWorkReported(tmpDir) {
		t.Error("IsNoWorkReported: got false, want true")
	}

	if err := ClearNoWorkFile(tmpDir); err != nil {
		t.Fatalf("ClearNoWorkFile failed: %v", err)
	}
	if IsNoWorkReported(tmpDir) {
		t.Error("IsNoWorkReported after clear: got true, want false")
	}

	// tmp file must not survive a successful write
	if _, err := os.Stat(filepath.Join(tmpDir, NoWorkFileName+".tmp")); !os.IsNotExist(err) {
		t.Errorf("expected no .tmp file to remain, stat err = %v", err)
	}
}

func TestReadNoWorkFile_Absent(t *testing.T) {
	tmpDir := t.TempDir()

	got, err := ReadNoWorkFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadNoWorkFile on absent file: unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("ReadNoWorkFile on absent file: got %+v, want nil", got)
	}
}

func TestClearNoWorkFile_Absent(t *testing.T) {
	tmpDir := t.TempDir()
	if err := ClearNoWorkFile(tmpDir); err != nil {
		t.Errorf("ClearNoWorkFile on absent file: unexpected error %v", err)
	}
}

func TestReadNoWorkFile_MalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, NoWorkFileName), []byte("not json"), 0600); err != nil {
		t.Fatalf("failed to write malformed marker: %v", err)
	}

	got, err := ReadNoWorkFile(tmpDir)
	if err == nil {
		t.Fatal("ReadNoWorkFile on malformed JSON: expected error, got nil")
	}
	if got != nil {
		t.Errorf("ReadNoWorkFile on malformed JSON: got %+v, want nil", got)
	}
}

func TestIsNoWorkReported_Absent(t *testing.T) {
	tmpDir := t.TempDir()
	if IsNoWorkReported(tmpDir) {
		t.Error("IsNoWorkReported on empty dir: got true, want false")
	}
}

func TestNoWorkReportedAfter_Fresh(t *testing.T) {
	tmpDir := t.TempDir()
	since := time.Now()

	if err := WriteNoWorkFile(tmpDir, &NoWorkReport{Reason: "fresh"}); err != nil {
		t.Fatalf("WriteNoWorkFile failed: %v", err)
	}

	report, ok := NoWorkReportedAfter(tmpDir, since)
	if !ok {
		t.Fatal("NoWorkReportedAfter: got ok=false for a fresh marker, want true")
	}
	if report == nil || report.Reason != "fresh" {
		t.Errorf("NoWorkReportedAfter: got %+v, want Reason=fresh", report)
	}
}

func TestNoWorkReportedAfter_StaleRejected(t *testing.T) {
	tmpDir := t.TempDir()

	if err := WriteNoWorkFile(tmpDir, &NoWorkReport{Reason: "stale"}); err != nil {
		t.Fatalf("WriteNoWorkFile failed: %v", err)
	}

	// since is after the marker's write time -> stale.
	since := time.Now().Add(time.Hour)
	report, ok := NoWorkReportedAfter(tmpDir, since)
	if ok {
		t.Errorf("NoWorkReportedAfter: got ok=true for a stale marker (report=%+v), want false", report)
	}
	if report != nil {
		t.Errorf("NoWorkReportedAfter stale: got %+v, want nil", report)
	}
}

func TestNoWorkReportedAfter_Absent(t *testing.T) {
	tmpDir := t.TempDir()
	report, ok := NoWorkReportedAfter(tmpDir, time.Now())
	if ok || report != nil {
		t.Errorf("NoWorkReportedAfter on absent marker: got (%+v, %v), want (nil, false)", report, ok)
	}
}

func TestNoWorkReportedAfter_MalformedJSON_StillReported(t *testing.T) {
	tmpDir := t.TempDir()
	since := time.Now()
	if err := os.WriteFile(filepath.Join(tmpDir, NoWorkFileName), []byte("not json"), 0600); err != nil {
		t.Fatalf("failed to write malformed marker: %v", err)
	}

	report, ok := NoWorkReportedAfter(tmpDir, since)
	if !ok {
		t.Fatal("NoWorkReportedAfter on malformed-but-fresh marker: got ok=false, want true (reported, no reason)")
	}
	if report == nil {
		t.Fatal("NoWorkReportedAfter on malformed marker: got nil report, want non-nil with empty Reason")
	}
	if report.Reason != "" {
		t.Errorf("NoWorkReportedAfter on malformed marker: got Reason=%q, want empty", report.Reason)
	}
}
