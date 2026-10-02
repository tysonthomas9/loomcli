package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// KindAgentCreated is saved once, when Create finishes.
const KindAgentCreated = "agent.created"

// Create steps, recorded in create_step as each finishes (design v2 §4.4).
// The first message waits in the creator's slot from step 1; the slot
// dispatcher hands it over once the agent is idle (step 4).
const (
	stepRow      = 1 // the row, plus the first message in the creator's slot
	stepWorktree = 2 // Workspace.Ensure
	stepSession  = 3 // harness Open, recorded with RecordNativeSession
	stepDone     = 5 // state idle, agent.created
)

// createCrash runs at each Create crash point; tests use it to crash there.
var createCrash = func(string) {}

// Create makes an agent, or finishes the one an earlier Create with the same
// ExternalKey or RequestID started (design v2 §4.4). It checks the preset,
// name, parent and repo before any side effect, stores the resolved Config as
// spec_json, and then runs the remaining steps, each safe to repeat.
func (s *Service) Create(ctx context.Context, req CreateRequest) (AgentInfo, error) {
	if req.RequestID == "" {
		return AgentInfo{}, invalid("Create needs a RequestID")
	}
	if req.Actor.Kind == "" {
		req.Actor = ActorRef{Kind: "user", ID: "local"}
	}
	key := "create:req:" + req.RequestID
	if req.ExternalKey != "" {
		key = "create:xkey:" + req.ExternalKey
	}
	unlock := s.lock(key)
	a, err := s.store.FindCreated(ctx, s.workspaceID, req.ExternalKey, req.RequestID)
	switch {
	case err == nil && req.ExternalKey != "":
		err = s.sameCreate(ctx, a, req)
	case errors.Is(err, loomstore.ErrNotFound):
		a, err = s.insertCreate(ctx, req)
	}
	if err == nil && a.CreateStep < stepRow {
		createCrash("row")
		err = s.queueFirst(ctx, a, req)
	}
	unlock()
	if err != nil {
		return AgentInfo{}, err
	}
	if a, err = s.finishCreate(ctx, a.AgentID); err != nil {
		return AgentInfo{}, err
	}
	return info(a), nil
}

// sameCreate checks a Create replayed by ExternalKey against the agent it
// made: the compared spec is the preset, subject type and id, repo and parent.
func (s *Service) sameCreate(ctx context.Context, a loomstore.Agent, req CreateRequest) error {
	p, err := s.presets.Get(ctx, req.Preset)
	if err != nil {
		return err
	}
	if p.OwnerKind == "user" && (a.OwnerKind != req.Actor.Kind || a.OwnerID != req.Actor.ID) {
		return &Error{Code: CodeExternalKeyTaken, Message: fmt.Sprintf("%s is owned by %s:%s", req.ExternalKey, a.OwnerKind, a.OwnerID)}
	}
	if p.Name != a.Preset || req.Subject.Type != deref(a.SubjectType) || req.Subject.ID != deref(a.SubjectID) ||
		req.Repo != a.Repo || req.Parent != deref(a.ParentAgentID) {
		return &Error{Code: CodeExternalKeyConflict, Message: req.ExternalKey + " exists with a different spec"}
	}
	return nil
}

// checkCreate validates req with no side effect and returns its preset,
// name and parent.
func (s *Service) checkCreate(ctx context.Context, req CreateRequest) (Preset, string, loomstore.Agent, error) {
	var parent loomstore.Agent
	p, err := s.presets.Get(ctx, req.Preset)
	if err != nil {
		return p, "", parent, err
	}
	name := req.Name
	if name == "" && p.Name == "lead" {
		if name = os.Getenv("LOOM_AGENT_NAME"); name == "" {
			name = "lead"
		}
	}
	if name == "" || req.Repo == "" {
		return p, name, parent, invalid("Create needs a Name and a Repo")
	}
	if p.OwnerKind == "parent" && req.Parent == "" {
		return p, name, parent, invalid(p.Name + " needs a Parent")
	}
	taken, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Name: name,
		IncludeArchived: true, Limit: 1})
	if err != nil {
		return p, name, parent, err
	}
	if len(taken) > 0 {
		return p, name, parent, &Error{Code: CodeAgentNameTaken, Message: name}
	}
	if _, err := s.repoPath(ctx, req.Repo); err != nil {
		return p, name, parent, err
	}
	if req.Parent != "" {
		parent, err = s.live(ctx, req.Parent)
	}
	return p, name, parent, err
}

// insertCreate validates req, resolves its Config and inserts its row in
// state creating (step 1).
func (s *Service) insertCreate(ctx context.Context, req CreateRequest) (loomstore.Agent, error) {
	p, name, parent, err := s.checkCreate(ctx, req)
	if err != nil {
		return loomstore.Agent{}, err
	}
	if req.Overrides, err = s.withBackend(ctx, req.Overrides); err != nil {
		return loomstore.Agent{}, err
	}
	models, err := s.models(ctx, req.Overrides.Harness)
	if err != nil {
		return loomstore.Agent{}, err
	}
	req.Bridge = BridgeCaps{} // never the request's; policy adds the host's at each launch
	cfg, err := Resolve(p, req, req.Overrides.Harness, models)
	if err != nil {
		return loomstore.Agent{}, err
	}
	if _, err := s.policy(ctx, cfg); err != nil {
		return loomstore.Agent{}, err
	}
	spec, err := json.Marshal(cfg)
	if err != nil {
		return loomstore.Agent{}, err
	}
	a := s.newRow(p, name, parent, req, cfg, string(spec))
	if err := s.store.InsertAgent(ctx, a); err != nil {
		if strings.Contains(err.Error(), "agents.name") {
			return a, &Error{Code: CodeAgentNameTaken, Message: name}
		}
		return a, err
	}
	return a, nil
}

// newRow builds the creating row for req with its resolved Config.
func (s *Service) newRow(p Preset, name string, parent loomstore.Agent, req CreateRequest, cfg Config, spec string) loomstore.Agent {
	id := "agt_" + strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "")
	a := loomstore.Agent{AgentID: id, WorkspaceID: s.workspaceID, Name: name, ProfileKey: name, Preset: p.Name,
		PresetVersion: fmt.Sprint(p.Version), Mode: p.Mode, InteractionMode: "interactive", RoleKind: p.RoleKind,
		SpecJSON: spec, SpecVersion: 1, OwnerKind: req.Actor.Kind, OwnerID: req.Actor.ID,
		CreatedByKind: req.Actor.Kind, CreatedByID: req.Actor.ID, CreateRequestID: req.RequestID,
		Repo: req.Repo, Harness: cfg.Harness, State: StateCreating}
	if p.RoleKind == "worker" {
		a.InteractionMode = "background"
	}
	switch p.OwnerKind {
	case "workspace":
		a.OwnerKind, a.OwnerID = "workspace", s.workspaceID
	case "parent":
		a.OwnerKind, a.OwnerID = "agent", req.Parent
	}
	if req.Parent != "" {
		root := parent.AgentID
		if parent.RootAgentID != nil {
			root = *parent.RootAgentID
		}
		a.ParentAgentID, a.RootAgentID = &req.Parent, &root
	}
	a.SubjectType, a.SubjectID, a.SubjectVersion = opt(req.Subject.Type), opt(req.Subject.ID), opt(req.Subject.Version)
	a.ExternalKey, a.Model = opt(req.ExternalKey), opt(cfg.Model)
	a.BaseRef = opt(req.BaseRef)
	if a.BaseRef == nil && parent.Branch != nil {
		a.BaseRef = parent.Branch // a task branches from its lead's branch tip
	}
	if !strings.HasPrefix(p.Name, "pr-review-") { // reviewers are detached at the head SHA
		a.Branch = opt("loom/agent/" + id)
	}
	return a
}

// withBackend fills an omitted harness, and an omitted model for that
// harness, from the workspace default backend. Explicit values win.
func (s *Service) withBackend(ctx context.Context, o Overrides) (Overrides, error) {
	if o.Harness != "" && o.Model != "" {
		return o, nil
	}
	def, err := s.backend(ctx)
	if err != nil {
		return o, err
	}
	if o.Harness == "" {
		o.Harness = def.Harness
	}
	if o.Model == "" && o.Harness == def.Harness {
		o.Model = def.Model
	}
	return o, nil
}

// queueFirst puts req's first message in the creator's slot and finishes
// step 1. The slot receipt makes a repeat a no-op.
func (s *Service) queueFirst(ctx context.Context, a loomstore.Agent, req CreateRequest) error {
	if req.FirstMessage != "" {
		if _, _, err := s.store.Send(ctx, loomstore.SlotSend{AgentID: a.AgentID, Sender: req.Actor.Kind + ":" + req.Actor.ID,
			RequestID: "create:" + req.RequestID, Body: req.FirstMessage, Source: "create",
			Result: func(bool) (string, error) { return `{"result":"queued"}`, nil }}); err != nil {
			return err
		}
	}
	return s.store.SetCreateStep(ctx, a.AgentID, stepRow, nil, nil, nil)
}

// finishCreate runs agentID's remaining Create steps from its create_step.
// Every step is safe to repeat; Reconcile calls it for a row left creating.
func (s *Service) finishCreate(ctx context.Context, agentID string) (loomstore.Agent, error) {
	defer s.lock(agentID)()
	a, err := s.agent(ctx, agentID)
	if err != nil || a.CreateStep >= stepDone {
		return a, err
	}
	cfg, err := loadConfig(a)
	if err != nil {
		return a, err
	}
	if a.CreateStep < stepRow {
		return a, fmt.Errorf("loomagent: %s was not fully inserted; retry its Create", a.AgentID)
	}
	if a.CreateStep < stepWorktree {
		if a, err = s.ensureWorktree(ctx, a); err != nil {
			return a, err
		}
	}
	if a.CreateStep < stepSession {
		createCrash("open")
		ref, err := s.openSession(ctx, a, cfg)
		if err != nil {
			return a, err
		}
		createCrash("session")
		if err := s.store.SetCreateStep(ctx, a.AgentID, stepSession, nil, &ref.NativeID, &ref.Root); err != nil {
			return a, err
		}
		a.HarnessSessionID, a.HarnessSessionRoot, a.CreateStep = &ref.NativeID, &ref.Root, stepSession
	}
	createCrash("created")
	if a.State == StateCreating {
		to := a.StateOf()
		to.State = StateIdle
		if a, err = s.setState(ctx, a, to); err != nil {
			return a, err
		}
	}
	if err := s.appendEvent(ctx, a.AgentID, KindAgentCreated, KindAgentCreated,
		map[string]any{"name": a.Name, "preset": a.Preset, "harness": a.Harness}); err != nil {
		return a, err
	}
	a.CreateStep = stepDone
	if err := s.store.SetCreateStep(ctx, a.AgentID, stepDone, nil, nil, nil); err != nil {
		return a, err
	}
	return s.wake(ctx, a) // hand over the first message
}

// ensureWorktree ensures a's owned working copy through the Workspace port
// and records the binding (step 2). Ensure reuses a copy a already owns.
func (s *Service) ensureWorktree(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	createCrash("worktree")
	repo, err := s.repoPath(ctx, a.Repo)
	if err != nil {
		return a, err
	}
	wc, err := s.workspace.Ensure(ctx, WorkspaceSpec{Key: a.AgentID, Repo: repo, BaseRef: deref(a.BaseRef),
		Branch: deref(a.Branch), Detached: a.Branch == nil})
	if err != nil {
		return a, err
	}
	a.WorktreePath, a.CreateStep = &wc.Path, stepWorktree
	return a, s.store.SetCreateStep(ctx, a.AgentID, stepWorktree, &wc.Path, nil, nil)
}

// openSession stages skills, opens a's session keyed by its AgentID and
// records the returned ref as owned before anything uses it. Open is
// idempotent by key, so a repeat after a crash gets the same session back.
func (s *Service) openSession(ctx context.Context, a loomstore.Agent, cfg Config) (loomharness.NativeRef, error) {
	h, ok := s.harnesses[a.Harness]
	if !ok {
		return loomharness.NativeRef{}, &Error{Code: CodeHarnessUnavailable, Message: a.Harness + " is not available"}
	}
	rules, err := s.policy(ctx, cfg)
	if err != nil {
		return loomharness.NativeRef{}, err
	}
	if err := s.prepare(ctx, s.target, a); err != nil {
		return loomharness.NativeRef{}, err
	}
	launch, err := s.launch(ctx, a, a.Harness)
	if err != nil {
		return loomharness.NativeRef{}, err
	}
	ref, err := h.Open(ctx, loomharness.OpenSpec{Key: a.AgentID, Launch: launch, Preset: cfg.Open,
		Dir: deref(a.WorktreePath), Model: cfg.Model, Rules: rules, Metadata: map[string]string{"agent_id": a.AgentID}})
	if err != nil {
		return loomharness.NativeRef{}, s.leftover(ctx, a.AgentID, a.Harness, ref, harnessErr(err))
	}
	createCrash("recorded")
	return ref, s.owned(ctx, a.AgentID, a.Harness, ref)
}

// owned records ref, which Open returned, as a's working session; a
// purge-pending mark left by an earlier failed Open of it is dropped.
func (s *Service) owned(ctx context.Context, agentID, harness string, ref loomharness.NativeRef) error {
	n := loomstore.NativeSession{AgentID: agentID, Harness: harness, NativeRoot: ref.Root, NativeID: ref.NativeID}
	if err := s.store.RecordNativeSession(ctx, n); err != nil {
		return err
	}
	return s.store.ClearPurgePending(ctx, n)
}

// leftover handles a failed Open: a non-zero ref it returned is a session it
// created and could not remove, so it is recorded as owned and
// purge-pending, then purged (PurgeLeftovers retries it). It returns cause,
// with any recording or purge error joined.
func (s *Service) leftover(ctx context.Context, agentID, harness string, ref loomharness.NativeRef, cause error) error {
	if ref == (loomharness.NativeRef{}) {
		return cause
	}
	n := loomstore.NativeSession{AgentID: agentID, Harness: harness, NativeRoot: ref.Root, NativeID: ref.NativeID}
	if err := s.store.RecordPurgePending(ctx, n); err != nil {
		return errors.Join(cause, err)
	}
	if err := s.purgeLeftover(ctx, n); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// purgeLeftover purges one purge-pending session and drops its mark.
func (s *Service) purgeLeftover(ctx context.Context, n loomstore.NativeSession) error {
	h, ok := s.harnesses[n.Harness]
	if !ok {
		return fmt.Errorf("loomagent: %s is not available to purge %s", n.Harness, n.NativeID)
	}
	if err := h.Purge(ctx, []loomharness.NativeRef{{Root: n.NativeRoot, NativeID: n.NativeID}}); err != nil {
		return err
	}
	return s.store.ClearPurgePending(ctx, n)
}

// sweepPause runs between the sweep's read of the pending list and its
// purges; tests use it to interleave a re-Open.
var sweepPause = func() {}

// PurgeLeftovers retries every purge-pending session; the dispatcher runs it
// at start-up, so a purge that failed is retried after a restart. Each purge
// runs under its owner's agent lock, which Create and a harness switch hold
// around Open, and only if the mark is still there: a re-Open that returned
// the same session as a working one cleared it.
func (s *Service) PurgeLeftovers(ctx context.Context) error {
	pending, err := s.store.PurgePending(ctx)
	sweepPause()
	for _, n := range pending {
		err = errors.Join(err, func() error {
			defer s.lock(n.AgentID)()
			now, err := s.store.PurgePending(ctx)
			if err != nil || !slices.Contains(now, n) {
				return err
			}
			return s.purgeLeftover(ctx, n)
		}())
	}
	return err
}

// loadConfig decodes a's stored Config. Its saved Rules are kept as saved:
// rows written before R-G may hold bridge-generated gh/git-push denies, but
// that shape records no provenance to tell them from a preset's or user's
// own, so none is dropped. Current Configs store only preset and override
// rules, and policy adds the bridge denies from the current registration.
func loadConfig(a loomstore.Agent) (Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(a.SpecJSON), &cfg); err != nil {
		return cfg, fmt.Errorf("loomagent: %s spec: %w", a.AgentID, err)
	}
	return cfg, nil
}

// policy returns cfg's permission rules compiled with the host's current
// bridge registration for cfg's preset. Stored capabilities are never read.
// A registration error (required wiring absent, or a bridge outage) stops
// the launch: nothing is loosened and no other credentials are tried.
func (s *Service) policy(ctx context.Context, cfg Config) ([]loomharness.PermissionRule, error) {
	caps, err := s.bridge(ctx, cfg.Preset)
	if err != nil {
		return nil, fmt.Errorf("loomagent: %s bridge registration: %w", cfg.Preset.Name, err)
	}
	rules := slices.Clone(cfg.Rules)
	if caps.HasGitHubRead && caps.HasPublish {
		rules = append(rules, publishDenies...)
	}
	return rules, nil
}

// opt is v, or nil when v is empty.
func opt(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
