// The effort choices and context windows follow T3 Code
// apps/server/src/provider/Layers/ClaudeProvider.ts (CLAUDE_MODEL_CATALOG) at
// commit 2daff8c25, limited to the values `claude --effort` takes.
//
// Copyright (c) 2026 T3 Tools Inc. Licensed under the MIT License; the full
// notice is in THIRD_PARTY_NOTICES.md.

package claude

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// effort is the --effort option (low, medium, high, xhigh, max; the CLI
// defaults to high).
var effort = loomharness.OptionDescriptor{ID: loomharness.OptionEffort, Label: "Effort", Type: loomharness.OptionSelect,
	Current: "high", Choices: []loomharness.OptionChoice{{ID: "low", Label: "Low"}, {ID: "medium", Label: "Medium"},
		{ID: "high", Label: "High", Default: true}, {ID: "xhigh", Label: "Extra High"}, {ID: "max", Label: "Max"}}}

// Models lists the model aliases `claude --model` accepts; the CLI has no
// model list. None is marked default: with no --model the CLI picks the
// account's default.
func (a *Adapter) Models(context.Context) ([]loomharness.Model, error) {
	in := []string{"text", "image", "pdf"}
	m := func(id, name string, ctx int64, opts ...loomharness.OptionDescriptor) loomharness.Model {
		return loomharness.Model{ID: id, Name: name, Provider: "anthropic", ProviderName: "Anthropic",
			ContextLimit: ctx, Input: in, Options: opts}
	}
	return []loomharness.Model{m("opus", "Opus", 1_000_000, effort), m("sonnet", "Sonnet", 200_000, effort),
		m("haiku", "Haiku", 200_000)}, nil
}
