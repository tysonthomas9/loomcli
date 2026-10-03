package loomagent

import (
	"context"
	"fmt"
	"slices"
	"time"

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
	s.mu.Lock()
	if _, ok := s.listed[harness]; !ok {
		s.listed[harness] = time.Now()
	}
	s.mu.Unlock()
	return ms, nil
}

// createModels lists harness's model ids for a create of model (MC1, MCS1).
// A harness that just started lists no models for a moment (OpenCode), or
// only some providers' models: while the catalog is empty a create naming a
// model polls it for up to catalogWait; while it lacks the model, it polls
// until catalogWarmUp after this service first listed the harness. A create
// still waiting after catalogWait, including on a hung listing, which keeps
// it well inside the API server's 30s write timeout, gets the ids listed so
// far (none for a hung listing), so Resolve accepts the model unverified
// and the harness decides. A warm catalog is used at once.
func (s *Service) createModels(ctx context.Context, harness, model string) ([]string, error) {
	deadline := time.Now().Add(s.catalogWait)
	lctx, cancel := context.WithDeadline(ctx, deadline) // bounds a hung listing too
	defer cancel()
	for {
		ids, err := s.models(lctx, harness)
		if err != nil && lctx.Err() != nil && ctx.Err() == nil {
			return []string{}, nil // not ready: unverified
		}
		if err != nil || ids == nil || model == "" || slices.Contains(ids, model) {
			return ids, err
		}
		now := time.Now()
		if len(ids) > 0 {
			s.mu.Lock()
			warm := s.listed[harness].Add(s.catalogWarmUp)
			s.mu.Unlock()
			if !now.Before(warm) {
				return ids, nil
			}
		}
		if !now.Before(deadline) {
			return ids, nil
		}
		select {
		case <-lctx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return ids, nil
		case <-time.After(s.catalogPoll):
		}
	}
}

// selection is the model and the whole option selection a's next turn
// uses after req. target is the model to set on the session: req's model,
// else a's, else the catalog default (OpenCode needs one to carry a variant);
// "" leaves the harness's own default. Options a has that the target model
// does not take are dropped; req's options replace those with the same id.
// An option or value the catalog does not list is preset_invalid. A model it
// does not list passes unverified (MCS1) with req's options unchecked, and
// the harness decides. An unwired harness skips the checks.
func (s *Service) selection(ctx context.Context, harness, model string, have []loomharness.Option, req UpdateRequest) (string, []loomharness.Option, bool, error) {
	set := slices.Clone(req.Options)
	if req.Effort != "" {
		set = append(set, loomharness.Option{ID: loomharness.OptionEffort, Value: req.Effort})
	}
	ms, err := s.catalog(ctx, harness)
	if err != nil || ms == nil {
		return model, merge(have, set), false, err
	}
	target, m, known, err := pick(ms, model, harness)
	if err != nil {
		return "", nil, false, err
	}
	if !known {
		if req.Model == "" {
			set = merge(have, set) // the same model: keep what it had
		}
		return model, set, true, nil
	}
	var out []loomharness.Option
	for _, o := range have {
		if checkOption(m, o) == nil {
			out = append(out, o)
		}
	}
	for _, o := range set {
		if err := checkOption(m, o); err != nil {
			return "", nil, false, err
		}
	}
	return target, merge(out, set), false, nil
}

// pick finds model in ms; known is false when ms does not list it. With none
// chosen it validates options against the catalog default, or the first
// model when the harness marks none (Claude, whose CLI picks per account),
// and target stays "" for the latter.
func pick(ms []loomharness.Model, model, harness string) (target string, m loomharness.Model, known bool, err error) {
	if model != "" {
		i := slices.IndexFunc(ms, func(m loomharness.Model) bool { return m.ID == model })
		if i < 0 {
			return model, loomharness.Model{}, false, nil
		}
		return model, ms[i], true, nil
	}
	for _, m := range ms {
		if m.Default {
			return m.ID, m, true, nil
		}
	}
	if len(ms) == 0 {
		return "", loomharness.Model{}, false, invalid(harness + " offers no models")
	}
	return "", ms[0], true, nil
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
