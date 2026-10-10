package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

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
//
// A switch with a RequestID saves its record switching, with its Open key,
// before it stops the turn, and its commit saves it done (OR5d). A pending
// switch (key, its saved Open key) is finished by running these steps again,
// from a retry of req or from settle: stopTurn does nothing on a stopped
// turn and Open, idempotent by key, returns the same session. A failure
// drops the record, so req can be retried.
func (s *Service) switchHarness(ctx context.Context, a loomstore.Agent, req UpdateRequest, key string) (AgentInfo, error) {
	h, cfg, model, rules, err := s.beginSwitch(ctx, a, req, key)
	if err != nil {
		return AgentInfo{}, err
	}
	key = openKey(a)
	failed := func(cause error) (AgentInfo, error) { return AgentInfo{}, s.switchFailed(ctx, a, req, cause) }
	if a, err = s.stopTurn(ctx, a); err != nil {
		return failed(err)
	}
	dispatchCrash("switch_stopped")
	launch, err := s.launch(ctx, a, req.Harness)
	if err != nil {
		return failed(err)
	}
	ref, err := h.Open(ctx, loomharness.OpenSpec{Key: key,
		Launch: launch, Preset: cfg.Open, Dir: deref(a.WorktreePath), Model: model, Rules: rules,
		Metadata: map[string]string{"agent_id": a.AgentID}})
	if err != nil {
		return failed(s.leftover(ctx, a.AgentID, req.Harness, ref, harnessErr(err)))
	}
	dispatchCrash("switch_opened")
	if err := s.owned(ctx, a.AgentID, req.Harness, ref); err != nil {
		_ = h.Purge(ctx, []loomharness.NativeRef{ref}) // unrecorded, so remove the orphan now
		return failed(err)
	}
	beforeSwitchCommit()
	cfg.Harness, cfg.Model, cfg.Effort, cfg.Options = req.Harness, model, "", nil // effort and options belong to the old harness's models
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
	if a, err = s.commitSpec(ctx, a, to, req, KindHarnessChanged); err != nil {
		return failed(err) // the recorded destination is an orphan for the R29 purge
	}
	return info(a), nil
}

// beginSwitch resolves req's switch of a. A new one with a RequestID saves
// its record switching; a pending one (key, its saved Open key) that can no
// longer run fails.
func (s *Service) beginSwitch(ctx context.Context, a loomstore.Agent, req UpdateRequest, key string) (
	loomharness.Harness, Config, string, []loomharness.PermissionRule, error) {
	h, cfg, model, err := s.switchTarget(ctx, a, req)
	var rules []loomharness.PermissionRule
	if err == nil {
		rules, err = s.policy(ctx, cfg)
	}
	switch {
	case err != nil && key != "":
		err = s.switchFailed(ctx, a, req, err)
	case key != "" && key != openKey(a): // the spec moved: never seen, as the commit saves the record done
		err = s.switchFailed(ctx, a, req, &Error{Code: CodeSpecVersionMismatch, Message: fmt.Sprintf("spec version is %d", a.SpecVersion)})
	case err == nil && key == "": // saved without a RequestID too, so settle can finish it
		var r loomstore.UpdateRecord
		if r, err = receipt(a, a, req, loomstore.RequestSwitching); err == nil {
			err = s.store.SaveUpdateRecord(ctx, r)
		}
	}
	return h, cfg, model, rules, err
}

// switchFailed saves req's harness.switch_failed on a and drops its pending
// record in one write (Store.FailSwitch); it returns cause. If that write
// fails, the switch stays pending and reconcile is queued to run it again.
func (s *Service) switchFailed(ctx context.Context, a loomstore.Agent, req UpdateRequest, cause error) error {
	e, err := eventRow(a.AgentID, KindError, fmt.Sprintf("harness.switch_failed:v%d:%s", a.SpecVersion, req.RequestID),
		map[string]any{"op": "harness_switch", "harness": req.Harness, "error": cause.Error()})
	if err != nil {
		return err
	}
	if _, err := s.events.commit(func() ([]loomstore.Event, error) {
		got, err := s.store.FailSwitch(ctx, e, req.RequestID)
		return []loomstore.Event{got}, err
	}, func([]loomstore.Event) {}); err != nil {
		s.retryLater(a.AgentID) // still pending: reconcile runs it again
		return errors.Join(cause, err)
	}
	return cause
}

// switchTarget validates a switch of a to req.Harness and returns the
// destination, a's resolved config and the model to open with.
func (s *Service) switchTarget(ctx context.Context, a loomstore.Agent, req UpdateRequest) (loomharness.Harness, Config, string, error) {
	var cfg Config
	if req.Expect == nil || req.Expect.SpecVersion == nil {
		return nil, cfg, "", invalid("a harness switch needs expect.spec_version")
	}
	h, ok := s.harnesses[req.Harness]
	if !ok {
		return nil, cfg, "", &Error{Code: CodeHarnessUnavailable, Message: req.Harness + " is not available"}
	}
	cfg, err := loadConfig(a)
	if err != nil {
		return nil, cfg, "", err
	}
	if len(cfg.Preset.Harnesses) > 0 && !slices.Contains(cfg.Preset.Harnesses, req.Harness) {
		return nil, cfg, "", invalid(fmt.Sprintf("harness %q not allowed for %s", req.Harness, cfg.Preset.Name), cfg.Preset.Harnesses...)
	}
	if restricts(cfg.Rules) && !Enforcement[req.Harness].Rules {
		return nil, cfg, "", invalid(fmt.Sprintf("%s cannot enforce the permission rules of %s", req.Harness, a.Preset), enforcing()...)
	}
	model := req.Model
	if model != "" {
		if cfg.ModelUnverified, err = s.checkModel(ctx, req.Harness, model); err != nil {
			return nil, cfg, "", err
		}
	} else {
		cfg.ModelUnverified = false
		if ids, err := s.models(ctx, req.Harness); err == nil && slices.Contains(ids, deref(a.Model)) {
			model = deref(a.Model) // keep the model only if the destination offers it
		}
	}
	return h, cfg, model, nil
}

// stopTurn stops a's running turn for a switch (R30): the turn is interrupted
// and not replayed, each open ask is saved as ask.lost first, and a goes idle.
func (s *Service) stopTurn(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	if a.State != StateActive && a.State != StateWaiting {
		return a, nil
	}
	if err := s.interrupt(ctx, a); err != nil {
		return a, harnessErr(err)
	}
	if err := s.loseOpen(ctx, a, nil); err != nil {
		return a, err
	}
	to := a.StateOf()
	to.State, to.WaitingOn, to.RunningTurn = StateIdle, nil, nil
	return s.setState(ctx, a, to)
}

// resume recovers a's current session before a hand-over, installing the
// policy compiled from the current bridge registration; a registration or
// install failure stops it before anything runs. A different returned ref
// is recorded as another owned session and becomes current;
// every earlier ref stays owned. It is safe to repeat; current then returns
// the resumed session.
func (s *Service) resume(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	sess, ref, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return a, err
	}
	cfg, err := loadConfig(a)
	if err != nil {
		return a, err
	}
	rules, err := s.policy(ctx, cfg)
	if err != nil {
		return a, err
	}
	l, err := s.launch(ctx, a, a.Harness)
	if err != nil {
		return a, err
	}
	got, err := sess.Resume(ctx, l, rules)
	if errors.Is(err, loomharness.ErrSessionNotFound) {
		return a, permanent{harnessErr(err)} // no retry brings a missing session back
	} else if err != nil {
		return a, harnessErr(err)
	}
	if err := s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: a.AgentID, Harness: a.Harness,
		NativeRoot: got.Root, NativeID: got.NativeID}); err != nil {
		return a, err
	}
	s.markResumed(a.Harness, got)
	if err := s.reapply(ctx, a.Harness, got, cfg, false); err != nil {
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
