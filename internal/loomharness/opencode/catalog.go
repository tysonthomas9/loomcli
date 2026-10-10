// Ported from T3 Code apps/server/src/provider/Layers/OpenCodeProvider.ts
// (titleCaseSlug, inferDefaultVariant, openCodeCapabilitiesForModel) at
// commit 2daff8c25.
//
// Copyright (c) 2026 T3 Tools Inc. Licensed under the MIT License; the full
// notice is in THIRD_PARTY_NOTICES.md.

package opencode

import (
	"context"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// wireModel is the part of OpenCode's Model.Info the catalog reads.
type wireModel struct {
	ID           string `json:"id"`
	ProviderID   string `json:"providerID"`
	Name         string `json:"name"`
	Capabilities struct {
		Input []string `json:"input"`
	} `json:"capabilities"`
	Variants []struct {
		ID string `json:"id"`
	} `json:"variants"`
	Limit struct {
		Context int64 `json:"context"`
	} `json:"limit"`
}

// Models lists the models of the service's connected providers as
// "provider/model" ids (GET /api/model returns only enabled models of
// active providers), with the service default marked. A model's variants
// are its effort option.
func (a *Adapter) Models(ctx context.Context) ([]loomharness.Model, error) {
	var models struct{ Data []wireModel }
	if err := a.call(ctx, "GET", "/api/model", nil, &models); err != nil {
		return nil, err
	}
	var def struct{ Data *wireModel }
	if err := a.call(ctx, "GET", "/api/model/default", nil, &def); err != nil {
		return nil, err
	}
	var providers struct {
		Data []struct{ ID, Name string }
	}
	if err := a.call(ctx, "GET", "/api/provider", nil, &providers); err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, p := range providers.Data {
		names[p.ID] = p.Name
	}
	out := make([]loomharness.Model, len(models.Data))
	for i, m := range models.Data {
		out[i] = loomharness.Model{ID: m.ProviderID + "/" + m.ID, Name: m.Name, Provider: m.ProviderID,
			ProviderName: names[m.ProviderID], ContextLimit: m.Limit.Context, Input: inputTypes(m.Capabilities.Input),
			Default: def.Data != nil && def.Data.ProviderID == m.ProviderID && def.Data.ID == m.ID}
		if out[i].ProviderName == "" {
			out[i].ProviderName = m.ProviderID
		}
		if d, ok := variantOption(m); ok {
			out[i].Options = []loomharness.OptionDescriptor{d}
		}
	}
	return out, nil
}

// inputTypes keeps the text, image and pdf input types (OpenCode lists
// modalities such as "text", "image", "pdf", "audio", "video").
func inputTypes(in []string) []string {
	out := []string{}
	for _, t := range in {
		if (t == "text" || t == "image" || t == "pdf") && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// variantOption is the model's variants as the effort option, with T3's
// default for its provider as the current value.
func variantOption(m wireModel) (loomharness.OptionDescriptor, bool) {
	ids := make([]string, len(m.Variants))
	for i, v := range m.Variants {
		ids[i] = v.ID
	}
	if len(ids) == 0 {
		return loomharness.OptionDescriptor{}, false
	}
	def := defaultVariant(m.ProviderID, ids)
	d := loomharness.OptionDescriptor{ID: loomharness.OptionEffort, Label: "Effort", Type: loomharness.OptionSelect, Current: def}
	for _, id := range ids {
		d.Choices = append(d.Choices, loomharness.OptionChoice{ID: id, Label: choiceLabel(id), Default: id == def})
	}
	return d, true
}

func defaultVariant(provider string, ids []string) string {
	switch {
	case len(ids) == 1:
		return ids[0]
	case provider == "anthropic" || strings.HasPrefix(provider, "google"):
		if slices.Contains(ids, "high") {
			return "high"
		}
	case provider == "openai" || provider == "opencode":
		for _, id := range []string{"medium", "high"} {
			if slices.Contains(ids, id) {
				return id
			}
		}
	}
	return ""
}

// choiceLabel is a variant's display name: "Extra High" for xhigh (as the
// claude and codex catalogs show it), else the id title-cased.
func choiceLabel(id string) string {
	if id == "xhigh" {
		return "Extra High"
	}
	return titleCase(id)
}

func titleCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == '/' })
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
