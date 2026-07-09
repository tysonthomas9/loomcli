package prreview

import (
	"context"
	"crypto/rand"
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
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const reviewerAgentSegmentMaxLen = 48

func reviewerAgentName(repo string, number int) string {
	return "review-" + safeAgentSegment(repo) + "-pr-" + strconv.Itoa(number)
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
	if len(out) > reviewerAgentSegmentMaxLen {
		out = strings.Trim(out[:reviewerAgentSegmentMaxLen], "-")
	}
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
	ws := r.PathValue("ws")
	params, ok := parsePullRequestPath(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("number"))
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid pull request path", false)
		return
	}
	canonOwner, canonRepo, ok := m.authorizeRepo(w, r, ws, params.owner, params.repo)
	if !ok {
		return
	}
	params.owner, params.repo = canonOwner, canonRepo

	if err := m.ensureConnectorAndGrants(r.Context(), ws, params.owner, params.repo, prReviewActions); err != nil {
		writePRReviewError(w, err)
		return
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
		return
	}
	headSHA := stringValue(res.Body["headSha"])
	if headSHA == "" {
		writePRReviewErrorCode(w, http.StatusBadGateway, "upstream_error", "pull request read response missing head sha", true)
		return
	}
	title := stringValue(res.Body["title"])
	baseRef := stringValue(res.Body["baseRef"])

	repoPath, remote, repoName, wsPath, ok, err := m.resolveRepoCheckout(r.Context(), ws, params.owner, params.repo)
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	if !ok {
		writePRReviewErrorCode(w, http.StatusNotFound, "repo_not_checked_out", "repository is registered but no local checkout path is available", false)
		return
	}

	agentName := reviewerAgentName(params.repo, params.number)
	// Isolated PR-checkout namespace (.loom/pr-worktrees/<repo>/pr-N), distinct
	// from the agent-worktree tree so PR review checkouts never collide with a
	// working agent's branch worktree.
	target, pathErr := localworkspace.PRReviewWorktreePath(wsPath, repoName, params.number)
	if pathErr != nil {
		slog.Error("pr-review: worktree path failed", "ws", ws, "agent", agentName, "err", pathErr)
		writePRReviewErrorCode(w, http.StatusInternalServerError, "worktree_failed", "failed to resolve the PR review worktree path", false)
		return
	}
	checkedOutSHA, err := localworkspace.EnsureDetachedGitWorktreeAtPRHead(repoPath, target, remote, params.number, headSHA)
	if err != nil {
		// Keep the client message terse; the git error can embed local paths.
		slog.Error("pr-review: prepare worktree failed", "ws", ws, "agent", agentName, "err", err)
		writePRReviewErrorCode(w, http.StatusInternalServerError, "worktree_failed", "failed to prepare the PR review worktree", false)
		return
	}
	// The remembered worktree IS the reviewer's launch cwd — if we can't persist
	// it the agent would boot in the wrong directory, so this is a hard failure.
	if err := localworkspace.RememberAgentWorktree(ws, agentName, target); err != nil {
		slog.Error("pr-review: remember worktree failed", "ws", ws, "agent", agentName, "err", err)
		writePRReviewErrorCode(w, http.StatusInternalServerError, "worktree_failed", "failed to record the PR review worktree", false)
		return
	}

	if err := m.ensureReviewerAgent(r.Context(), ws, agentName); err != nil {
		slog.Error("pr-review: ensure reviewer agent failed", "ws", ws, "agent", agentName, "err", err)
		writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", "failed to create the reviewer agent", false)
		return
	}

	seeded := false
	seedText := reviewerSeedText(params.owner, params.repo, params.number, title, checkedOutSHA, baseRef, remote)
	result, deliveryErr := leadcontrol.DeliverLeadMessageWithOptions(r.Context(), m.store, ws, agentName, seedText, leadcontrol.LeadMessageDeliveryOptions{
		SourceKind: "user_chat",
		DedupeKey:  "pr-review-seed:" + prResource(params.owner, params.repo) + fmt.Sprintf("#%d", params.number),
	})
	if deliveryErr == nil && result != nil {
		seeded = true
	}

	writeJSON(w, http.StatusOK, gen.ReviewerEnsureResult{
		AgentName:     agentName,
		CheckedOutSha: checkedOutSHA,
		Seeded:        seeded,
	})
}

func (m *Module) ensureReviewerAgent(ctx context.Context, ws, agentName string) error {
	if _, err := m.store.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: ws,
		Name:         "lead",
		Description:  "Lead/orchestrator terminal",
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create lead role: %w", err)
	}
	if _, err := m.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: ws,
		Name:         agentName,
		RoleName:     "lead",
		Backend:      "codex",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create reviewer agent: %w", err)
	}
	return nil
}

func reviewerSeedText(owner, repo string, number int, title, sha, baseRef, remote string) string {
	url := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
	return fmt.Sprintf("You are a READ-ONLY code reviewer for GitHub PR #%d %q (%s). "+
		"Your working directory is a detached checkout of the PR head (%s); the base branch is %s. "+
		"Do NOT edit files, commit, push, or attempt to approve/merge — a human posts the actual review "+
		"decision through the UI. Inspect the change by running `git fetch %s %s && git diff FETCH_HEAD...HEAD` "+
		"and by reading files directly in this checkout. Then give a concise, specific review: first summarize "+
		"what the PR does, then call out risks, bugs, and edge cases with file/line references, and answer the "+
		"user's questions grounded in the actual diff.",
		number, title, url, sha, baseRef, remote, baseRef)
}

func (m *Module) postReviewerMessage(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	params, ok := parsePullRequestPath(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("number"))
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid pull request path", false)
		return
	}
	canonOwner, canonRepo, ok := m.authorizeRepo(w, r, ws, params.owner, params.repo)
	if !ok {
		return
	}
	params.owner, params.repo = canonOwner, canonRepo

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

	agentName := reviewerAgentName(params.repo, params.number)
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

	writeJSON(w, http.StatusOK, gen.ReviewerMessageResult{
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
