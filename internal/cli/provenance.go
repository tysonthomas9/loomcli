package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Build provenance (D-48) and the PATH-skew tripwire (D-44).
//
// `loom --version` printed "loom version dev (<commit>)" and nothing else:
// Version was never stamped, and there was no way to ask which ref or which
// PRs a deployed binary came from. Meanwhile the deploy tooling already knows
// all of it — it checks out a ref at a commit — so the information existed
// everywhere except in the artifact that gets run.
//
// The skew half is the other end of the same problem: a stale `loom` on PATH
// behaves like the current one until it doesn't, and the failure surfaces as
// a mysterious behavior difference rather than "you are running last week's
// binary". A deployed build records itself; every later invocation can compare.

// Provenance vars, all settable with -ldflags -X. Empty/unknown is a legal
// state and is reported as such rather than guessed at: a binary built by
// `go build` with no stamping must not claim a ref it does not have.
var (
	// Ref is the git ref the build came from (branch or tag).
	Ref = ""
	// SourcePRs is a comma-separated list of PR URLs or numbers the deploy
	// was assembled from. The deploy tool knows this; nothing else does.
	SourcePRs = ""
	// BuildTime is an RFC3339 timestamp.
	BuildTime = ""
)

// VersionInfo is the structured form of `loom version --json`.
type VersionInfo struct {
	Version   string   `json:"version"`
	Commit    string   `json:"commit"`
	Ref       string   `json:"ref,omitempty"`
	SourcePRs []string `json:"source_prs,omitempty"`
	BuildTime string   `json:"build_time,omitempty"`
	Path      string   `json:"path,omitempty"`

	// Deployed describes the build recorded as deployed on this host, when a
	// record exists. Nil when nothing has been recorded — which is different
	// from "no skew".
	Deployed *DeployedBuild `json:"deployed,omitempty"`
	// Skew is set when a deploy record exists and disagrees with this binary.
	Skew string `json:"skew,omitempty"`
}

// DeployedBuild is the on-disk record a deploy writes about itself.
type DeployedBuild struct {
	Version    string   `json:"version"`
	Commit     string   `json:"commit"`
	Ref        string   `json:"ref,omitempty"`
	SourcePRs  []string `json:"source_prs,omitempty"`
	BuildTime  string   `json:"build_time,omitempty"`
	RecordedAt string   `json:"recorded_at"`
	// Path is where the deployed binary was installed, so the tripwire can
	// say which binary the operator is missing.
	Path string `json:"path,omitempty"`
}

// deployRecordName is the file a deploy stamps under the loom directory.
const deployRecordName = "deployed-version.json"

// CurrentVersionInfo assembles this binary's provenance and compares it with
// the host's deploy record.
func CurrentVersionInfo() VersionInfo {
	info := VersionInfo{
		Version:   Version,
		Commit:    Build,
		Ref:       Ref,
		SourcePRs: splitSourcePRs(SourcePRs),
		BuildTime: BuildTime,
	}
	if exe, err := os.Executable(); err == nil {
		info.Path = exe
	}
	if rec, err := ReadDeployRecord(); err == nil && rec != nil {
		info.Deployed = rec
		info.Skew = describeSkew(info, *rec)
	}
	return info
}

// String renders the human one-liner, preserving the historical shape and
// appending only what is actually known.
func (v VersionInfo) String() string {
	s := fmt.Sprintf("loom version %s (%s)", v.Version, v.Commit)
	if v.Ref != "" {
		s += " ref " + v.Ref
	}
	if len(v.SourcePRs) > 0 {
		s += " from " + strings.Join(v.SourcePRs, ", ")
	}
	if v.BuildTime != "" {
		s += " built " + v.BuildTime
	}
	return s
}

// describeSkew returns a human explanation when the running binary is not the
// recorded deployed build, else "". Commit is the identity: version strings
// and refs can repeat across builds, a commit cannot.
func describeSkew(running VersionInfo, deployed DeployedBuild) string {
	if deployed.Commit == "" || running.Commit == "" {
		// Nothing to compare honestly. An unstamped binary is a real
		// possibility (plain `go build`), and claiming skew from missing
		// data would train operators to ignore the warning.
		return ""
	}
	if deployed.Commit == running.Commit {
		return ""
	}
	msg := fmt.Sprintf("the loom on PATH is commit %s, but the deployed build is %s", running.Commit, deployed.Commit)
	if deployed.Ref != "" {
		msg += " (ref " + deployed.Ref + ")"
	}
	if deployed.Path != "" && running.Path != "" && deployed.Path != running.Path {
		msg += fmt.Sprintf("; deployed binary lives at %s, this one at %s", deployed.Path, running.Path)
	}
	return msg
}

// splitSourcePRs parses the comma-separated stamp, dropping blanks.
func splitSourcePRs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// deployRecordPath returns the record location under the loom directory.
func deployRecordPath() (string, error) {
	dir, err := loomDirForProvenance()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, deployRecordName), nil
}

// loomDirForProvenance resolves ~/.loom (honoring LOOM_DIR), kept local so
// this file does not drag bootstrap into every version print.
func loomDirForProvenance() (string, error) {
	if d := strings.TrimSpace(os.Getenv("LOOM_DIR")); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".loom"), nil
}

// ReadDeployRecord loads the host's deploy record. Returns (nil, nil) when no
// record exists — an un-recorded host is normal, not an error.
func ReadDeployRecord() (*DeployedBuild, error) {
	path, err := deployRecordPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec DeployedBuild
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("deploy record %s: %w", path, err)
	}
	return &rec, nil
}

// WriteDeployRecord records THIS binary as the deployed build. The deployer
// calls `loom version --record` after installing, so the record describes the
// binary that was actually put in place rather than whatever ran last.
func WriteDeployRecord(now string) (string, error) {
	dir, err := loomDirForProvenance()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	rec := DeployedBuild{
		Version:    Version,
		Commit:     Build,
		Ref:        Ref,
		SourcePRs:  splitSourcePRs(SourcePRs),
		BuildTime:  BuildTime,
		RecordedAt: now,
	}
	if exe, exeErr := os.Executable(); exeErr == nil {
		rec.Path = exe
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, deployRecordName)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// VersionSkewWarning returns the operator-facing warning for a stale PATH
// binary, or "" when there is nothing to say. Cheap enough to call from a
// session preamble: one small file read, no exec.
func VersionSkewWarning() string {
	info := CurrentVersionInfo()
	if info.Skew == "" {
		return ""
	}
	warn := "WARNING: loom version skew — " + info.Skew + "."
	if p := pathLoomLocation(); p != "" {
		warn += " PATH resolves loom to " + p + "."
	}
	return warn + " Re-run the deployer (or `go install ./cmd/loom`) before trusting behavior differences."
}

// pathLoomLocation resolves `loom` on PATH for the warning text. Best-effort:
// an unresolvable name just omits the detail.
func pathLoomLocation() string {
	p, err := exec.LookPath("loom")
	if err != nil {
		return ""
	}
	return p
}
