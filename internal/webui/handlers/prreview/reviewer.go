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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

const reviewerAgentNameMaxLen = 100
const legacyReviewerRepoSegmentMaxLen = 48
const reviewerIdentityHashLen = 8
const reviewerRoleName = prreviewer.RoleName
const reviewerGitTimeout = 60 * time.Second

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

func (m *Module) resolveRepositoryRef(
	ctx context.Context,
	ws, owner, repo string,
) (repositoryRef, repoName string, ok bool, err error) {
	if m == nil || m.store == nil {
		return "", "", false, sourcecontrol.ErrUnavailable
	}
	repositories, err := m.store.Repos().List(ctx, ws)
	if err != nil {
		return "", "", false, err
	}
	var matched *domain.Repo
	for _, workspaceRepo := range repositories {
		if workspaceRepo == nil {
			return "", "", false, sourcecontrol.ErrInvalidMaterialization
		}
		gotOwner, gotRepo, parsed := parseGitHubOwnerRepo(workspaceRepo.RemoteURL)
		if !parsed {
			continue
		}
		if !strings.EqualFold(gotOwner, owner) || !strings.EqualFold(gotRepo, repo) {
			continue
		}
		if matched != nil {
			return "", "", false, fmt.Errorf(
				"multiple registered repositories match %s/%s: %w",
				owner,
				repo,
				sourcecontrol.ErrInvalidMaterialization,
			)
		}
		matched = workspaceRepo
	}
	if matched == nil {
		return "", "", false, nil
	}
	repositoryRef = strings.TrimSpace(matched.SourceRepoID)
	if repositoryRef == "" {
		repositoryRef = matched.Name
	}
	if repositoryRef == "" || strings.TrimSpace(matched.Name) == "" {
		return "", "", false, sourcecontrol.ErrInvalidMaterialization
	}
	return repositoryRef, matched.Name, true, nil
}

//nolint:cyclop,funlen // Keep PR materialization checks and canonical reviewer provisioning in one compensating transaction before identity creation.
func (m *Module) ensureReviewer(w http.ResponseWriter, r *http.Request) {
	if m == nil || m.reviewerProvisioning == nil || m.reviewerAgents == nil {
		writePRReviewErrorCode(
			w,
			http.StatusServiceUnavailable,
			"agents_unavailable",
			"PR reviewer identity provisioning is unavailable",
			true,
		)
		return
	}
	ws, params, ok := m.resolveAuthorizedPR(w, r)
	if !ok {
		return
	}

	headSHA, title, baseRef, ok := m.fetchPullRequestHead(w, r, ws, params)
	if !ok {
		return
	}

	repositoryRef, repoName, ok, err := m.resolveRepositoryRef(
		r.Context(),
		ws,
		params.owner,
		params.repo,
	)
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	if !ok {
		writePRReviewErrorCode(w, http.StatusNotFound, "repo_not_registered", "repository is not registered in this workspace", false)
		return
	}

	agentName := reviewerAgentName(params.owner, params.repo, params.number)
	gitCtx, cancelGit := reviewerGitContext(r.Context())
	defer cancelGit()
	if m.sourceControl == nil {
		writePRReviewErrorCode(
			w,
			http.StatusServiceUnavailable,
			"source_control_unavailable",
			"source control materialization is unavailable",
			true,
		)
		return
	}
	materialized, err := m.sourceControl.PreparePullRequestCheckout(
		gitCtx,
		sourcecontrol.PullRequestCheckoutCommand{
			WorkspaceKey: ws, ReviewID: agentName, RepositoryRef: repositoryRef,
			Number: params.number, HeadCommit: headSHA, BaseBranch: baseRef,
		},
	)
	var changed *sourcecontrol.RefChangedError
	if errors.As(err, &changed) {
		writePRReviewErrorCode(
			w,
			http.StatusConflict,
			"stale_subject",
			"pull request head changed while preparing the reviewer; refresh and retry",
			true,
		)
		return
	}
	if err != nil {
		slog.Error("pr-review: source control materialization failed",
			"ws", ws, "agent", agentName, "err", err)
		writePRReviewErrorCode(
			w,
			http.StatusInternalServerError,
			"worktree_failed",
			"failed to materialize the PR review repository",
			false,
		)
		return
	}
	if materialized == nil ||
		materialized.WorkspaceKey != ws ||
		materialized.ReviewID != agentName ||
		materialized.RepositoryRef != repositoryRef ||
		!strings.EqualFold(materialized.HeadCommit, headSHA) ||
		materialized.CheckoutPath == "" ||
		materialized.HeadRef == "" ||
		materialized.BaseRef == "" ||
		materialized.BaseCommit == "" {
		writePRReviewErrorCode(
			w,
			http.StatusInternalServerError,
			"worktree_failed",
			"source control returned an invalid PR checkout",
			false,
		)
		return
	}
	wsPath := filepath.Dir(materialized.CheckoutPath)
	checkedOutSHA, ok := prepareReviewerCheckout(w, reviewerCheckoutSpec{
		ctx: gitCtx, ws: ws, agentName: agentName, params: params,
		repoPath: materialized.CheckoutPath, repoName: repoName, wsPath: wsPath,
		headSHA: materialized.HeadCommit, headRef: materialized.HeadRef,
		baseCommit: materialized.BaseCommit, title: title,
		checkoutPRHead:  m.checkoutReviewerPRHead,
		recordPRContext: m.recordReviewerPRContext,
	})
	if !ok {
		return
	}

	if err := m.ensureReviewerAgent(r.Context(), ws, agentName); err != nil {
		slog.Error("pr-review: ensure reviewer agent failed", "ws", ws, "agent", agentName, "err", err)
		writeReviewerProvisioningError(w, err)
		return
	}

	writeJSON(w, gen.ReviewerEnsureResult{
		AgentName:     agentName,
		CheckedOutSha: checkedOutSHA,
		Seeded:        true,
	})
}

func reviewerGitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, reviewerGitTimeout)
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
	res, err := m.dispatcher.Dispatch(r.Context(), connectorsmodule.DispatchCommand{
		WorkspaceKey: ws,
		RunID:        syntheticRunID(r, params, connectorsmodule.ActionGitHubPullRequestRead),
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       connectorsmodule.ActionGitHubPullRequestRead,
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

type reviewerCheckoutFunc func(
	ctx context.Context,
	repoPath, targetPath, headRef, headSHA string,
) (string, error)

type reviewerRecordContextFunc func(
	ctx context.Context,
	worktreePath, baseCommit string,
	meta map[string]string,
) (string, error)

type reviewerCheckoutSpec struct {
	ctx             context.Context
	ws              string
	agentName       string
	params          pullRequestPath
	repoPath        string
	repoName        string
	wsPath          string
	headSHA         string
	headRef         string
	baseCommit      string
	title           string
	checkoutPRHead  reviewerCheckoutFunc
	recordPRContext reviewerRecordContextFunc
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
		checkoutPRHead = localworkspace.EnsureDetachedGitWorktreeAtFetchedPRHead
	}
	checkedOutSHA, err := checkoutPRHead(
		spec.ctx, spec.repoPath, target, spec.headRef, spec.headSHA,
	)
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
	recordPRContext := spec.recordPRContext
	if recordPRContext == nil {
		recordPRContext = localworkspace.RecordPRReviewContextFromFetchedBase
	}
	if _, err := recordPRContext(spec.ctx, target, spec.baseCommit, map[string]string{
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
	if m == nil || m.reviewerProvisioning == nil {
		return prreviewer.ErrUnavailable
	}
	result, err := m.reviewerProvisioning.EnsureReviewer(ctx, prreviewer.EnsureCommand{
		WorkspaceKey: ws,
		AgentID:      agentName,
	})
	if err != nil {
		return err
	}
	if result == nil || result.Agent == nil ||
		result.Agent.WorkspaceKey != ws || result.Agent.AgentID != agentName {
		return fmt.Errorf("pr reviewer provisioning returned an invalid identity: %w", agents.ErrInvalidPersistedState)
	}
	return nil
}

//nolint:funlen // Keep PR authority resolution, reviewer identity lookup, message delivery, and response mapping in one request transaction.
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
	if !m.requireReviewerIdentity(w, r.Context(), ws, agentName) {
		return
	}

	unique, err := randomHex(8)
	if err != nil {
		writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", "failed to prepare reviewer message", true)
		return
	}
	if m.interactionMessenger == nil {
		writePRReviewErrorCode(
			w,
			http.StatusServiceUnavailable,
			"interaction_unavailable",
			"reviewer chat delivery is unavailable",
			true,
		)
		return
	}
	result, err := m.interactionMessenger.DeliverChatMessage(
		r.Context(),
		interaction.DeliverChatMessageCommand{
			WorkspaceKey: ws,
			AgentID:      agentName,
			Body:         text,
			SourceKind:   "user_chat",
			DedupeKey:    "pr-review-msg:" + unique,
		},
	)
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

func (m *Module) requireReviewerIdentity(
	w http.ResponseWriter,
	ctx context.Context,
	workspace,
	agentID string,
) bool {
	if m == nil || m.reviewerAgents == nil {
		writePRReviewErrorCode(
			w,
			http.StatusServiceUnavailable,
			"agents_unavailable",
			"PR reviewer identity queries are unavailable",
			true,
		)
		return false
	}
	agent, err := m.reviewerAgents.GetAgent(ctx, workspace, agentID)
	if err == nil && agent != nil && agent.DeletedAt == nil {
		return true
	}
	if errors.Is(err, agents.ErrNotFound) || err == nil {
		writePRReviewErrorCode(
			w,
			http.StatusNotFound,
			"reviewer_not_started",
			"reviewer has not been started for this pull request",
			false,
		)
		return false
	}
	writePRReviewErrorCode(
		w,
		http.StatusServiceUnavailable,
		"agents_unavailable",
		"PR reviewer identity queries are unavailable",
		true,
	)
	return false
}

func writeReviewerProvisioningError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, prreviewer.ErrUnavailable),
		errors.Is(err, agents.ErrUnavailable):
		writePRReviewErrorCode(
			w,
			http.StatusServiceUnavailable,
			"agents_unavailable",
			"PR reviewer identity provisioning is unavailable",
			true,
		)
	case errors.Is(err, agents.ErrConflict),
		errors.Is(err, agents.ErrAlreadyExists),
		errors.Is(err, agents.ErrInvalidPersistedState):
		writePRReviewErrorCode(
			w,
			http.StatusConflict,
			"reviewer_identity_conflict",
			"the PR reviewer identity conflicts with an existing Agent definition",
			false,
		)
	case errors.Is(err, agents.ErrInvalid):
		writePRReviewErrorCode(
			w,
			http.StatusBadRequest,
			"invalid",
			"the PR reviewer identity definition is invalid",
			false,
		)
	default:
		writePRReviewErrorCode(
			w,
			http.StatusInternalServerError,
			"internal",
			"failed to prepare the reviewer Agent",
			false,
		)
	}
}

func randomHex(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
