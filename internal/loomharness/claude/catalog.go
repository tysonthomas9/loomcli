// The fallback effort choices and context windows follow T3 Code
// apps/server/src/provider/Layers/ClaudeProvider.ts (CLAUDE_MODEL_CATALOG) at
// commit 2daff8c25, limited to the values `claude --effort` takes.
//
// Copyright (c) 2026 T3 Tools Inc. Licensed under the MIT License; the full
// notice is in THIRD_PARTY_NOTICES.md.

package claude

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// modelsJSON is the Anthropic part of models.dev, trimmed by
// scripts/refresh-claude-models.sh (make refresh-claude-models). Loom never
// fetches it at runtime.
//
//go:embed models.json
var modelsJSON []byte

// snapshotModel is one models.json entry.
type snapshotModel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Family      string   `json:"family"`
	ReleaseDate string   `json:"release_date"`
	Status      string   `json:"status"`
	Context     int64    `json:"context"`
	Input       []string `json:"input"`
	Effort      []string `json:"effort"`
}

// aliases are the `claude --model` aliases, listed first so saved choices
// keep working; each takes the newest snapshot model of its family.
var aliases = []struct{ id, name, family string }{
	{"opus", "Opus", "claude-opus"}, {"sonnet", "Sonnet", "claude-sonnet"}, {"haiku", "Haiku", "claude-haiku"}}

var effortLabels = map[string]string{"low": "Low", "medium": "Medium", "high": "High", "xhigh": "Extra High", "max": "Max"}

// effortOption is the --effort option over the given levels; the CLI
// defaults to high.
func effortOption(levels []string) []loomharness.OptionDescriptor {
	if len(levels) == 0 {
		return nil
	}
	o := loomharness.OptionDescriptor{ID: loomharness.OptionEffort, Label: "Reasoning", Type: loomharness.OptionSelect, Current: levels[0]}
	for _, l := range levels {
		c := loomharness.OptionChoice{ID: l, Label: cmp.Or(effortLabels[l], l)}
		if l == "high" {
			c.Default, o.Current = true, l
		}
		o.Choices = append(o.Choices, c)
	}
	return []loomharness.OptionDescriptor{o}
}

func model(id, name string, ctx int64, input, effort []string) loomharness.Model {
	return loomharness.Model{ID: id, Name: name, Provider: "anthropic", ProviderName: "Anthropic",
		ContextLimit: ctx, Input: input, Options: effortOption(effort)}
}

// fallback is the hand-written list, used when the snapshot can't be parsed.
func fallback() []loomharness.Model {
	in := []string{"text", "image", "pdf"}
	all := []string{"low", "medium", "high", "xhigh", "max"}
	return []loomharness.Model{model("opus", "Opus", 1_000_000, in, all), model("sonnet", "Sonnet", 200_000, in, all),
		model("haiku", "Haiku", 200_000, in, nil)}
}

// catalog builds the model list from a snapshot: the aliases, then every
// non-deprecated model newest first. It returns the fallback when the
// snapshot is unreadable or lacks a family an alias needs.
func catalog(data []byte) []loomharness.Model {
	var byID map[string]snapshotModel
	if json.Unmarshal(data, &byID) != nil {
		return fallback()
	}
	var ms []snapshotModel
	for id, m := range byID {
		if m.Status != "deprecated" && strings.HasPrefix(id, "claude-") {
			m.ID = id
			ms = append(ms, m)
		}
	}
	slices.SortFunc(ms, func(a, b snapshotModel) int {
		return cmp.Or(cmp.Compare(b.ReleaseDate, a.ReleaseDate), cmp.Compare(a.ID, b.ID))
	})
	var out []loomharness.Model
	for _, a := range aliases {
		i := slices.IndexFunc(ms, func(m snapshotModel) bool { return m.Family == a.family })
		if i < 0 {
			return fallback()
		}
		out = append(out, model(a.id, a.name, ms[i].Context, ms[i].Input, ms[i].Effort))
	}
	for _, m := range ms {
		out = append(out, model(m.ID, cmp.Or(m.Name, m.ID), m.Context, m.Input, m.Effort))
	}
	return out
}

// Models lists the `claude --model` aliases and the full model ids from the
// bundled snapshot; the CLI has no model list. None is marked default: with
// no --model the CLI picks the account's default.
func (a *Adapter) Models(context.Context) ([]loomharness.Model, error) {
	return catalog(modelsJSON), nil
}
