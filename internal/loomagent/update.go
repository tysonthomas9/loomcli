package loomagent

import (
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
)

// UpdateRequest is the Update input (design v2 §4.6). Empty fields are left
// unchanged. Expect.SpecVersion is optional for Name and Model and required
// for Harness.
type UpdateRequest struct {
	Envelope
	AgentID, Name, Model, Harness string
}

// Update changes an agent's name, model or harness. Every accepted change
// bumps the spec version; a retry with the last applied RequestID returns the
// current agent. A single task that is not finished may change only its name.
func (s *Service) Update(ctx context.Context, req UpdateRequest) (AgentInfo, error) {
	defer s.lock(req.AgentID)()
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
	if a.Mode == "single_task" && a.State != StateFinished && (req.Model != "" || req.Harness != "") {
		return AgentInfo{}, &Error{Code: CodeAgentBusy, Message: "only the name of an unfinished single task can change"}
	}
	if req.Harness != "" && req.Harness != a.Harness {
		return s.switchHarness(ctx, a, req)
	}
	to := a.SpecOf()
	if req.Name != "" {
		to.Name = req.Name
	}
	if req.Model != "" {
		if err := s.checkModel(ctx, a.Harness, req.Model); err != nil {
			return AgentInfo{}, err
		}
		to.Model = &req.Model
		if err := s.setModel(ctx, a, req.Model); err != nil {
			return AgentInfo{}, err
		}
	}
	if to.Name == a.Name && deref(to.Model) == deref(a.Model) {
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
		return a, &Error{Code: CodeAgentNameTaken, Message: to.Name}
	case err != nil:
		return a, err
	}
	from := a.Harness
	a.Name, a.SpecJSON, a.Harness, a.Model = to.Name, to.SpecJSON, to.Harness, to.Model
	a.HarnessSessionID, a.HarnessSessionRoot = to.HarnessSessionID, to.HarnessSessionRoot
	a.LastRequestID, a.SpecVersion = to.LastRequestID, to.SpecVersion
	return a, s.appendEvent(ctx, a.AgentID, kind, kind+":v"+strconv.FormatInt(a.SpecVersion, 10),
		map[string]any{"name": a.Name, "model": deref(a.Model), "from_harness": from, "harness": a.Harness,
			"spec_version": a.SpecVersion})
}

// checkModel refuses a model missing from harness's catalog. An unwired
// harness skips the check.
func (s *Service) checkModel(ctx context.Context, harness, model string) error {
	ids, err := s.models(ctx, harness)
	if err != nil || ids == nil || slices.Contains(ids, model) {
		return err
	}
	return invalid(fmt.Sprintf("unknown model %q on %s", model, harness), ids...)
}

// models lists harness's model ids, or nil when it is not wired.
func (s *Service) models(ctx context.Context, harness string) ([]string, error) {
	h, ok := s.harnesses[harness]
	if !ok {
		return nil, nil
	}
	ms, err := h.Models(ctx)
	if err != nil {
		return nil, harnessErr(err)
	}
	ids := []string{}
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// setModel applies model to a's current session from its next turn.
func (s *Service) setModel(ctx context.Context, a loomstore.Agent, model string) error {
	sess, _, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return err
	}
	return harnessErr(sess.SetModel(ctx, model))
}

// current returns a's current native session and ref, or nil when a has
// none or its harness is not wired. The ref is the recorded row matching the
// row's session id and root; with no root saved (Create), the oldest
// recorded row with that id.
func (s *Service) current(ctx context.Context, a loomstore.Agent) (loomharness.Session, loomharness.NativeRef, error) {
	h, ok := s.harnesses[a.Harness]
	if !ok || a.HarnessSessionID == nil {
		return nil, loomharness.NativeRef{}, nil
	}
	owned, err := s.store.NativeSessions(ctx, a.AgentID)
	if err != nil {
		return nil, loomharness.NativeRef{}, err
	}
	for _, n := range owned {
		if n.Harness == a.Harness && n.NativeID == *a.HarnessSessionID &&
			(a.HarnessSessionRoot == nil || n.NativeRoot == *a.HarnessSessionRoot) {
			ref := loomharness.NativeRef{Root: n.NativeRoot, NativeID: n.NativeID}
			return h.Session(ref), ref, nil
		}
	}
	return nil, loomharness.NativeRef{}, fmt.Errorf("loomagent: %s's current session is not recorded", a.AgentID)
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
