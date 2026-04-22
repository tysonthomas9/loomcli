//go:build parity

package paritytest

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// DualRunner executes a fixture's op sequence against both a beads
// IssueBackend and a fleet IssueBackend, then diffs the responses.
//
// Construction is deferred to subsequent commits: runner.go will gain a
// New(opts) constructor that spawns a bd daemon subprocess in a t.TempDir()
// (mirroring fleet-db's BeadsCaller pattern) and a fleet-db subprocess
// listening on a random port. Both backends are real implementations from
// internal/backend/beads and internal/backend/fleet.
//
// This skeleton lets the package build under -tags parity and lets fixture
// code compile against the public type. It does not yet execute fixtures.
type DualRunner struct {
	beads  backend.IssueBackend
	fleet  backend.IssueBackend
	report *Report
}

// New constructs a DualRunner with two pre-built backends and a report sink.
// Lifecycle (subprocess spawn/teardown) is the caller's responsibility.
func New(beadsBackend, fleetBackend backend.IssueBackend, report *Report) *DualRunner {
	return &DualRunner{
		beads:  beadsBackend,
		fleet:  fleetBackend,
		report: report,
	}
}

// RunFixture executes the named fixture's op sequence against both backends
// and returns the field-level diffs. Implementation pending — currently
// returns a no-op error so callers compile.
func (r *DualRunner) RunFixture(ctx context.Context, fixtureID string) ([]DiffEntry, error) {
	return nil, fmt.Errorf("paritytest.DualRunner.RunFixture: not yet implemented (fixture=%s)", fixtureID)
}
