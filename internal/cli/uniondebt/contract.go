package uniondebt

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LocalIntegration is the slice of a repo's integration.yaml entry that the
// sweeper needs: where the union clone lives and which branch inside it is the
// union tip.
type LocalIntegration struct {
	// Branch is the union branch inside Clone (e.g. "local/union").
	Branch string `yaml:"branch"`
	// Clone is an absolute path to the checkout that holds the union branch.
	Clone string `yaml:"clone"`
}

// LabelSet is the label vocabulary the sweep reads and writes. The names are
// the agentic layer's, not the core's: they are rungs of a workspace's own
// pipeline ladder, so a shipped binary must not hardcode them. Every field is
// optional and falls back to the value PUPPET has used since the sweep existed.
type LabelSet struct {
	// Marker is the ledger marker the sweep drains.
	Marker string `yaml:"marker"`
	// Unreachable replaces the marker when no branch exists to merge.
	Unreachable string `yaml:"unreachable"`
	// Debt marks a derived, claimable debt ticket.
	Debt string `yaml:"debt"`
	// DebtOfPrefix + originID is the per-original dedupe label.
	DebtOfPrefix string `yaml:"debt_of_prefix"`
	// Route routes the derived ticket to the role that performs the merge. It
	// is a rung of the workspace's pipeline ladder: rename that rung and this
	// must follow, which is precisely why it is configuration.
	Route string `yaml:"route"`
}

// defaultLabels is the vocabulary in use before any of this was configurable.
// A contract with no labels block must reproduce it byte for byte.
var defaultLabels = LabelSet{
	Marker:       "union-pending",
	Unreachable:  "union-unreachable",
	Debt:         "union-debt",
	DebtOfPrefix: "union-debt-of:",
	Route:        "approved",
}

// withDefaults fills every empty field from defaultLabels, so a contract may
// override one label without restating the other four.
func (l LabelSet) withDefaults() LabelSet {
	if l.Marker == "" {
		l.Marker = defaultLabels.Marker
	}
	if l.Unreachable == "" {
		l.Unreachable = defaultLabels.Unreachable
	}
	if l.Debt == "" {
		l.Debt = defaultLabels.Debt
	}
	if l.DebtOfPrefix == "" {
		l.DebtOfPrefix = defaultLabels.DebtOfPrefix
	}
	if l.Route == "" {
		l.Route = defaultLabels.Route
	}
	return l
}

// contractFile mirrors only the fields we read. Unknown keys are ignored on
// purpose: integration.yaml is large, operator-owned and grows over time, so
// yaml.Decoder.KnownFields(true) would turn every unrelated addition into a
// sweep failure.
type contractFile struct {
	Defaults struct {
		LocalIntegration *LocalIntegration `yaml:"local_integration"`
		Labels           *LabelSet         `yaml:"labels"`
	} `yaml:"defaults"`
	Repos map[string]struct {
		LocalIntegration *LocalIntegration `yaml:"local_integration"`
	} `yaml:"repos"`
}

// Contract is a parsed integration.yaml, reduced to the local-integration
// lookup the sweeper performs.
type Contract struct {
	defaultBranch string
	labels        LabelSet
	repos         map[string]LocalIntegration
}

// LoadContract reads and parses integration.yaml at path.
func LoadContract(path string) (*Contract, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304 — operator-supplied contract path
	if err != nil {
		return nil, fmt.Errorf("read contract %s: %w", path, err)
	}
	var cf contractFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		return nil, fmt.Errorf("parse contract %s: %w", path, err)
	}

	c := &Contract{repos: make(map[string]LocalIntegration, len(cf.Repos))}
	if cf.Defaults.Labels != nil {
		c.labels = *cf.Defaults.Labels
	}
	c.labels = c.labels.withDefaults()
	if cf.Defaults.LocalIntegration != nil {
		c.defaultBranch = cf.Defaults.LocalIntegration.Branch
	}
	for id, entry := range cf.Repos {
		// A repo with no local_integration block (local-stack today) takes part
		// in no union at all — record nothing so Lookup reports it as missing
		// rather than handing back a clone-less entry.
		if entry.LocalIntegration == nil {
			continue
		}
		li := *entry.LocalIntegration
		if li.Branch == "" {
			li.Branch = c.defaultBranch
		}
		c.repos[id] = li
	}
	return c, nil
}

// Lookup returns the local-integration settings for a repo id. The second
// result is false when the repo is absent from the contract, or is present but
// declares no local_integration, or declares one with no clone path — in every
// one of those cases there is nothing local to probe.
func (c *Contract) Lookup(repoID string) (LocalIntegration, bool) {
	li, ok := c.repos[repoID]
	if !ok || li.Clone == "" {
		return LocalIntegration{}, false
	}
	return li, true
}

// Labels returns the configured label vocabulary, with every unset field —
// and a nil receiver, which a caller holding no contract at all presents —
// falling back to the defaults.
func (c *Contract) Labels() LabelSet {
	if c == nil {
		return defaultLabels
	}
	return c.labels.withDefaults()
}
