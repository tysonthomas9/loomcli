package httpapi

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

func checkBlockedQuerySupported(query workitems.AvailabilityQuery) error {
	var unsupported []string
	if query.Unassigned {
		unsupported = append(unsupported, "Unassigned")
	}
	if len(query.Labels) > 0 {
		unsupported = append(unsupported, "Labels")
	}
	if len(query.LabelsAny) > 0 {
		unsupported = append(unsupported, "LabelsAny")
	}
	if len(query.SourceRepos) > 0 {
		unsupported = append(unsupported, "SourceRepos")
	}
	if query.SortPolicy != "" {
		unsupported = append(unsupported, "SortPolicy")
	}
	if query.MolType != "" {
		unsupported = append(unsupported, "MolType")
	}
	if len(unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("loom API: unsupported blocked filters [%s]: %w",
		strings.Join(unsupported, ", "), workitems.ErrFilterNotSupported)
}

// listQueryToQuery builds the canonical query string for GET /issues.
func listQueryToQuery(query workitems.ListQuery) string {
	q := url.Values{}
	opts := query.Filter
	addListCoreFilters(q, opts)
	addListSearchFilters(q, opts)
	addListDateFilters(q, opts)
	addListAdvancedFilters(q, opts)
	joinCSV(q, "exclude_status", query.ExcludeStatus)
	return q.Encode()
}

func addListCoreFilters(q url.Values, opts workitems.ListFilter) {
	setNonEmpty(q, "status", opts.Status)
	setOptInt(q, "priority", opts.Priority)
	setNonEmpty(q, "type", opts.IssueType)
	setNonEmpty(q, "assignee", opts.Assignee)
	setNonEmpty(q, "parent_id", opts.ParentID)
	joinCSV(q, "labels", opts.Labels)
	joinCSV(q, "source_repos", opts.SourceRepos)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
}

func addListSearchFilters(q url.Values, opts workitems.ListFilter) {
	setNonEmpty(q, "q", opts.Query)
	setNonEmpty(q, "title_contains", opts.TitleContains)
	setNonEmpty(q, "description_contains", opts.DescriptionContains)
	setNonEmpty(q, "notes_contains", opts.NotesContains)
}

func addListDateFilters(q url.Values, opts workitems.ListFilter) {
	setNonEmpty(q, "created_after", opts.CreatedAfter)
	setNonEmpty(q, "created_before", opts.CreatedBefore)
	setNonEmpty(q, "updated_after", opts.UpdatedAfter)
	setNonEmpty(q, "updated_before", opts.UpdatedBefore)
}

func addListAdvancedFilters(q url.Values, opts workitems.ListFilter) {
	setBoolIfTrue(q, "empty_description", opts.EmptyDescription)
	setBoolIfTrue(q, "no_assignee", opts.NoAssignee)
	setBoolIfTrue(q, "no_labels", opts.NoLabels)
	setOptBool(q, "pinned", opts.Pinned)
}

// readyQueryToQuery builds the query string for GET /ready.
func readyQueryToQuery(opts workitems.AvailabilityQuery) string {
	q := url.Values{}
	setNonEmpty(q, "assignee", opts.Assignee)
	setBoolIfTrue(q, "unassigned", opts.Unassigned)
	setOptInt(q, "priority", opts.Priority)
	setNonEmpty(q, "type", opts.IssueType)
	setNonEmpty(q, "parent_id", opts.ParentID)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	setNonEmpty(q, "sort", opts.SortPolicy)
	joinCSV(q, "labels", opts.Labels)
	joinCSV(q, "labels_any", opts.LabelsAny)
	setNonEmpty(q, "mol_type", opts.MolType)
	joinCSV(q, "source_repos", opts.SourceRepos)
	return q.Encode()
}

// blockedQueryToQuery builds the query string for GET /blocked from the Work
// Items owner projection.
func blockedQueryToQuery(opts workitems.AvailabilityQuery) string {
	q := url.Values{}
	setNonEmpty(q, "parent_id", opts.ParentID)
	setNonEmpty(q, "assignee", opts.Assignee)
	setOptInt(q, "priority", opts.Priority)
	setNonEmpty(q, "type", opts.IssueType)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	return q.Encode()
}

// patchCommandToRequest converts a Work Items command to a generated
// PatchIssueRequest, using pointer semantics to distinguish "set" from
// "leave alone". The Claim field is rejected at the caller level since the
// server does not yet expose a per-issue claim field (see task loomcli-j7qcq).
func patchCommandToRequest(params workitems.PatchCommand) gen.PatchIssueRequest {
	req := gen.PatchIssueRequest{
		Title:              params.Title,
		Description:        params.Description,
		Priority:           params.Priority,
		Design:             params.Design,
		DesignFormat:       (*gen.PatchIssueRequestDesignFormat)(params.DesignFormat),
		AcceptanceCriteria: params.AcceptanceCriteria,
		Notes:              params.Notes,
		Assignee:           params.Assignee,
		Owner:              params.Owner,
		IssueType:          params.IssueType,
		ExternalRef:        params.ExternalRef,
		EstimatedMinutes:   params.EstimatedMinutes,
		Parent:             params.Parent,
		DueAt:              params.DueAt,
		DeferUntil:         params.DeferUntil,
	}
	if params.Status != nil {
		status := gen.PatchIssueRequestStatus(*params.Status)
		req.Status = &status
	}
	if params.AgentState != nil {
		state := gen.PatchIssueRequestAgentState(*params.AgentState)
		req.AgentState = &state
	}
	if len(params.AddLabels) > 0 {
		labels := append([]string(nil), params.AddLabels...)
		req.AddLabels = &labels
	}
	if len(params.RemoveLabels) > 0 {
		labels := append([]string(nil), params.RemoveLabels...)
		req.RemoveLabels = &labels
	}
	if len(params.SetLabels) > 0 {
		labels := append([]string(nil), params.SetLabels...)
		req.SetLabels = &labels
	}
	return req
}

// createCommandToRequest converts a Work Items command to a generated
// CreateIssueRequest.
func createCommandToRequest(params workitems.CreateCommand) gen.CreateIssueRequest {
	req := gen.CreateIssueRequest{
		Title:     params.Title,
		IssueType: gen.CreateIssueRequestIssueType(params.IssueType),
		Priority:  params.Priority,
	}
	setCreateStringFields(&req, params)
	if params.EstimatedMinutes != nil {
		req.EstimatedMinutes = params.EstimatedMinutes
	}
	if len(params.Labels) > 0 {
		labels := append([]string(nil), params.Labels...)
		req.Labels = &labels
	}
	if len(params.Dependencies) > 0 {
		deps := append([]string(nil), params.Dependencies...)
		req.Dependencies = &deps
	}
	return req
}

// setCreateStringFields copies the optional string-typed fields from
// CreateParams into the generated CreateIssueRequest. Extracted from
// createParamsToCreateRequest to keep funlen happy.
func setCreateStringFields(req *gen.CreateIssueRequest, params workitems.CreateCommand) {
	if params.ID != "" {
		req.Id = &params.ID
	}
	if params.Parent != "" {
		req.Parent = &params.Parent
	}
	if params.Description != "" {
		req.Description = &params.Description
	}
	if params.Status != "" {
		status := gen.CreateIssueRequestStatus(params.Status)
		req.Status = &status
	}
	if params.Design != "" {
		req.Design = &params.Design
	}
	if params.AcceptanceCriteria != "" {
		req.AcceptanceCriteria = &params.AcceptanceCriteria
	}
	if params.Notes != "" {
		req.Notes = &params.Notes
	}
	if params.Assignee != "" {
		req.Assignee = &params.Assignee
	}
	if params.Owner != "" {
		req.Owner = &params.Owner
	}
	if params.CreatedBy != "" {
		req.CreatedBy = &params.CreatedBy
	}
	if params.ExternalRef != "" {
		req.ExternalRef = &params.ExternalRef
	}
	if params.SourceRepo != "" {
		req.SourceRepo = &params.SourceRepo
	}
	if params.DueAt != "" {
		req.DueAt = &params.DueAt
	}
	if params.DeferUntil != "" {
		req.DeferUntil = &params.DeferUntil
	}
}

// --- Helpers ---

func setNonEmpty(q url.Values, key, val string) {
	if val != "" {
		q.Set(key, val)
	}
}

func setOptInt(q url.Values, key string, val *int) {
	if val != nil {
		q.Set(key, strconv.Itoa(*val))
	}
}

func setOptBool(q url.Values, key string, val *bool) {
	if val != nil {
		q.Set(key, strconv.FormatBool(*val))
	}
}

func setBoolIfTrue(q url.Values, key string, val bool) {
	if val {
		q.Set(key, "true")
	}
}

// joinCSV sets a single query parameter as a comma-separated list of values.
// No-op if vals is empty. Matches the server's parseCSVParam handling at
// internal/webui/handlers_ready.go and handlers_issues.go.
func joinCSV(q url.Values, key string, vals []string) {
	if len(vals) == 0 {
		return
	}
	joined := vals[0]
	for _, v := range vals[1:] {
		joined += "," + v
	}
	q.Set(key, joined)
}
