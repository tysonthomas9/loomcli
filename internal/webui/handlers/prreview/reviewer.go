package prreview

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const reviewerAgentNameMaxLen = 100
const legacyReviewerRepoSegmentMaxLen = 48
const reviewerIdentityHashLen = 8
const reviewerRoleName = "pr-reviewer"
const reviewerPromptFile = "builtin:pr-review-checkout"
const reviewerRoleDescription = "PR review checkout terminal agent"

// terminalKindAgent mirrors the agent-terminal tab kind used by the terminal
// handlers (internal/webui/handlers/terminal); tabs of this kind for the
// reviewer's agent name are the reviewer's live PTYs.
const terminalKindAgent = "agent"

// reviewerAgentName expects the canonical owner/repo pair returned by the
// repository membership check.
func reviewerAgentName(owner, repo string, number int) string {
	const prefix = "review-"
	suffix := "-pr-" + strconv.Itoa(number)
	identityHash := reviewerIdentityHash(owner, repo)
	segmentBudget := reviewerAgentNameMaxLen - len(prefix) - len(suffix) - len(identityHash) - 2
	ownerSegment, repoSegment := fitReviewerAgentSegments(
		safeAgentSegment(owner),
		safeAgentSegment(repo),
		segmentBudget,
	)
	return prefix + ownerSegment + "-" + repoSegment + "-" + identityHash + suffix
}

func reviewerIdentityHash(owner, repo string) string {
	sum := sha256.Sum256([]byte(owner + "/" + repo))
	return hex.EncodeToString(sum[:])[:reviewerIdentityHashLen]
}

// intermediateReviewerAgentName reproduces the owner-inclusive shape used
// before reviewer identities gained a collision-resistant hash.
func intermediateReviewerAgentName(owner, repo string, number int) string {
	const prefix = "review-"
	suffix := "-pr-" + strconv.Itoa(number)
	segmentBudget := reviewerAgentNameMaxLen - len(prefix) - len(suffix) - 1
	ownerSegment, repoSegment := fitReviewerAgentSegments(
		safeAgentSegment(owner),
		safeAgentSegment(repo),
		segmentBudget,
	)
	return prefix + ownerSegment + "-" + repoSegment + suffix
}

func legacyReviewerAgentName(repo string, number int) string {
	repoSegment := safeAgentSegment(repo)
	if len(repoSegment) > legacyReviewerRepoSegmentMaxLen {
		repoSegment = truncateAgentSegment(repoSegment, legacyReviewerRepoSegmentMaxLen)
	}
	return "review-" + repoSegment + "-pr-" + strconv.Itoa(number)
}

func fitReviewerAgentSegments(owner, repo string, budget int) (string, string) {
	if len(owner)+len(repo) <= budget {
		return owner, repo
	}
	ownerLimit := budget / 2
	repoLimit := budget - ownerLimit
	if len(owner) < ownerLimit {
		repoLimit += ownerLimit - len(owner)
		ownerLimit = len(owner)
	}
	if len(repo) < repoLimit {
		ownerLimit += repoLimit - len(repo)
		repoLimit = len(repo)
	}
	return truncateAgentSegment(owner, ownerLimit), truncateAgentSegment(repo, repoLimit)
}

func truncateAgentSegment(segment string, limit int) string {
	return strings.TrimRight(segment[:limit], "-")
}

func safeAgentSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "repo"
	}
	return out
}

func (m *Module) resolveRepoCheckout(ctx context.Context, ws, owner, repo string) (repoPath, remote, repoName, wsPath string, ok bool, err error) {
	data, buildErr := storeadapter.BuildWorkspaceDataForKey(ctx, m.store, ws)
	if buildErr != nil {
		return "", "", "", "", false, buildErr
	}
	if data == nil {
		return "", "", "", "", false, nil
	}
	for _, workspaceRepo := range data.Repos {
		gotOwner, gotRepo, parsed := parseGitHubOwnerRepo(workspaceRepo.RemoteURL)
		if !parsed {
			continue
		}
		if !strings.EqualFold(gotOwner, owner) || !strings.EqualFold(gotRepo, repo) {
			continue
		}
		repoPath = strings.TrimSpace(workspaceRepo.Path)
		wsPath = strings.TrimSpace(data.Path)
		if repoPath == "" || wsPath == "" {
			return "", "", "", "", false, nil
		}
		remote = strings.TrimSpace(workspaceRepo.Remote)
		if remote == "" {
			remote = "origin"
		}
		return repoPath, remote, workspaceRepo.Name, wsPath, true, nil
	}
	return "", "", "", "", false, nil
}

func (m *Module) ensureReviewer(w http.ResponseWriter, r *http.Request) {
	ws, params, ok := m.resolveAuthorizedPR(w, r)
	if !ok {
		return
	}

	headSHA, title, baseRef, ok := m.fetchPullRequestHead(w, r, ws, params)
	if !ok {
		return
	}

	repoPath, remote, repoName, wsPath, ok, err := m.resolveRepoCheckout(r.Context(), ws, params.owner, params.repo)
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	if !ok {
		writePRReviewErrorCode(w, http.StatusNotFound, "repo_not_checked_out", "repository is registered but no local checkout path is available", false)
		return
	}

	agentName := reviewerAgentName(params.owner, params.repo, params.number)
	checkedOutSHA, ok := prepareReviewerCheckout(w, reviewerCheckoutSpec{
		ws: ws, agentName: agentName, params: params,
		repoPath: repoPath, remote: remote, repoName: repoName, wsPath: wsPath,
		headSHA: headSHA, title: title, baseRef: baseRef,
		checkoutPRHead: m.checkoutReviewerPRHead,
	})
	if !ok {
		return
	}

	if err := m.ensureReviewerAgentAndRetireLegacy(
		r.Context(), ws, agentName, params.owner, params.repo, params.number,
	); err != nil {
		slog.Error("pr-review: ensure reviewer agent failed", "ws", ws, "agent", agentName, "err", err)
		writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", "failed to prepare the reviewer agent", false)
		return
	}

	writeJSON(w, gen.ReviewerEnsureResult{
		AgentName:     agentName,
		CheckedOutSha: checkedOutSHA,
		Seeded:        true,
	})
}

// fetchPullRequestHead seeds the connector grants and reads the PR's head
// sha, title, and base ref through the connector. It writes the HTTP error
// itself and returns ok=false on failure; a response without a head sha is an
// upstream error because everything downstream (the pinned checkout) needs it.
func (m *Module) fetchPullRequestHead(w http.ResponseWriter, r *http.Request, ws string, params pullRequestPath) (headSHA, title, baseRef string, ok bool) {
	if err := m.ensureConnectorAndGrants(r.Context(), ws, params.owner, params.repo, prReadActions); err != nil {
		writePRReviewError(w, err)
		return "", "", "", false
	}
	res, err := m.dispatcher.Dispatch(r.Context(), connector.Request{
		WorkspaceKey: ws,
		RunID:        syntheticRunID(r, params, providers.ActionGitHubPullRequestRead),
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       providers.ActionGitHubPullRequestRead,
		Resource:     prResource(params.owner, params.repo),
		Args:         pullRequestArgs(params),
		CallSeq:      0,
	})
	if err != nil {
		writePRReviewError(w, err)
		return "", "", "", false
	}
	headSHA = stringValue(res.Body["headSha"])
	if headSHA == "" {
		writePRReviewErrorCode(w, http.StatusBadGateway, "upstream_error", "pull request read response missing head sha", true)
		return "", "", "", false
	}
	return headSHA, stringValue(res.Body["title"]), stringValue(res.Body["baseRef"]), true
}

type reviewerCheckoutFunc func(repoPath, targetPath, remoteName string, prNumber int, headSHA string) (string, error)

type reviewerCheckoutSpec struct {
	ws             string
	agentName      string
	params         pullRequestPath
	repoPath       string
	remote         string
	repoName       string
	wsPath         string
	headSHA        string
	title          string
	baseRef        string
	checkoutPRHead reviewerCheckoutFunc
}

// prepareReviewerCheckout stands up the reviewer's PR-head worktree, records
// it as the agent's launch cwd, and makes the checkout self-describing. It
// writes the HTTP error response itself and returns ok=false on failure.
func prepareReviewerCheckout(w http.ResponseWriter, spec reviewerCheckoutSpec) (string, bool) {
	fail := func(logMsg string, err error, clientMsg string) (string, bool) {
		// Keep the client message terse; the git error can embed local paths.
		slog.Error("pr-review: "+logMsg, "ws", spec.ws, "agent", spec.agentName, "err", err)
		writePRReviewErrorCode(w, http.StatusInternalServerError, "worktree_failed", clientMsg, false)
		return "", false
	}
	stale := func() (string, bool) {
		writePRReviewErrorCode(w, http.StatusConflict, "stale_subject",
			"pull request head changed while preparing the reviewer; refresh and retry", true)
		return "", false
	}
	// Isolated PR-checkout namespace (.loom/pr-worktrees/<repo>/pr-N), distinct
	// from the agent-worktree tree so PR review checkouts never collide with a
	// working agent's branch worktree.
	target, err := localworkspace.PRReviewWorktreePath(spec.wsPath, spec.repoName, spec.params.number)
	if err != nil {
		return fail("worktree path failed", err, "failed to resolve the PR review worktree path")
	}
	checkoutPRHead := spec.checkoutPRHead
	if checkoutPRHead == nil {
		checkoutPRHead = localworkspace.EnsureDetachedGitWorktreeAtPRHead
	}
	checkedOutSHA, err := checkoutPRHead(spec.repoPath, target, spec.remote, spec.params.number, spec.headSHA)
	var changed *localworkspace.PRHeadChangedError
	if errors.As(err, &changed) {
		return stale()
	}
	if err != nil {
		return fail("prepare worktree failed", err, "failed to prepare the PR review worktree")
	}
	if !strings.EqualFold(strings.TrimSpace(checkedOutSHA), strings.TrimSpace(spec.headSHA)) {
		return stale()
	}
	// The remembered worktree IS the reviewer's launch cwd — if we can't persist
	// it the agent would boot in the wrong directory, so this is a hard failure.
	if err := localworkspace.RememberAgentWorktree(spec.ws, spec.agentName, target); err != nil {
		return fail("remember worktree failed", err, "failed to record the PR review worktree")
	}
	// Make the checkout self-describing: fetch the base and record it (plus PR
	// metadata) in per-worktree git config, so the generic reviewer prompt can
	// diff the PR with no PR-specific data injected into the prompt. The prompt
	// is the backend CLI's positional first turn (codex and every harness
	// backend alike), so the reviewer auto-reviews on boot — no delivered seed
	// to dedupe (which is what broke re-opened reviewers on a fresh thread).
	if _, err := localworkspace.RecordPRReviewContext(target, spec.remote, spec.baseRef, map[string]string{
		"Pr":    strconv.Itoa(spec.params.number),
		"Title": spec.title,
		"Url":   fmt.Sprintf("https://github.com/%s/%s/pull/%d", spec.params.owner, spec.params.repo, spec.params.number),
		"Head":  checkedOutSHA,
	}); err != nil {
		return fail("record review context failed", err, "failed to prepare the PR review context")
	}
	return checkedOutSHA, true
}

func (m *Module) ensureReviewerAgent(ctx context.Context, ws, agentName string) error {
	if err := m.ensureReviewerRole(ctx, ws); err != nil {
		return err
	}
	backend := m.reviewerBackend(ctx, ws)
	_, err := m.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: ws,
		Name:         agentName,
		RoleName:     reviewerRoleName,
		Backend:      backend,
		DesiredState: domain.AgentDesiredRunning,
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create reviewer agent: %w", err)
	}
	agent, getErr := m.store.Agents().Get(ctx, ws, agentName)
	if getErr != nil {
		return fmt.Errorf("load existing reviewer agent: %w", getErr)
	}
	if reviewerAgentCurrent(agent, backend, reviewerRoleName) {
		return nil
	}
	return m.migrateReviewer(ctx, ws, agentName, backend, reviewerRoleName)
}

func (m *Module) ensureReviewerAgentAndRetireLegacy(
	ctx context.Context,
	ws, agentName, owner, repo string,
	number int,
) error {
	if err := m.ensureReviewerAgent(ctx, ws, agentName); err != nil {
		return err
	}
	return m.retireLegacyReviewer(ctx, ws, agentName,
		legacyReviewerAgentName(repo, number),
		intermediateReviewerAgentName(owner, repo, number),
	)
}

func reviewerAgentCurrent(agent *domain.Agent, backend, roleName string) bool {
	return strings.EqualFold(strings.TrimSpace(agent.Backend), backend) &&
		strings.TrimSpace(agent.RoleName) == roleName
}

func (m *Module) retireLegacyReviewer(ctx context.Context, ws, agentName string, legacyAgentNames ...string) error {
	seen := make(map[string]struct{}, len(legacyAgentNames))
	for _, legacyAgentName := range legacyAgentNames {
		if legacyAgentName == agentName {
			continue
		}
		if _, duplicate := seen[legacyAgentName]; duplicate {
			continue
		}
		seen[legacyAgentName] = struct{}{}
		if err := m.retireReviewerAgent(ctx, ws, legacyAgentName); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) retireReviewerAgent(ctx context.Context, ws, agentName string) error {
	if _, err := m.store.Agents().Get(ctx, ws, agentName); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("load legacy reviewer agent: %w", err)
	}
	if err := m.stopAndClearReviewerRuntime(ctx, ws, agentName); err != nil {
		return err
	}
	if err := m.store.Agents().Delete(ctx, ws, agentName); err != nil {
		return fmt.Errorf("delete legacy reviewer agent: %w", err)
	}
	return nil
}

func (m *Module) ensureReviewerRole(ctx context.Context, ws string) error {
	if _, err := m.store.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: ws,
		Name:         reviewerRoleName,
		Kind:         string(domain.RoleKindInteractive),
		Description:  reviewerRoleDescription,
		PromptFile:   reviewerPromptFile,
	}); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create reviewer role: %w", err)
	}
	role, err := m.store.Roles().Get(ctx, ws, reviewerRoleName)
	if err != nil {
		return fmt.Errorf("load reviewer role: %w", err)
	}
	return m.reconcileReviewerRole(ctx, ws, role)
}

func (m *Module) reconcileReviewerRole(ctx context.Context, ws string, role *domain.Role) error {
	if role == nil {
		return fmt.Errorf("load reviewer role: %w", domain.ErrNotFound)
	}
	kind := string(domain.RoleKindInteractive)
	prompt := ""
	promptFile := reviewerPromptFile
	description := reviewerRoleDescription
	patch := store.RoleUpdate{}
	if role.Kind != domain.RoleKindInteractive {
		patch.Kind = &kind
	}
	if role.Prompt != "" {
		patch.Prompt = &prompt
	}
	if strings.TrimSpace(role.PromptFile) != reviewerPromptFile {
		patch.PromptFile = &promptFile
	}
	if strings.TrimSpace(role.Description) == "" {
		patch.Description = &description
	}
	if patch.Kind == nil && patch.Prompt == nil && patch.PromptFile == nil && patch.Description == nil {
		return nil
	}
	if _, err := m.store.Roles().Update(ctx, ws, reviewerRoleName, patch); err != nil {
		return fmt.Errorf("reconcile reviewer role: %w", err)
	}
	return nil
}

// reviewerBackend resolves the backend for a reviewer agent: the workspace's
// configured agent backend when it names a controlled lead runtime, else
// codex. Controlled is required because the chat routes deliver messages via
// the lead inbox; an uncontrolled backend would strand them.
func (m *Module) reviewerBackend(ctx context.Context, ws string) string {
	backend := strings.ToLower(runtimepreflight.ResolveLocalBackend(ctx, m.store, ws))
	if !leadcontrol.IsControlledLeadBackend(backend) {
		return leadcontrol.RuntimeProviderCodex
	}
	return backend
}

// migrateReviewer switches an existing reviewer agent to the workspace's
// current backend and role. Order matters: the old runtime's PTY is killed
// first so it cannot keep overwriting the orchestration session's runtime
// metadata after the clear, then stale provider identity keys are removed so
// the new runtime starts from a clean slate, then the agent record flips.
func (m *Module) migrateReviewer(ctx context.Context, ws, agentName, backend, roleName string) error {
	if err := m.stopAndClearReviewerRuntime(ctx, ws, agentName); err != nil {
		return err
	}
	running := domain.AgentDesiredRunning
	if _, err := m.store.Agents().Update(ctx, ws, agentName, store.AgentUpdate{
		Backend:      &backend,
		RoleName:     &roleName,
		DesiredState: &running,
	}); err != nil {
		return fmt.Errorf("update reviewer agent: %w", err)
	}
	return nil
}

func (m *Module) stopAndClearReviewerRuntime(ctx context.Context, ws, agentName string) error {
	if m.terminalSvc != nil {
		tabs, err := m.terminalSvc.ListTabs(ctx, ws)
		if err != nil {
			// Without the tab list the old PTY may survive and fight the new
			// runtime over session metadata — refuse rather than migrate dirty.
			return fmt.Errorf("list reviewer terminals: %w", err)
		}
		for _, tab := range tabs {
			if tab.Kind != terminalKindAgent || tab.AgentID != agentName {
				continue
			}
			// DeleteTab kills the live PTY along with the tab metadata.
			if err := m.terminalSvc.DeleteTab(ctx, ws, tab.SessionName); err != nil {
				return fmt.Errorf("stop reviewer terminal %s: %w", tab.SessionName, err)
			}
		}
	}
	if err := m.clearReviewerRuntimeMetadata(ctx, ws, agentName); err != nil {
		return err
	}
	return nil
}

// reviewerRuntimeMetadataPrefixes are the orchestration-session keys that
// identify a specific runtime process/thread. They must not survive a backend
// migration: a leftover codex endpoint or claude session id would point the
// conversation reader at the previous backend's transcript.
var reviewerRuntimeMetadataPrefixes = []string{"lead_runtime_", "codex_", "lead_harness_"}

func (m *Module) clearReviewerRuntimeMetadata(ctx context.Context, ws, agentName string) error {
	sess, err := store.OrchestrationSessionFor(ctx, m.store, ws, agentName)
	if err != nil {
		return fmt.Errorf("load reviewer orchestration session: %w", err)
	}
	if sess == nil || len(sess.Metadata) == 0 {
		return nil
	}
	cleaned := make(map[string]string, len(sess.Metadata))
	for key, value := range sess.Metadata {
		if hasAnyPrefix(key, reviewerRuntimeMetadataPrefixes) {
			continue
		}
		cleaned[key] = value
	}
	if len(cleaned) == len(sess.Metadata) {
		return nil
	}
	if _, err := m.store.AgentSessions().Update(ctx, ws, sess.SessionID, store.AgentSessionUpdate{
		Metadata: &cleaned,
	}); err != nil {
		return fmt.Errorf("clear reviewer runtime metadata: %w", err)
	}
	return nil
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func (m *Module) postReviewerMessage(w http.ResponseWriter, r *http.Request) {
	ws, params, ok := m.resolveAuthorizedPR(w, r)
	if !ok {
		return
	}

	var req gen.ReviewerMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid reviewer message request body", false)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "text is required", false)
		return
	}

	agentName := reviewerAgentName(params.owner, params.repo, params.number)
	if _, err := m.store.Agents().Get(r.Context(), ws, agentName); err != nil {
		writePRReviewErrorCode(w, http.StatusNotFound, "reviewer_not_started", "reviewer has not been started for this pull request", false)
		return
	}

	unique, err := randomHex(8)
	if err != nil {
		writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", "failed to prepare reviewer message", true)
		return
	}
	result, err := leadcontrol.DeliverLeadMessageWithOptions(r.Context(), m.store, ws, agentName, text, leadcontrol.LeadMessageDeliveryOptions{
		SourceKind: "user_chat",
		DedupeKey:  "pr-review-msg:" + unique,
	})
	if err != nil {
		writePRReviewErrorCode(w, http.StatusInternalServerError, "delivery_failed", "failed to deliver reviewer message", true)
		return
	}
	if result == nil {
		writePRReviewErrorCode(w, http.StatusInternalServerError, "delivery_failed", "reviewer message delivery returned no result", true)
		return
	}

	writeJSON(w, gen.ReviewerMessageResult{
		State:  string(result.State),
		Reason: result.Reason,
	})
}

func randomHex(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
