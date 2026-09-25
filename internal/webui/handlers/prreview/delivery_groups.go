package prreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/prreadiness"
	"github.com/tysonthomas9/loomcli/internal/prref"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const (
	maxDeliveryGroupListLimit = 200
	defaultDeliveryGroupLimit = 50
	maxDeliveryGroupBodyBytes = 1 << 20
)

// deliveryGroupMemberView joins a durable member to the latest readiness
// observation (STACKED-PRS-33). Warnings stay on the member when the repo is
// removed or GitHub facts are stale/unavailable — members are never deleted.
type deliveryGroupMemberView struct {
	domain.DeliveryGroupMember
	Readiness *prreadiness.View `json:"readiness,omitempty"`
	Warnings  []string          `json:"warnings,omitempty"`
}

type deliveryGroupView struct {
	domain.DeliveryGroup
	Members []deliveryGroupMemberView `json:"members"`
}

type deliveryGroupListData struct {
	DeliveryGroups []deliveryGroupView `json:"delivery_groups"`
	Count          int                 `json:"count"`
	HasMore        bool                `json:"has_more"`
	NextCursor     string              `json:"next_cursor,omitempty"`
	Warnings       []string            `json:"warnings,omitempty"`
}

type deliveryGroupWriteData struct {
	Group    deliveryGroupView `json:"group"`
	Replayed bool              `json:"replayed,omitempty"`
}

type deliveryGroupPreviewData struct {
	GroupID           string               `json:"group_id"`
	Revision          int64                `json:"revision"`
	ServerNow         time.Time            `json:"server_now"`
	FreshForSeconds   int                  `json:"fresh_for_s"`
	StaleAfterSeconds int                  `json:"stale_after_s"`
	Preview           prreadiness.Preview  `json:"preview"`
	RepoErrors        []readinessRepoError `json:"repo_errors"`
	Warnings          []string             `json:"warnings,omitempty"`
}

func (m *Module) requireDeliveryGroups(w http.ResponseWriter) store.DeliveryGroupStore {
	if m == nil || m.deliveryGroups == nil {
		writePRReviewErrorCode(w, http.StatusServiceUnavailable, "delivery_groups_unavailable",
			"delivery groups require a FleetDB-backed store", false)
		return nil
	}
	return m.deliveryGroups
}

func (m *Module) listDeliveryGroups(w http.ResponseWriter, r *http.Request) {
	backend := m.requireDeliveryGroups(w)
	if backend == nil {
		return
	}
	ws := r.PathValue("ws")
	opts := store.DeliveryGroupListOpts{
		State:  strings.TrimSpace(r.URL.Query().Get("state")),
		EpicID: strings.TrimSpace(r.URL.Query().Get("epic_id")),
		Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > maxDeliveryGroupListLimit {
			writePRReviewErrorCode(w, http.StatusBadRequest, "invalid",
				fmt.Sprintf("limit must be an integer between 0 and %d", maxDeliveryGroupListLimit), false)
			return
		}
		opts.Limit = n
	}
	page, err := backend.List(r.Context(), ws, opts)
	if err != nil {
		writeDeliveryGroupError(w, err)
		return
	}
	views, warnings := m.decorateDeliveryGroups(r, ws, page.Groups, false)
	writeJSON(w, deliveryGroupListData{
		DeliveryGroups: views,
		Count:          page.Count,
		HasMore:        page.HasMore,
		NextCursor:     page.NextCursor,
		Warnings:       warnings,
	})
}

func (m *Module) getDeliveryGroup(w http.ResponseWriter, r *http.Request) {
	backend := m.requireDeliveryGroups(w)
	if backend == nil {
		return
	}
	ws := r.PathValue("ws")
	groupID := r.PathValue("group_id")
	group, err := backend.Get(r.Context(), ws, groupID)
	if err != nil {
		writeDeliveryGroupError(w, err)
		return
	}
	view, _ := m.decorateDeliveryGroup(r, ws, group, true)
	writeJSON(w, deliveryGroupWriteData{Group: view})
}

func (m *Module) createDeliveryGroup(w http.ResponseWriter, r *http.Request) {
	backend := m.requireDeliveryGroups(w)
	if backend == nil {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var in domain.DeliveryGroupCreate
	if !decodeDeliveryGroupBody(w, r, &in) {
		return
	}
	if err := m.validateMemberInputs(r, r.PathValue("ws"), in.Members); err != nil {
		writePRReviewErrorCode(w, http.StatusUnprocessableEntity, domain.DeliveryGroupMembersInvalidCode, err.Error(), false)
		return
	}
	res, err := backend.Create(r.Context(), r.PathValue("ws"), idempotencyKey, in)
	if err != nil {
		writeDeliveryGroupError(w, err)
		return
	}
	m.writeDeliveryGroupMutation(w, r, res)
}

func (m *Module) updateDeliveryGroup(w http.ResponseWriter, r *http.Request) {
	backend := m.requireDeliveryGroups(w)
	if backend == nil {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	ifMatch, ok := requireIfMatch(w, r)
	if !ok {
		return
	}
	var in domain.DeliveryGroupUpdate
	if !decodeDeliveryGroupBody(w, r, &in) {
		return
	}
	res, err := backend.Update(r.Context(), r.PathValue("ws"), r.PathValue("group_id"), ifMatch, idempotencyKey, in)
	if err != nil {
		writeDeliveryGroupError(w, err)
		return
	}
	m.writeDeliveryGroupMutation(w, r, res)
}

func (m *Module) setDeliveryGroupMembers(w http.ResponseWriter, r *http.Request) {
	backend := m.requireDeliveryGroups(w)
	if backend == nil {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	ifMatch, ok := requireIfMatch(w, r)
	if !ok {
		return
	}
	var body struct {
		Members []domain.DeliveryGroupMemberInput `json:"members"`
	}
	if !decodeDeliveryGroupBody(w, r, &body) {
		return
	}
	if body.Members == nil {
		body.Members = []domain.DeliveryGroupMemberInput{}
	}
	if err := m.validateMemberInputs(r, r.PathValue("ws"), body.Members); err != nil {
		writePRReviewErrorCode(w, http.StatusUnprocessableEntity, domain.DeliveryGroupMembersInvalidCode, err.Error(), false)
		return
	}
	res, err := backend.SetMembers(r.Context(), r.PathValue("ws"), r.PathValue("group_id"), ifMatch, idempotencyKey, body.Members)
	if err != nil {
		writeDeliveryGroupError(w, err)
		return
	}
	m.writeDeliveryGroupMutation(w, r, res)
}

func (m *Module) archiveDeliveryGroup(w http.ResponseWriter, r *http.Request) {
	backend := m.requireDeliveryGroups(w)
	if backend == nil {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	ifMatch, ok := requireIfMatch(w, r)
	if !ok {
		return
	}
	res, err := backend.Archive(r.Context(), r.PathValue("ws"), r.PathValue("group_id"), ifMatch, idempotencyKey)
	if err != nil {
		writeDeliveryGroupError(w, err)
		return
	}
	m.writeDeliveryGroupMutation(w, r, res)
}

func (m *Module) previewDeliveryGroup(w http.ResponseWriter, r *http.Request) {
	backend := m.requireDeliveryGroups(w)
	if backend == nil {
		return
	}
	ws := r.PathValue("ws")
	group, err := backend.Get(r.Context(), ws, r.PathValue("group_id"))
	if err != nil {
		writeDeliveryGroupError(w, err)
		return
	}
	refs := make([]prref.Ref, 0, len(group.Members))
	warnings := make([]string, 0)
	for _, member := range group.Members {
		ref, parseErr := prref.Parse(member.PRKey)
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: invalid pr_key retained on member", member.PRKey))
			continue
		}
		refs = append(refs, ref)
	}
	repoErrs, refreshErr := m.refreshReadiness(r, ws, refs, true)
	if refreshErr != nil {
		// Partial GitHub failure must not hide the group: continue with cache.
		warnings = append(warnings, sanitizeWarning(refreshErr))
	}
	now := m.readinessClock()
	views := make([]prreadiness.View, 0, len(refs))
	for _, ref := range refs {
		views = append(views, m.readiness.view(ws, ref.Key(), now))
	}
	writeJSON(w, deliveryGroupPreviewData{
		GroupID:           group.ID,
		Revision:          group.Revision,
		ServerNow:         now.UTC(),
		FreshForSeconds:   int(prreadiness.FreshFor / time.Second),
		StaleAfterSeconds: int(prreadiness.StaleAfter / time.Second),
		Preview:           prreadiness.BuildPreview(views),
		RepoErrors:        repoErrs,
		Warnings:          warnings,
	})
}

func (m *Module) writeDeliveryGroupMutation(w http.ResponseWriter, r *http.Request, res *store.DeliveryGroupWriteResult) {
	view, _ := m.decorateDeliveryGroup(r, r.PathValue("ws"), res.Group, false)
	if res.ETag != "" {
		w.Header().Set("ETag", res.ETag)
	}
	if res.Replayed {
		w.Header().Set("X-Idempotency-Replayed", "true")
	}
	status := res.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Success bool                   `json:"success"`
		Data    deliveryGroupWriteData `json:"data"`
	}{
		Success: true,
		Data:    deliveryGroupWriteData{Group: view, Replayed: res.Replayed},
	})
}

func (m *Module) decorateDeliveryGroups(r *http.Request, ws string, groups []*domain.DeliveryGroup, refresh bool) ([]deliveryGroupView, []string) {
	out := make([]deliveryGroupView, 0, len(groups))
	var warnings []string
	for _, g := range groups {
		view, w := m.decorateDeliveryGroup(r, ws, g, refresh)
		out = append(out, view)
		warnings = append(warnings, w...)
	}
	return out, warnings
}

func (m *Module) decorateDeliveryGroup(r *http.Request, ws string, group *domain.DeliveryGroup, refresh bool) (deliveryGroupView, []string) {
	if group == nil {
		return deliveryGroupView{}, nil
	}
	registered := m.registeredRepoIndex(r, ws)
	refs := make([]prref.Ref, 0, len(group.Members))
	for _, member := range group.Members {
		if ref, err := prref.Parse(member.PRKey); err == nil {
			refs = append(refs, ref)
		}
	}
	if refresh && len(refs) > 0 {
		_, _ = m.refreshReadiness(r, ws, refs, false)
	}
	now := m.readinessClock()
	members := make([]deliveryGroupMemberView, 0, len(group.Members))
	var warnings []string
	for _, member := range group.Members {
		mv := deliveryGroupMemberView{DeliveryGroupMember: member}
		ref, err := prref.Parse(member.PRKey)
		if err != nil {
			mv.Warnings = append(mv.Warnings, "invalid pr_key on retained member")
			members = append(members, mv)
			continue
		}
		if !registered[strings.ToLower(ref.Owner+"/"+ref.Repo)] {
			msg := fmt.Sprintf("%s: repository no longer registered; retaining member with last observation", member.PRKey)
			mv.Warnings = append(mv.Warnings, msg)
			warnings = append(warnings, msg)
		}
		view := m.readiness.view(ws, ref.Key(), now)
		mv.Readiness = &view
		members = append(members, mv)
	}
	return deliveryGroupView{
		DeliveryGroup: *group,
		Members:       members,
	}, warnings
}

func (m *Module) registeredRepoIndex(r *http.Request, ws string) map[string]bool {
	out := map[string]bool{}
	data, err := storeadapter.BuildWorkspaceDataForKey(r.Context(), m.store, ws)
	if err != nil || data == nil {
		return out
	}
	for _, repo := range data.Repos {
		owner, name, ok := parseGitHubOwnerRepo(repo.RemoteURL)
		if !ok {
			continue
		}
		out[strings.ToLower(owner+"/"+name)] = true
	}
	return out
}

// validateMemberInputs checks registered-repo PR identities at the Loom API
// boundary before calling FleetDB. Group writes never call GitHub.
func (m *Module) validateMemberInputs(r *http.Request, ws string, members []domain.DeliveryGroupMemberInput) error {
	seen := map[string]bool{}
	for i, member := range members {
		var ref prref.Ref
		var err error
		switch {
		case strings.TrimSpace(member.PRKey) != "":
			ref, err = prref.Parse(member.PRKey)
			if err != nil {
				return fmt.Errorf("members[%d].pr_key: %w", i, err)
			}
			if member.RepoName != "" || member.PRNumber > 0 {
				if member.PRNumber > 0 && member.PRNumber != ref.Number {
					return fmt.Errorf("members[%d]: pr_key disagrees with pr_number", i)
				}
			}
		case member.RepoName != "" && member.PRNumber > 0:
			owner, repo, ok, lookupErr := m.resolveRegisteredRepoName(r, ws, member.RepoName)
			if lookupErr != nil {
				return lookupErr
			}
			if !ok {
				return fmt.Errorf("members[%d]: repository %q is not registered", i, member.RepoName)
			}
			ref = prref.Ref{Owner: owner, Repo: repo, Number: member.PRNumber}
		default:
			return fmt.Errorf("members[%d]: require pr_key or repo_name+pr_number", i)
		}
		key := ref.Key()
		if seen[key] {
			return fmt.Errorf("members[%d]: duplicate PR %s", i, key)
		}
		seen[key] = true
		owner, repo := ref.Owner, ref.Repo
		if _, _, ok, lookupErr := m.workspaceHasRepo(r.Context(), ws, owner, repo); lookupErr != nil {
			return lookupErr
		} else if !ok {
			return fmt.Errorf("members[%d]: repository %s/%s is not registered", i, owner, repo)
		}
	}
	return nil
}

func (m *Module) resolveRegisteredRepoName(r *http.Request, ws, repoName string) (owner, repo string, ok bool, err error) {
	data, buildErr := storeadapter.BuildWorkspaceDataForKey(r.Context(), m.store, ws)
	if buildErr != nil {
		return "", "", false, buildErr
	}
	for _, workspaceRepo := range data.Repos {
		if !strings.EqualFold(workspaceRepo.Name, repoName) {
			continue
		}
		owner, repo, parsed := parseGitHubOwnerRepo(workspaceRepo.RemoteURL)
		return owner, repo, parsed, nil
	}
	return "", "", false, nil
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	if key == "" {
		writePRReviewErrorCode(w, http.StatusPreconditionRequired, domain.DeliveryGroupPreconditionRequiredCode,
			"X-Idempotency-Key is required", false)
		return "", false
	}
	if len(key) > 128 || strings.ContainsAny(key, " \t\r\n") {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "X-Idempotency-Key must be 1-128 printable characters without spaces", false)
		return "", false
	}
	return key, true
}

func requireIfMatch(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		writePRReviewErrorCode(w, http.StatusPreconditionRequired, domain.DeliveryGroupPreconditionRequiredCode,
			"If-Match is required", false)
		return "", false
	}
	if raw == "*" || strings.Contains(raw, ",") || strings.HasPrefix(raw, "W/") {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "If-Match must be a single strong revision tag", false)
		return "", false
	}
	return raw, true
}

func decodeDeliveryGroupBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, maxDeliveryGroupBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", err.Error(), false)
		return false
	}
	return true
}

func writeDeliveryGroupError(w http.ResponseWriter, err error) {
	var conflict *domain.DeliveryGroupConflictError
	var pre *domain.DeliveryGroupPreconditionError
	switch {
	case errors.As(err, &conflict):
		writeDeliveryGroupConflict(w, conflict)
	case errors.As(err, &pre):
		writePRReviewErrorDetails(w, http.StatusPreconditionFailed, pre.Code, pre.Error(), false, map[string]any{
			"expected_revision": pre.ExpectedRevision,
			"stored_revision":   pre.StoredRevision,
		})
	case errors.Is(err, domain.ErrDeliveryGroupInconsistent):
		writePRReviewErrorCode(w, http.StatusServiceUnavailable, domain.DeliveryGroupInconsistentCode, err.Error(), true)
	case errors.Is(err, domain.ErrNotFound):
		writePRReviewErrorCode(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, domain.ErrInvalid):
		writePRReviewErrorCode(w, http.StatusUnprocessableEntity, domain.DeliveryGroupMembersInvalidCode, err.Error(), false)
	default:
		writePRReviewError(w, err)
	}
}

func writeDeliveryGroupConflict(w http.ResponseWriter, conflict *domain.DeliveryGroupConflictError) {
	details := map[string]any{}
	for k, v := range conflict.Meta {
		details[k] = v
	}
	if conflict.PRKey != "" {
		details["pr_key"] = conflict.PRKey
	}
	if conflict.GroupID != "" {
		details["group_id"] = conflict.GroupID
	}
	if conflict.Revision > 0 {
		details["revision"] = conflict.Revision
	}
	writePRReviewErrorDetails(w, http.StatusConflict, conflict.Code, conflict.Error(), conflict.Retryable, details)
}

// groupedPRKeys collects active-group membership so list responses can mark
// standalone discovered PRs (present in GitHub, not in any active group).
func (m *Module) loadActiveGroupedPRKeys(r *http.Request, ws string) (map[string]string, []string, bool) {
	grouped := map[string]string{}
	if m == nil || m.deliveryGroups == nil {
		return grouped, nil, false
	}
	var warnings []string
	cursor := ""
	pages := 0
	const maxPages = 50 // bound Loom-side fanout; still reports has_more honestly via FleetDB
	for {
		pages++
		if pages > maxPages {
			warnings = append(warnings, "delivery group membership index truncated after bounded page walk; some grouped PRs may still appear as standalone")
			return grouped, warnings, true
		}
		page, err := m.deliveryGroups.List(r.Context(), ws, store.DeliveryGroupListOpts{
			State:  "active",
			Limit:  maxDeliveryGroupListLimit,
			Cursor: cursor,
		})
		if err != nil {
			warnings = append(warnings, "delivery groups unavailable: "+sanitizeWarning(err))
			return grouped, warnings, false
		}
		for _, g := range page.Groups {
			if g == nil || g.Inconsistent {
				continue
			}
			for _, member := range g.Members {
				grouped[member.PRKey] = g.ID
			}
		}
		if !page.HasMore {
			return grouped, warnings, false
		}
		if page.NextCursor == "" {
			warnings = append(warnings, "delivery groups reported has_more without next_cursor")
			return grouped, warnings, true
		}
		cursor = page.NextCursor
	}
}

// filterStandalonePullRequests drops PRs that belong to an active delivery
// group. Discovered PRs stay standalone until explicitly added.
func filterStandalonePullRequests(prs []ops.GitPullRequest, grouped map[string]string) []ops.GitPullRequest {
	if len(grouped) == 0 {
		return prs
	}
	out := make([]ops.GitPullRequest, 0, len(prs))
	for _, pr := range prs {
		key := strings.TrimSpace(pr.PRKey)
		if key == "" {
			if parts := strings.Split(pr.RepoName, "/"); len(parts) == 2 {
				key = prref.Format(parts[0], parts[1], pr.Number)
			}
		}
		if key != "" {
			if _, ok := grouped[key]; ok {
				continue
			}
		}
		out = append(out, pr)
	}
	return out
}
