package packaged

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/driver"
)

// Want is what the running binary expects a packaged built-in to carry.
type Want struct {
	SourceDigest string
	Runners      []driver.DriverRunnerSpec
}

// ArtifactStatus is the readiness view of one built-in.
type ArtifactStatus struct {
	Required             bool   `json:"required"`
	Packaged             bool   `json:"packaged"`
	Verified             bool   `json:"verified"`
	Error                string `json:"error"`
	ArtifactDigest       string `json:"artifact_digest"`
	SourceDigest         string `json:"source_digest"`
	ExpectedSourceDigest string `json:"expected_source_digest"`
}

// Report is the readiness view of the packaged lane. Its JSON keys are the
// DEV-V5-31 readiness contract.
type Report struct {
	Root                string                    `json:"root"`
	IndexDigest         string                    `json:"index_digest"`
	ExpectedIndexDigest string                    `json:"expected_index_digest"`
	FlueCommit          string                    `json:"flue_commit"`
	NodeVersion         string                    `json:"node_version"`
	Target              string                    `json:"target"`
	PackagedBuild       bool                      `json:"packaged_build"`
	Desktop             bool                      `json:"desktop"`
	Required            []string                  `json:"required"`
	Artifacts           map[string]ArtifactStatus `json:"artifacts"`
}

// AllRequiredVerified reports whether every RequiredBuiltins entry verified.
func (r Report) AllRequiredVerified() bool {
	for _, name := range RequiredBuiltins {
		if !r.Artifacts[name].Verified {
			return false
		}
	}
	return true
}

// Describe never errors or panics: it fills in whatever it can read (root and
// index fields even when the digest mismatches, so operators see both sides),
// runs Lookup per name, and reports index entries this binary does not know
// under error="unknown built-in".
func Describe(names []string, want map[string]Want) Report {
	report := Report{
		ExpectedIndexDigest: strings.TrimSpace(ExpectedIndexDigest),
		PackagedBuild:       IsPackagedBuild(),
		Desktop:             IsDesktop(),
		Required:            append([]string{}, RequiredBuiltins...),
		Artifacts:           make(map[string]ArtifactStatus, len(names)),
	}
	var idx *Index
	if root, err := Root(); err == nil {
		report.Root = root
		idx = describeIndex(&report, root)
	}
	known := make(map[string]struct{}, len(names))
	for _, name := range names {
		known[name] = struct{}{}
		report.Artifacts[name] = describeArtifact(idx, name, want[name])
	}
	if idx != nil {
		for name, entry := range idx.Builtins {
			if _, ok := known[name]; ok {
				continue
			}
			report.Artifacts[name] = ArtifactStatus{
				Packaged:       true,
				Error:          "unknown built-in",
				ArtifactDigest: entry.ArtifactDigest,
				SourceDigest:   entry.SourceDigest,
			}
		}
	}
	return report
}

// describeArtifact reports one built-in: index-declared digests (when the
// index is readable), then the Lookup verdict layered on top.
func describeArtifact(idx *Index, name string, w Want) ArtifactStatus {
	status := ArtifactStatus{Required: IsRequired(name), ExpectedSourceDigest: w.SourceDigest}
	if idx != nil {
		if entry, ok := idx.Builtins[name]; ok {
			status.Packaged = true
			status.SourceDigest = entry.SourceDigest
			status.ArtifactDigest = entry.ArtifactDigest
		}
	}
	art, err := Lookup(name, w.SourceDigest, w.Runners)
	switch {
	case err == nil && art != nil:
		status.Verified = true
		status.ArtifactDigest = art.ArtifactDigest
		status.SourceDigest = art.SourceDigest
	case err != nil:
		status.Error = err.Error()
		if errors.Is(err, ErrNotPackaged) && FailClosed() {
			// Readiness is an operator surface: a missing artifact on a
			// fail-closed build is a packaging defect, say so.
			status.Error += " — " + FailClosedGuidance
		}
	}
	return status
}

// describeIndex fills the index-derived report fields best-effort and returns
// the parsed index when it is readable (regardless of digest match).
func describeIndex(report *Report, root string) *Index {
	raw, err := os.ReadFile(filepath.Join(root, IndexFileName)) //nolint:gosec // root is the resolved resource root.
	if err != nil {
		return nil
	}
	report.IndexDigest = IndexDigest(raw)
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil
	}
	report.FlueCommit = idx.FlueCommit
	report.NodeVersion = idx.NodeVersion
	report.Target = idx.Target
	return &idx
}

// IsVerificationError reports whether err is (or wraps) a *VerificationError.
func IsVerificationError(err error) bool {
	var verr *VerificationError
	return errors.As(err, &verr)
}
