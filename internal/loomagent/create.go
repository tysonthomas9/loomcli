package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
	if err := s.waitReady(ctx); err != nil {
		return AgentInfo{}, err
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
	queued := true
	if err == nil && a.CreateStep < stepRow { // a row written before its insert took the first message along
		err = s.queueFirst(ctx, a, req)
		queued = err == nil
	}
	unlock()
	if !queued {
		s.createFailed(ctx, a.AgentID, err)
	}
	if err != nil {
		return AgentInfo{}, err
	}
	id := a.AgentID
	createCrash("inserted")
	if a, err = s.finishCreate(ctx, id); err != nil {
		s.createFailed(ctx, id, err)
		if !isPermanent(err) {
			s.retryLater(id)
		}
		return AgentInfo{}, err
	}
	return info(a), nil
}

// permanent is a Create failure no retry can fix (OR4a): the row's stored
// Config does not load, or the harness refused the session as a bad request.
type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

func isPermanent(err error) bool {
	var p permanent
	return errors.As(err, &p)
}

// createFailed shows why agentID's Create, whose row is written, has not
// finished: the terminal create_incomplete for a permanent failure, else
// create_retrying while reconcile retries it. It is saved even when the
// request was cancelled, within a bounded time.
func (s *Service) createFailed(ctx context.Context, agentID string, err error) {
	if err == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	reason := AttentionCreateRetrying
	if isPermanent(err) {
		reason = AttentionCreateIncomplete
	}
	slog.Warn("loomagent: create did not finish", "agent", agentID, "reason", reason, "error", err)
	if err := s.createAttention(ctx, agentID, reason); err != nil && !isCode(err, CodeAgentNotFound) {
		slog.Warn("loomagent: could not raise Attention", "agent", agentID, "reason", reason, "error", err)
	}
}

// createAttention shows reason on agentID under its lock, after checking
// its Create is still below done and no Delete replaced it; it replaces the other Create reason but no
// other Attention.
func (s *Service) createAttention(ctx context.Context, agentID, reason string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	cur := deref(a.AttentionReason)
	if err != nil || a.CreateStep >= stepDone || a.DeleteRequested || cur == reason || (cur != "" && !createReason(cur)) {
		return err
	}
	_, err = s.raiseAttention(ctx, a, reason)
	return err
}

// createReason reports whether reason is one a Create below done shows.
func createReason(reason string) bool {
	return reason == AttentionCreateIncomplete || reason == AttentionCreateRetrying
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
		return p, name, parent, nameTaken(name)
	}
	repo, err := s.repoPath(ctx, req.Repo)
	if err != nil {
		return p, name, parent, err
	}
	if req.Parent != "" {
		if parent, err = s.live(ctx, req.Parent); err != nil {
			return p, name, parent, err
		}
	}
	if req.BaseRef == "" && parent.Branch == nil { // a task starts from its lead's branch
		return p, name, parent, invalid("Create needs a base_ref, the branch or commit the agent starts from")
	}
	if req.BaseRef != "" {
		if err := s.workspace.CheckBase(ctx, repo, req.BaseRef); err != nil {
			return p, name, parent, invalid(fmt.Sprintf("base_ref %q is not a branch or commit in %s: %v", req.BaseRef, req.Repo, err))
		}
	}
	return p, name, parent, nil
}

// insertCreate validates req, resolves its Config and inserts its row in
// state creating with its first message, in one transaction (step 1).
func (s *Service) insertCreate(ctx context.Context, req CreateRequest) (loomstore.Agent, error) {
	p, name, parent, err := s.checkCreate(ctx, req)
	if err != nil {
		return loomstore.Agent{}, err
	}
	if req.Overrides, err = s.withBackend(ctx, req.Overrides); err != nil {
		return loomstore.Agent{}, err
	}
	models, err := s.createModels(ctx, req.Overrides.Harness, req.Overrides.Model)
	if err != nil {
		return loomstore.Agent{}, err
	}
	req.Bridge = BridgeCaps{} // never the request's; policy adds the host's at each launch
	cfg, err := Resolve(p, req, req.Overrides.Harness, models)
	if err != nil {
		return loomstore.Agent{}, err
	}
	if _, ok := s.harnesses[cfg.Harness]; !ok {
		return loomstore.Agent{}, s.unavailable(cfg.Harness)
	}
	if _, err := s.policy(ctx, cfg); err != nil {
		return loomstore.Agent{}, err
	}
	spec, err := json.Marshal(cfg)
	if err != nil {
		return loomstore.Agent{}, err
	}
	a := s.newRow(p, name, parent, req, cfg, string(spec))
	a.CreateStep = stepRow
	createCrash("row")
	if err := s.store.InsertCreate(ctx, a, firstSend(a, req)); err != nil {
		if strings.Contains(err.Error(), "agents.name") {
			return a, nameTaken(name)
		}
		return a, err
	}
	return a, nil
}

// Wired lists the harnesses this service runs, sorted.
func (s *Service) Wired() []string { return slices.Sorted(maps.Keys(s.harnesses)) }

// unavailable is harness_unavailable for a harness this service does not run.
func (s *Service) unavailable(harness string) error {
	wired := s.Wired()
	return &Error{Code: CodeHarnessUnavailable, Allowed: wired,
		Message: fmt.Sprintf("%s is not available on this server; use %s", harness, strings.Join(wired, " or "))}
}

// nameTaken is agent_name_taken for name.
func nameTaken(name string) error {
	return &Error{Code: CodeAgentNameTaken, Message: fmt.Sprintf("an agent named %q already exists", name)}
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

// firstSend is req's first message for the creator's slot, or nil.
func firstSend(a loomstore.Agent, req CreateRequest) *loomstore.SlotSend {
	if req.FirstMessage == "" {
		return nil
	}
	return &loomstore.SlotSend{AgentID: a.AgentID, Sender: req.Actor.Kind + ":" + req.Actor.ID,
		RequestID: "create:" + req.RequestID, Body: req.FirstMessage, Source: "create",
		Result: func(bool) (string, error) { return `{"result":"queued"}`, nil }}
}

// queueFirst puts req's first message in the creator's slot and finishes
// step 1, for a row written before its insert took the message along. The
// slot receipt makes a repeat a no-op.
func (s *Service) queueFirst(ctx context.Context, a loomstore.Agent, req CreateRequest) error {
	if f := firstSend(a, req); f != nil {
		if _, _, err := s.store.Send(ctx, *f); err != nil {
			return err
		}
	}
	return s.store.SetCreateStep(ctx, a.AgentID, stepRow, nil, nil, nil)
}

// finishCreate runs agentID's remaining Create steps from its create_step.
// Every step is safe to repeat; reconcileAgent calls it for a row below done.
func (s *Service) finishCreate(ctx context.Context, agentID string) (loomstore.Agent, error) {
	defer s.lock(agentID)()
	a, err := s.agent(ctx, agentID)
	if err == nil && (a.DeletedAt != nil || a.DeleteRequested) { // a Delete won: make nothing for it
		return a, &Error{Code: CodeAgentNotFound, Message: agentID + " is being deleted"}
	}
	if err != nil || a.CreateStep >= stepDone {
		return a, err
	}
	cfg, err := loadConfig(a)
	if err != nil {
		return a, permanent{err}
	}
	if a.CreateStep < stepRow { // an earlier Loom's row without its first message: only its Create request has it
		return a, permanent{fmt.Errorf("loomagent: %s was not fully inserted; retry its Create", a.AgentID)}
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
	if a, err = s.commitCreated(ctx, a); err != nil {
		return a, err
	}
	createCrash("done")
	return s.wake(ctx, a) // hand over the first message
}

// commitCreated is Create's last step, one transaction under the event lane
// (Store.CommitCreate): a creating row moves to idle; a Create Attention
// clears; create_step becomes done; and the created events are
// saved, the parent's child.created only while the parent is not deleted.
// It takes no parent lock. On any error nothing is saved or published.
func (s *Service) commitCreated(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	before, to := a, a.StateOf()
	if a.State == StateCreating {
		to.State = StateIdle
	}
	if createReason(deref(to.AttentionReason)) {
		to.AttentionReason = nil
	}
	a.State, a.AttentionReason, a.CreateStep = to.State, to.AttentionReason, stepDone
	a.Revision++
	out := changeEvents(before, a)
	rows, err := eventRows(out)
	if err != nil {
		return before, err
	}
	more, err := created(a)
	if err != nil {
		return before, err
	}
	if _, err := s.events.commit(func() ([]loomstore.Event, error) {
		return s.store.CommitCreate(ctx, a.AgentID, before.StateOf(), to, before.Revision, stepDone, append(rows, more...))
	}, s.busPublish(out)); err != nil {
		return before, err
	}
	return a, nil
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
	if err := s.worktreeFree(ctx, a, wc.Path); err != nil {
		return a, err
	}
	a.WorktreePath, a.CreateStep = &wc.Path, stepWorktree
	return a, s.store.SetCreateStep(ctx, a.AgentID, stepWorktree, &wc.Path, nil, nil)
}

// worktreeFree refuses with worktree_taken to bind path to a when another
// live (not deleted) agent has it and either has bridge tools: OpenCode
// runs one MCP bridge per folder, so two agents there would share one
// agent's tools and token.
func (s *Service) worktreeFree(ctx context.Context, a loomstore.Agent, path string) error {
	all, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.AgentID == a.AgentID || o.WorktreePath == nil || filepath.Clean(*o.WorktreePath) != filepath.Clean(path) {
			continue
		}
		if s.hasBridgeTools(ctx, a.Preset) || s.hasBridgeTools(ctx, o.Preset) {
			return &Error{Code: CodeWorktreeTaken, Message: path + " belongs to agent " + o.AgentID +
				"; an agent with bridge tools needs a folder of its own"}
		}
	}
	return nil
}

// hasBridgeTools reports whether preset has bridge tools; an unknown preset
// counts as having them, so the check fails closed.
func (s *Service) hasBridgeTools(ctx context.Context, preset string) bool {
	p, err := s.presets.Get(ctx, preset)
	return err != nil || len(p.Tools) > 0
}

// openSession stages skills, opens a's session keyed by its AgentID and
// records the returned ref as owned before anything uses it. Open is
// idempotent by key, so a repeat after a crash gets the same session back.
func (s *Service) openSession(ctx context.Context, a loomstore.Agent, cfg Config) (loomharness.NativeRef, error) {
	h, ok := s.harnesses[a.Harness]
	if !ok {
		return loomharness.NativeRef{}, s.unavailable(a.Harness)
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
		return loomharness.NativeRef{}, s.leftover(ctx, a.AgentID, a.Harness, ref, openErr(err))
	}
	createCrash("recorded")
	if err := s.owned(ctx, a.AgentID, a.Harness, ref); err != nil {
		return ref, err
	}
	return ref, s.reapply(ctx, a.Harness, ref, cfg, true)
}

// openErr is harnessErr for a harness call that sets up a session; a bad
// request is permanent (harnessErr keeps only err's text).
func openErr(err error) error {
	if errors.Is(err, loomharness.ErrBadRequest) {
		return permanent{harnessErr(err)}
	}
	return harnessErr(err)
}

// owned records ref, which Open returned, as a's working session; a
// purge-pending mark left by an earlier failed Open of it is dropped.
func (s *Service) owned(ctx context.Context, agentID, harness string, ref loomharness.NativeRef) error {
	n := loomstore.NativeSession{AgentID: agentID, Harness: harness, NativeRoot: ref.Root, NativeID: ref.NativeID}
	if err := s.store.RecordNativeSession(ctx, n); err != nil {
		return err
	}
	s.markResumed(harness, ref) // Open installed its policy
	return s.store.ClearPurgePending(ctx, n)
}

// leftover handles a failed Open: a non-zero ref it returned is a session it
// created and could not remove, so it is recorded as owned and
// purge-pending, then purged (reconcileAgent retries it). It returns cause,
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
		s.retryLater(agentID)
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

// purgePending purges agentID's purge-pending sessions (reconcileAgent
// retries them). It reads the marks under the agent lock, which Create and
// a harness switch hold around Open, so a re-Open that returned the same
// session as a working one, clearing its mark, is never purged.
func (s *Service) purgePending(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	pending, err := s.store.PurgePending(ctx, s.workspaceID)
	for _, n := range pending {
		if n.AgentID == agentID {
			err = errors.Join(err, s.purgeLeftover(ctx, n))
		}
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
// The preset's current Subagents decides subagentDeny, so an existing agent
// gets a changed flag at its next Open or Resume; the stored flag counts
// only for a preset no longer served.
func (s *Service) policy(ctx context.Context, cfg Config) ([]loomharness.PermissionRule, error) {
	caps, err := s.bridge(ctx, cfg.Preset)
	if err != nil {
		return nil, fmt.Errorf("loomagent: %s bridge registration: %w", cfg.Preset.Name, err)
	}
	rules := slices.Clone(cfg.Rules)
	if caps.HasGitHubRead && caps.HasPublish {
		rules = append(rules, publishDenies...)
	}
	subagents := cfg.Preset.Subagents
	if p, err := s.presets.Get(ctx, cfg.Preset.Name); err == nil {
		subagents = p.Subagents
	}
	if !subagents {
		rules = append(rules, subagentDeny)
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
