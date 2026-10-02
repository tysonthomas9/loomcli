package loomagent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// beforeSwitchCommit runs between the destination Open and the row commit;
// tests use it to crash there.
var beforeSwitchCommit = func() {}

// switchHarness moves a to req.Harness (Spec R30, design v2 §4.6). A running
// turn is stopped first and not replayed; an open ask is reported once as
// ask.lost. The destination session is opened and recorded as owned, then
// one compare-and-set commits the harness, the current session (which is
// also the bridge's caller mapping) and the spec version together. On
// failure the row is unchanged, the old session stays current and the agent
// stays idle, so the slot dispatcher, woken by agent.idle and serialized by
// the agent lock, delivers the next waiting slot on the old harness. On
// success it delivers it on the new session; the old one is no longer used
// and stays recorded for the R29 purge.
func (s *Service) switchHarness(ctx context.Context, a loomstore.Agent, req UpdateRequest) (AgentInfo, error) {
	h, cfg, model, err := s.switchTarget(ctx, a, req)
	if err != nil {
		return AgentInfo{}, err
	}
	rules, err := s.policy(ctx, cfg)
	if err != nil {
		return AgentInfo{}, err
	}
	a, err = s.stopTurn(ctx, a)
	if err != nil {
		return AgentInfo{}, err
	}
	failed := func(cause error) (AgentInfo, error) {
		id := fmt.Sprintf("harness.switch_failed:v%d:%s", a.SpecVersion, req.RequestID)
		if err := s.appendEvent(ctx, a.AgentID, KindError, id,
			map[string]any{"op": "harness_switch", "harness": req.Harness, "error": cause.Error()}); err != nil {
			return AgentInfo{}, err
		}
		return AgentInfo{}, cause
	}
	launch, err := s.launch(ctx, a, req.Harness)
	if err != nil {
		return failed(err)
	}
	ref, err := h.Open(ctx, loomharness.OpenSpec{Key: a.AgentID + "@" + strconv.FormatInt(a.SpecVersion+1, 10),
		Launch: launch, Preset: cfg.Open, Dir: deref(a.WorktreePath), Model: model, Rules: rules,
		Metadata: map[string]string{"agent_id": a.AgentID}})
	if err != nil {
		return failed(harnessErr(err))
	}
	if err := s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: a.AgentID, Harness: req.Harness,
		NativeRoot: ref.Root, NativeID: ref.NativeID}); err != nil {
		_ = h.Purge(ctx, []loomharness.NativeRef{ref}) // unrecorded, so remove the orphan now
		return failed(err)
	}
	beforeSwitchCommit()
	cfg.Harness, cfg.Model = req.Harness, model
	spec, err := json.Marshal(cfg)
	if err != nil {
		return failed(err)
	}
	to := a.SpecOf()
	to.Harness, to.HarnessSessionID, to.HarnessSessionRoot = req.Harness, &ref.NativeID, &ref.Root
	to.SpecJSON, to.Model = string(spec), nil
	if model != "" {
		to.Model = &model
	}
	if a, err = s.commitSpec(ctx, a, to, req.RequestID, KindHarnessChanged); err != nil {
		return failed(err) // the recorded destination is an orphan for the R29 purge
	}
	return info(a), nil
}

// switchTarget validates a switch of a to req.Harness and returns the
// destination, a's resolved config and the model to open with.
func (s *Service) switchTarget(ctx context.Context, a loomstore.Agent, req UpdateRequest) (loomharness.Harness, Config, string, error) {
	var cfg Config
	if req.Expect == nil || req.Expect.SpecVersion == nil {
		return nil, cfg, "", &Error{Code: CodeSpecVersionMismatch, Message: "a harness switch needs Expect.SpecVersion"}
	}
	h, ok := s.harnesses[req.Harness]
	if !ok {
		return nil, cfg, "", &Error{Code: CodeHarnessUnavailable, Message: req.Harness + " is not available"}
	}
	if err := json.Unmarshal([]byte(a.SpecJSON), &cfg); err != nil {
		return nil, cfg, "", fmt.Errorf("loomagent: %s spec: %w", a.AgentID, err)
	}
	if len(cfg.Preset.Harnesses) > 0 && !slices.Contains(cfg.Preset.Harnesses, req.Harness) {
		return nil, cfg, "", invalid(fmt.Sprintf("harness %q not allowed for %s", req.Harness, cfg.Preset.Name), cfg.Preset.Harnesses...)
	}
	if restricts(cfg.Rules) && !Enforcement[req.Harness].Rules {
		return nil, cfg, "", invalid(fmt.Sprintf("%s cannot enforce the permission rules of %s", req.Harness, a.Preset), enforcing()...)
	}
	model := req.Model
	if model != "" {
		if err := s.checkModel(ctx, req.Harness, model); err != nil {
			return nil, cfg, "", err
		}
	} else if ids, err := s.models(ctx, req.Harness); err == nil && slices.Contains(ids, deref(a.Model)) {
		model = deref(a.Model) // keep the model only if the destination offers it
	}
	return h, cfg, model, nil
}

// stopTurn stops a's running turn for a switch (R30): the turn is interrupted
// and not replayed, an open ask is reported once as ask.lost, and a goes idle.
func (s *Service) stopTurn(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	if a.State != StateActive && a.State != StateWaiting {
		return a, nil
	}
	if s.interrupt != nil {
		if err := s.interrupt(ctx, a); err != nil {
			return a, harnessErr(err)
		}
	}
	if a.State == StateWaiting {
		turn := deref(a.RunningTurnID)
		if err := s.appendEvent(ctx, a.AgentID, KindAskLost, fmt.Sprintf("ask.lost:switch:v%d:%s", a.SpecVersion, turn),
			map[string]any{"reason": "harness_switch", "waiting_on": deref(a.WaitingOn), "turn_id": turn}); err != nil {
			return a, err
		}
	}
	to := a.StateOf()
	to.State, to.WaitingOn, to.RunningTurn = StateIdle, nil, nil
	return s.setState(ctx, a, to)
}

// resume recovers a's current session before a hand-over. A different
// returned ref is recorded as another owned session and becomes current;
// every earlier ref stays owned. It is safe to repeat; current then returns
// the resumed session.
func (s *Service) resume(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	sess, ref, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return a, err
	}
	var cfg Config
	if err := json.Unmarshal([]byte(a.SpecJSON), &cfg); err != nil {
		return a, fmt.Errorf("loomagent: %s spec: %w", a.AgentID, err)
	}
	if _, err := s.policy(ctx, cfg); err != nil { // fail before resuming a turn without the bridge
		return a, err
	}
	l, err := s.launch(ctx, a, a.Harness)
	if err != nil {
		return a, err
	}
	got, err := sess.Resume(ctx, l)
	if err != nil {
		return a, harnessErr(err)
	}
	if err := s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: a.AgentID, Harness: a.Harness,
		NativeRoot: got.Root, NativeID: got.NativeID}); err != nil {
		return a, err
	}
	if got == ref {
		return a, nil
	}
	to := a.SpecOf()
	to.HarnessSessionID, to.HarnessSessionRoot = &got.NativeID, &got.Root
	if err := s.store.CompareAndSetSpec(ctx, a.AgentID, a.SpecVersion, to); err != nil {
		return a, err
	}
	a.HarnessSessionID, a.HarnessSessionRoot = to.HarnessSessionID, to.HarnessSessionRoot
	return a, nil
}
