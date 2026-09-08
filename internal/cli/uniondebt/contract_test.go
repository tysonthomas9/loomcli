package uniondebt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const contractFixture = `
defaults:
  target_branch: main
  merge_mode: ff-only
  local_integration:
    branch: local/union
    push: never
repos:
  loomcli:
    target_branch: v5
    gate_command: make check
    local_integration:
      branch: local/union
      base: origin/v5
      clone: /clones/loomcli
      worktree: /union/loomcli
      deployed: true
  meta-harness:
    local_integration:
      clone: /clones/meta-harness
  local-stack:
    target_branch: main
`

func writeContract(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "integration.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// withLabels splices a labels block into the fixture's defaults section, which
// is where the sweep reads it from.
func withLabels(block string) string {
	return strings.Replace(contractFixture, "defaults:\n", "defaults:\n"+block, 1)
}

func TestLoadContract(t *testing.T) {
	c, err := LoadContract(writeContract(t, contractFixture))
	if err != nil {
		t.Fatalf("LoadContract: %v", err)
	}

	li, ok := c.Lookup("loomcli")
	if !ok {
		t.Fatal("loomcli not found in contract")
	}
	if li.Clone != "/clones/loomcli" || li.Branch != "local/union" {
		t.Errorf("loomcli = %+v, want clone=/clones/loomcli branch=local/union", li)
	}

	// meta-harness declares no branch of its own and must inherit the default.
	li, ok = c.Lookup("meta-harness")
	if !ok {
		t.Fatal("meta-harness not found in contract")
	}
	if li.Branch != "local/union" {
		t.Errorf("meta-harness branch = %q, want the inherited local/union", li.Branch)
	}

	// A repo with no local_integration takes part in no union.
	if _, ok := c.Lookup("local-stack"); ok {
		t.Error("local-stack has no local_integration; Lookup should report it missing")
	}
	if _, ok := c.Lookup("nope"); ok {
		t.Error("unknown repo should not be found")
	}
}

func TestLoadContract_UnknownKeysTolerated(t *testing.T) {
	// integration.yaml is operator-owned and grows; an unrelated new key must
	// never fail the sweep.
	body := contractFixture + `
    some_future_key:
      nested: true
`
	if _, err := LoadContract(writeContract(t, body)); err != nil {
		t.Fatalf("unknown keys should be tolerated, got: %v", err)
	}
}

func TestLoadContract_Errors(t *testing.T) {
	if _, err := LoadContract(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("missing file should error")
	}
	if _, err := LoadContract(writeContract(t, "repos: [oops\n")); err == nil {
		t.Error("malformed yaml should error")
	}
}

func TestLookup_NoClone(t *testing.T) {
	c, err := LoadContract(writeContract(t, "repos:\n  x:\n    local_integration:\n      branch: local/union\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup("x"); ok {
		t.Error("a local_integration with no clone path has nothing to probe")
	}
}

func TestLabels_DefaultsWhenUnconfigured(t *testing.T) {
	c, err := LoadContract(writeContract(t, contractFixture))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Labels(); got != defaultLabels {
		t.Errorf("Labels() = %+v, want the defaults %+v", got, defaultLabels)
	}
	// Named explicitly: an unconfigured contract must still yield the label the
	// superseded path stamps.
	if got := c.Labels().Superseded; got != "union-superseded" {
		t.Errorf("Superseded = %q, want union-superseded", got)
	}
	// A caller holding no contract at all must still get a usable vocabulary.
	var nilContract *Contract
	if got := nilContract.Labels(); got != defaultLabels {
		t.Errorf("nil Contract Labels() = %+v, want the defaults", got)
	}
}

// TestLabels_PartialBlockFallsBackPerField is the exhaustiveness test for the
// vocabulary: every field is configured ALONE and the rest must default. A new
// field added to LabelSet without a withDefaults() clause fails here — and it
// must, because retire() would otherwise write an empty replacement label,
// silently removing the marker and stamping nothing.
func TestLabels_PartialBlockFallsBackPerField(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  func(LabelSet) LabelSet
	}{
		{"marker", "    marker: merge-pending\n", func(l LabelSet) LabelSet { l.Marker = "merge-pending"; return l }},
		{"unreachable", "    unreachable: merge-unreachable\n", func(l LabelSet) LabelSet { l.Unreachable = "merge-unreachable"; return l }},
		{"superseded", "    superseded: merge-superseded\n", func(l LabelSet) LabelSet { l.Superseded = "merge-superseded"; return l }},
		{"debt", "    debt: merge-debt\n", func(l LabelSet) LabelSet { l.Debt = "merge-debt"; return l }},
		{"debt_of_prefix", "    debt_of_prefix: \"merge-debt-for/\"\n", func(l LabelSet) LabelSet { l.DebtOfPrefix = "merge-debt-for/"; return l }},
		{"route", "    route: integrate\n", func(l LabelSet) LabelSet { l.Route = "integrate"; return l }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := LoadContract(writeContract(t, withLabels("  labels:\n"+tc.block)))
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want(defaultLabels)
			if got := c.Labels(); got != want {
				t.Errorf("Labels() = %+v, want only %s overridden: %+v", got, tc.name, want)
			}
		})
	}
}

// TestLabels_ExplicitSupersededOverridesDefault pins the other direction: an
// explicitly configured value must win over the default.
func TestLabels_ExplicitSupersededOverridesDefault(t *testing.T) {
	c, err := LoadContract(writeContract(t, withLabels("  labels:\n    superseded: merge-superseded\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Labels().Superseded; got != "merge-superseded" {
		t.Errorf("Superseded = %q, want the configured merge-superseded", got)
	}
}
