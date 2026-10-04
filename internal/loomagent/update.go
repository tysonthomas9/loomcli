package loomagent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Agent-level events Update saves in agent_events (design v2 §5.2).
const (
	KindAgentUpdated   = "agent.updated"
	KindHarnessChanged = "harness.changed"
	KindAskLost        = "ask.lost"
	KindError          = "error"
	// KindModelUnverified warns that the agent's model was not in its
	// harness's catalog when chosen; the harness decides (MCS1).
	KindModelUnverified = "model.unverified"
)

// UpdateRequest is the Update input (design v2 §4.6). Empty fields are left
// unchanged. Expect.SpecVersion is optional for Name, Model and options and
// required for Harness.
type UpdateRequest struct {
	Envelope
	AgentID, Name, Model, Harness string
	// Effort is shorthand for the effort option. Options set the model's
	// options by id, keeping the others; both apply from the next turn.
	Effort  string
	Options []loomharness.Option
}

// Update changes an agent's name, model and its options, or harness. Every
// accepted change bumps the spec version; a retry with the last applied
// RequestID returns the current agent. A single task that is not finished
// may change only its name.
func (s *Service) Update(ctx context.Context, req UpdateRequest) (AgentInfo, error) {
	defer s.lockReady(ctx, req.AgentID)()
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return AgentInfo{}, err
	}
	if req.RequestID != "" && deref(a.LastRequestID) == req.RequestID {
		return info(a), nil
	}
	if req.Expect != nil && req.Expect.SpecVersion != nil && *req.Expect.SpecVersion != a.SpecVersion {
		return AgentInfo{}, &Error{Code: CodeSpecVersionMismatch, Message: fmt.Sprintf("spec version is %d", a.SpecVersion)}
	}
	if err := checkUpdate(a, req); err != nil {
		return AgentInfo{}, err
	}
	if req.Harness != "" && req.Harness != a.Harness {
		return s.switchHarness(ctx, a, req)
	}
	to := a.SpecOf()
	if req.Name != "" {
		to.Name = req.Name
	}
	if req.Model != "" || req.Effort != "" || len(req.Options) > 0 {
		if err := s.choose(ctx, a, req, &to); err != nil {
			return AgentInfo{}, err
		}
	}
	if to.Name == a.Name && deref(to.Model) == deref(a.Model) && to.SpecJSON == a.SpecJSON {
		return info(a), nil
	}
	a, err = s.commitSpec(ctx, a, to, req.RequestID, KindAgentUpdated)
	if err != nil {
		return AgentInfo{}, err
	}
	return info(a), nil
}

// commitSpec bumps a's spec version, records requestID and, after the
// commit, saves the change as a `kind` event (agent.updated or harness.changed).
func (s *Service) commitSpec(ctx context.Context, a loomstore.Agent, to loomstore.AgentSpec, requestID, kind string) (loomstore.Agent, error) {
	to.SpecVersion = a.SpecVersion + 1
	if requestID != "" {
		to.LastRequestID = &requestID
	}
	err := s.store.CompareAndSetSpec(ctx, a.AgentID, a.SpecVersion, to)
	switch {
	case errors.Is(err, loomstore.ErrSpecChanged):
		return a, &Error{Code: CodeSpecVersionMismatch, Message: "the agent changed meanwhile"}
	case err != nil && strings.Contains(err.Error(), "agents.name"):
		return a, nameTaken(to.Name)
	case err != nil:
		return a, err
	}
	from, fromModel := a.Harness, deref(a.Model)
	a.Name, a.SpecJSON, a.Harness, a.Model = to.Name, to.SpecJSON, to.Harness, to.Model
	a.HarnessSessionID, a.HarnessSessionRoot = to.HarnessSessionID, to.HarnessSessionRoot
	a.LastRequestID, a.SpecVersion = to.LastRequestID, to.SpecVersion
	if err := s.appendEvent(ctx, a.AgentID, kind, kind+":v"+strconv.FormatInt(a.SpecVersion, 10),
		map[string]any{"name": a.Name, "model": deref(a.Model), "from_harness": from, "harness": a.Harness,
			"spec_version": a.SpecVersion}); err != nil {
		return a, err
	}
	if from == a.Harness && fromModel == deref(a.Model) {
		return a, nil
	}
	return a, s.warnUnverified(ctx, a)
}

// warnUnverified saves model.unverified when a's model was not in its
// harness's catalog when chosen (MCS1).
func (s *Service) warnUnverified(ctx context.Context, a loomstore.Agent) error {
	if cfg, err := loadConfig(a); err != nil || !cfg.ModelUnverified {
		return err
	}
	return s.appendEvent(ctx, a.AgentID, KindModelUnverified, KindModelUnverified+":v"+strconv.FormatInt(a.SpecVersion, 10),
		map[string]any{"model": deref(a.Model), "harness": a.Harness,
			"message": fmt.Sprintf("model %q is not in %s's model list; the harness decides whether it runs", deref(a.Model), a.Harness)})
}

// checkUpdate refuses a change of anything but the name of an unfinished
// single task, and effort or options together with a harness switch.
func checkUpdate(a loomstore.Agent, req UpdateRequest) error {
	options := req.Effort != "" || len(req.Options) > 0
	if a.Mode == "single_task" && a.State != StateFinished && (options || req.Model != "" || req.Harness != "") {
		return &Error{Code: CodeAgentBusy, Message: "only the name of an unfinished single task can change"}
	}
	if options && req.Harness != "" && req.Harness != a.Harness {
		return invalid("set effort and options after the harness switch, from the new harness's catalog")
	}
	return nil
}

// choose applies req's model and options to to and to a's session from its
// next turn. The model and options are saved in the spec's Model and
// Options, which every hand-off's resume sets on the session again (SM1); a
// model change keeps the options the new model takes, a create override's
// effort included, so the spec's Effort is then cleared.
func (s *Service) choose(ctx context.Context, a loomstore.Agent, req UpdateRequest, to *loomstore.AgentSpec) error {
	cfg, err := loadConfig(a)
	if err != nil {
		return err
	}
	if err := checkModelID(req.Model); err != nil {
		return err
	}
	have := selected(cfg)
	target, opts, unverified, err := s.selection(ctx, a.Harness, cmp.Or(req.Model, deref(a.Model)), have, req)
	if err != nil {
		return err
	}
	if req.Model != "" {
		to.Model = &req.Model
	}
	if !slices.Equal(opts, have) || unverified != cfg.ModelUnverified || cmp.Or(req.Model, cfg.Model) != cfg.Model {
		cfg.Model, cfg.Effort, cfg.Options, cfg.ModelUnverified = cmp.Or(req.Model, cfg.Model), "", opts, unverified
		b, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		to.SpecJSON = string(b)
	} else if req.Model == "" {
		return nil
	}
	return s.setModel(ctx, a, target, opts)
}

// checkModel refuses a malformed model and reports whether harness's catalog
// lacks it (MCS1: it passes unverified). An unwired harness skips the check.
func (s *Service) checkModel(ctx context.Context, harness, model string) (unverified bool, err error) {
	if err := checkModelID(model); err != nil {
		return false, err
	}
	ids, err := s.models(ctx, harness)
	return ids != nil && !slices.Contains(ids, model), err
}

// models lists harness's model ids, or nil when it is not wired.
func (s *Service) models(ctx context.Context, harness string) ([]string, error) {
	ms, err := s.catalog(ctx, harness)
	if err != nil || ms == nil {
		return nil, err
	}
	ids := []string{}
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// setModel applies model and opts to a's current session from its next turn.
func (s *Service) setModel(ctx context.Context, a loomstore.Agent, model string, opts []loomharness.Option) error {
	sess, _, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return err
	}
	return harnessErr(sess.SetModel(ctx, model, opts))
}

// current returns a's current native session and ref, or nil when a has
// none or its harness is not wired. The ref is the recorded row matching the
// row's session id and root. A legacy row with no root saved resolves only
// when exactly one recorded row has that id; several roots fail closed.
func (s *Service) current(ctx context.Context, a loomstore.Agent) (loomharness.Session, loomharness.NativeRef, error) {
	h, ok := s.harnesses[a.Harness]
	if !ok || a.HarnessSessionID == nil {
		return nil, loomharness.NativeRef{}, nil
	}
	owned, err := s.store.NativeSessions(ctx, a.AgentID)
	if err != nil {
		return nil, loomharness.NativeRef{}, err
	}
	var refs []loomharness.NativeRef
	for _, n := range owned {
		if n.Harness == a.Harness && n.NativeID == *a.HarnessSessionID &&
			(a.HarnessSessionRoot == nil || n.NativeRoot == *a.HarnessSessionRoot) {
			refs = append(refs, loomharness.NativeRef{Root: n.NativeRoot, NativeID: n.NativeID})
		}
	}
	switch len(refs) {
	case 0:
		return nil, loomharness.NativeRef{}, fmt.Errorf("loomagent: %s's current session is not recorded", a.AgentID)
	case 1:
		return h.Session(refs[0]), refs[0], nil
	}
	return nil, loomharness.NativeRef{}, fmt.Errorf("loomagent: %s's current session %s is recorded under %d roots and has no saved root",
		a.AgentID, *a.HarnessSessionID, len(refs))
}

// appendEvent saves one agent-level event; a repeated eventID is a no-op.
func (s *Service) appendEvent(ctx context.Context, agentID, kind, eventID string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.events.Append(ctx, loomstore.Event{AgentID: agentID, EventID: eventID, Kind: kind, Payload: b})
	return err
}

// harnessErr maps a harness failure to harness_unavailable or harness_error.
func harnessErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, loomharness.ErrUnavailable):
		return &Error{Code: CodeHarnessUnavailable, Message: err.Error()}
	}
	return &Error{Code: CodeHarnessError, Message: err.Error()}
}
