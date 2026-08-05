package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stampBuild sets the ldflags-backed vars for one test and restores them.
func stampBuild(t *testing.T, version, commit, ref, prs, built string) {
	t.Helper()
	ov, oc, or, op, ob := Version, Build, Ref, SourcePRs, BuildTime
	Version, Build, Ref, SourcePRs, BuildTime = version, commit, ref, prs, built
	t.Cleanup(func() { Version, Build, Ref, SourcePRs, BuildTime = ov, oc, or, op, ob })
}

func TestCurrentVersionInfo_ReportsWhatWasStamped(t *testing.T) {
	t.Setenv("LOOM_DIR", t.TempDir())
	stampBuild(t, "1.2.3", "abc1234", "v5", "https://github.com/o/r/pull/1, https://github.com/o/r/pull/2", "2026-08-05T12:00:00Z")

	info := CurrentVersionInfo()
	if info.Version != "1.2.3" || info.Commit != "abc1234" || info.Ref != "v5" {
		t.Fatalf("info = %+v", info)
	}
	if len(info.SourcePRs) != 2 || info.SourcePRs[0] != "https://github.com/o/r/pull/1" {
		t.Fatalf("SourcePRs = %v, want both PR URLs trimmed", info.SourcePRs)
	}
	s := info.String()
	for _, want := range []string{"1.2.3", "abc1234", "ref v5", "pull/1", "built 2026-08-05T12:00:00Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, missing %q", s, want)
		}
	}
}

// An unstamped build must say nothing rather than invent a ref: the whole
// point of the field is that a deployed binary can be trusted about it.
func TestCurrentVersionInfo_UnstampedFieldsStayAbsent(t *testing.T) {
	t.Setenv("LOOM_DIR", t.TempDir())
	stampBuild(t, "dev", "unknown", "", "", "")

	info := CurrentVersionInfo()
	if info.Ref != "" || info.SourcePRs != nil || info.BuildTime != "" {
		t.Fatalf("unstamped info should be empty, got %+v", info)
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"\"ref\"", "\"source_prs\"", "\"build_time\""} {
		if strings.Contains(string(data), absent) {
			t.Errorf("JSON %s should omit %s", data, absent)
		}
	}
}

func TestDeployRecord_RoundTripAndSkew(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_DIR", dir)

	// Nothing recorded yet: absence is not an error and not skew.
	rec, err := ReadDeployRecord()
	if err != nil || rec != nil {
		t.Fatalf("ReadDeployRecord on a fresh host = (%v, %v), want (nil, nil)", rec, err)
	}
	stampBuild(t, "1.0.0", "cafe123", "v5", "", "")
	if got := CurrentVersionInfo().Skew; got != "" {
		t.Fatalf("skew with no record = %q, want empty", got)
	}

	path, err := WriteDeployRecord("2026-08-05T12:00:00Z")
	if err != nil {
		t.Fatalf("WriteDeployRecord: %v", err)
	}
	if path != filepath.Join(dir, deployRecordName) {
		t.Fatalf("record path = %q", path)
	}

	// Same binary as recorded: no skew, and the record is reported.
	info := CurrentVersionInfo()
	if info.Skew != "" {
		t.Fatalf("skew against its own record = %q", info.Skew)
	}
	if info.Deployed == nil || info.Deployed.Commit != "cafe123" || info.Deployed.RecordedAt != "2026-08-05T12:00:00Z" {
		t.Fatalf("Deployed = %+v", info.Deployed)
	}

	// A different commit is skew, and the message names both sides.
	stampBuild(t, "1.0.0", "deadbee", "feature/x", "", "")
	info = CurrentVersionInfo()
	if info.Skew == "" {
		t.Fatal("a different commit must report skew")
	}
	for _, want := range []string{"deadbee", "cafe123", "v5"} {
		if !strings.Contains(info.Skew, want) {
			t.Errorf("skew %q should name %q", info.Skew, want)
		}
	}
	if w := VersionSkewWarning(); !strings.Contains(w, "WARNING") || !strings.Contains(w, "deadbee") {
		t.Errorf("VersionSkewWarning() = %q", w)
	}
}

// Missing commit data on either side means "cannot tell", not "skew" — a
// warning that fires on a plain `go build` teaches operators to ignore it.
func TestSkew_UnknownCommitsDoNotWarn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_DIR", dir)

	stampBuild(t, "dev", "", "", "", "")
	if _, err := WriteDeployRecord("2026-08-05T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	stampBuild(t, "dev", "abc1234", "", "", "")
	if got := CurrentVersionInfo().Skew; got != "" {
		t.Fatalf("skew against an unstamped record = %q, want empty", got)
	}
	if got := VersionSkewWarning(); got != "" {
		t.Fatalf("VersionSkewWarning = %q, want empty", got)
	}
}

func TestReadDeployRecord_MalformedIsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, deployRecordName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDeployRecord(); err == nil {
		t.Fatal("a malformed record must error rather than read as absent")
	}
	// And it must not crash the version path.
	if got := CurrentVersionInfo(); got.Deployed != nil {
		t.Fatalf("Deployed = %+v, want nil on a malformed record", got.Deployed)
	}
}
