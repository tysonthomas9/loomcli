package loomagent

import (
	"context"
	"fmt"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Models returns harness's model catalog: each model with its provider,
// context limit, input types, whether it is the default, and the options it
// takes (design: UI1, T3 Code's optionDescriptors shape).
func (s *Service) Models(ctx context.Context, harness string) ([]loomharness.Model, error) {
	if _, ok := s.harnesses[harness]; !ok {
		return nil, s.unavailable(harness)
	}
	return s.catalog(ctx, harness)
}

// catalog lists harness's models, or nil when it is not wired.
func (s *Service) catalog(ctx context.Context, harness string) ([]loomharness.Model, error) {
	h, ok := s.harnesses[harness]
	if !ok {
		return nil, nil
	}
	ms, err := h.Models(ctx)
	if err != nil {
		return nil, harnessErr(err)
	}
	return ms, nil
}

// selection is the model and the whole option selection a's next turn
// uses after req. target is the model to set on the session: req's model,
// else a's, else the catalog default (OpenCode needs one to carry a variant);
// "" leaves the harness's own default. Options a has that the target model
// does not take are dropped; req's options replace those with the same id.
// A model, option or value the catalog does not list is preset_invalid. An
// unwired harness skips the checks.
func (s *Service) selection(ctx context.Context, harness, model string, have []loomharness.Option, req UpdateRequest) (string, []loomharness.Option, error) {
	set := slices.Clone(req.Options)
	if req.Effort != "" {
		set = append(set, loomharness.Option{ID: loomharness.OptionEffort, Value: req.Effort})
	}
	ms, err := s.catalog(ctx, harness)
	if err != nil || ms == nil {
		return model, merge(have, set), err
	}
	target, m, err := pick(ms, model, harness)
	if err != nil {
		return "", nil, err
	}
	var out []loomharness.Option
	for _, o := range have {
		if checkOption(m, o) == nil {
			out = append(out, o)
		}
	}
	for _, o := range set {
		if err := checkOption(m, o); err != nil {
			return "", nil, err
		}
	}
	return target, merge(out, set), nil
}

// pick finds model in ms. With none chosen it validates options against the
// catalog default, or the first model when the harness marks none (Claude,
// whose CLI picks per account), and target stays "" for the latter.
func pick(ms []loomharness.Model, model, harness string) (string, loomharness.Model, error) {
	ids := make([]string, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
		if model != "" && m.ID == model {
			return model, m, nil
		}
	}
	if model != "" {
		return "", loomharness.Model{}, invalid(fmt.Sprintf("unknown model %q on %s", model, harness), ids...)
	}
	for _, m := range ms {
		if m.Default {
			return m.ID, m, nil
		}
	}
	if len(ms) == 0 {
		return "", loomharness.Model{}, invalid(harness + " offers no models")
	}
	return "", ms[0], nil
}

// checkOption refuses an option m does not take, or a value it does not list.
func checkOption(m loomharness.Model, o loomharness.Option) error {
	var ids []string
	for _, d := range m.Options {
		ids = append(ids, d.ID)
		if d.ID != o.ID {
			continue
		}
		var values []string
		switch d.Type {
		case loomharness.OptionBoolean:
			values = []string{"true", "false"}
		default:
			for _, c := range d.Choices {
				values = append(values, c.ID)
			}
		}
		if !slices.Contains(values, o.Value) {
			return invalid(fmt.Sprintf("unknown %s %q for model %s", o.ID, o.Value, m.ID), values...)
		}
		return nil
	}
	return invalid(fmt.Sprintf("model %s has no option %q", m.ID, o.ID), ids...)
}

// merge returns have with set's options replacing those of the same id,
// in have's order and then set's.
func merge(have, set []loomharness.Option) []loomharness.Option {
	out := slices.Clone(have)
	for _, o := range set {
		if i := slices.IndexFunc(out, func(x loomharness.Option) bool { return x.ID == o.ID }); i >= 0 {
			out[i] = o
		} else {
			out = append(out, o)
		}
	}
	return out
}

// selected is cfg's saved option selection: its Options, else the effort a
// create override set.
func selected(cfg Config) []loomharness.Option {
	if len(cfg.Options) > 0 || cfg.Effort == "" {
		return cfg.Options
	}
	return []loomharness.Option{{ID: loomharness.OptionEffort, Value: cfg.Effort}}
}

// reapply sets cfg's saved model and options on harness's session ref, just
// opened or resumed, so its next turn runs with them: a harness keeps them
// only in the live session, which a restart or an idle unload drops. Open
// already took the model, so after one only options are set.
func (s *Service) reapply(ctx context.Context, harness string, ref loomharness.NativeRef, cfg Config, opened bool) error {
	opts := selected(cfg)
	if len(opts) == 0 && (opened || cfg.Model == "") {
		return nil
	}
	return harnessErr(s.harnesses[harness].Session(ref).SetModel(ctx, cfg.Model, opts))
}
