// Ported from T3 Code apps/server/src/provider/Layers/CodexProvider.ts
// (mapCodexModelCapabilities) at commit 2daff8c25.
//
// Copyright (c) 2026 T3 Tools Inc. Licensed under the MIT License; the full
// notice is in THIRD_PARTY_NOTICES.md.

package codex

import (
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
)

var effortLabels = map[string]string{"none": "None", "minimal": "Minimal", "low": "Low", "medium": "Medium",
	"high": "High", "xhigh": "Extra High", "max": "Max", "ultra": "Ultra"}

// catalogModel maps one model/list entry to the port's catalog shape: its
// supported reasoning efforts become the effort option, its default effort
// the option's current value.
func catalogModel(m protocol.Model) loomharness.Model {
	out := loomharness.Model{ID: m.Model, Name: m.DisplayName, Provider: "codex", ProviderName: "Codex",
		Default: m.IsDefault, Input: []string{}}
	for _, raw := range m.InputModalities {
		var tag string
		if json.Unmarshal(raw, &tag) == nil && tag != "" {
			out.Input = append(out.Input, tag)
		}
	}
	if len(m.InputModalities) == 0 {
		out.Input = []string{"text", "image"} // codex's default when the catalog omits it
	}
	if len(m.SupportedReasoningEfforts) == 0 {
		return out
	}
	d := loomharness.OptionDescriptor{ID: loomharness.OptionEffort, Label: "Effort", Type: loomharness.OptionSelect}
	for _, e := range m.SupportedReasoningEfforts {
		label := effortLabels[e.ReasoningEffort]
		if label == "" {
			label = e.ReasoningEffort
		}
		c := loomharness.OptionChoice{ID: e.ReasoningEffort, Label: label, Description: e.Description,
			Default: e.ReasoningEffort == m.DefaultReasoningEffort}
		if c.Default {
			d.Current = c.ID
		}
		d.Choices = append(d.Choices, c)
	}
	out.Options = []loomharness.OptionDescriptor{d}
	return out
}
