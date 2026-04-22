//go:build parity

package paritytest

import (
	"encoding/json"
	"fmt"
	"os"
)

// Fixture is a single-JSON-file description of a parity scenario. The shape
// mirrors fleet-db's test/fixtures/testutil.Fixture so fixtures can move
// between the two harnesses with minimal rewriting.
//
// A fixture contains an ordered list of steps; each step invokes one
// IssueBackend method and produces a response that DualRunner diffs between
// the two backends. Variable capture + substitution (${issue_id} syntax) is
// supported across steps so a later step can reference the ID produced by an
// earlier issue.create.
type Fixture struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Steps       []Step   `json:"steps"`
}

// Step is one op within a fixture. `Method` is the JSON-RPC-style operation
// name used as both the dispatcher key and the label on emitted DiffEntry
// rows. `Params` is the raw JSON arguments — each backend's dispatcher
// unmarshals into its own Opts/Params struct.
//
// Expect is carried forward from fleet-db's schema for cross-compat but is
// unused in dual-run mode (both backends produce responses and DualRunner
// diffs them directly — no expected-value assertion is needed).
type Step struct {
	ID          string          `json:"id"`
	Description string          `json:"description,omitempty"`
	Method      string          `json:"method"`
	Params      json.RawMessage `json:"params"`
	Expect      json.RawMessage `json:"expect,omitempty"`
}

// LoadFixture reads + parses a single fixture JSON file.
func LoadFixture(path string) (*Fixture, error) {
	data, err := os.ReadFile(path) // #nosec G304 — test harness reads fixture paths under the package testdata dir
	if err != nil {
		return nil, fmt.Errorf("read fixture %q: %w", path, err)
	}
	var fx Fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return nil, fmt.Errorf("parse fixture %q: %w", path, err)
	}
	if fx.ID == "" {
		return nil, fmt.Errorf("fixture %q: missing id", path)
	}
	if len(fx.Steps) == 0 {
		return nil, fmt.Errorf("fixture %q: no steps", path)
	}
	return &fx, nil
}
