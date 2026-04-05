package fleet

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- Unsupported filter validation ---

// checkFleetUnsupportedFilters returns an error wrapping
// backend.ErrFilterNotSupported if any ListOpts fields are set that the
// fleet-db server cannot evaluate server-side. This prevents returning
// unfiltered results when the caller expects filters to be applied.
//
// Fleet-db supported fields: Status, IssueType, Assignee, Labels,
// SourceRepos, ParentID, UpdatedAfter, UpdatedBefore, Limit.
// All others are unsupported (tracked in fleet-qx9c).
func checkFleetUnsupportedFilters(opts backend.ListOpts) error {
	var unsupported []string
	checkUnsupportedCore(&unsupported, opts)
	checkUnsupportedSearch(&unsupported, opts)
	checkUnsupportedDates(&unsupported, opts)
	checkUnsupportedAdvanced(&unsupported, opts)
	if len(unsupported) > 0 {
		return fmt.Errorf("fleet-db: unsupported filters [%s]: %w",
			strings.Join(unsupported, ", "), backend.ErrFilterNotSupported)
	}
	return nil
}

func checkUnsupportedCore(out *[]string, opts backend.ListOpts) {
	if opts.Priority != nil {
		*out = append(*out, "Priority")
	}
	if len(opts.LabelsAny) > 0 {
		*out = append(*out, "LabelsAny")
	}
	if len(opts.IDs) > 0 {
		*out = append(*out, "IDs")
	}
	if opts.PriorityMin != nil {
		*out = append(*out, "PriorityMin")
	}
	if opts.PriorityMax != nil {
		*out = append(*out, "PriorityMax")
	}
}

func checkUnsupportedSearch(out *[]string, opts backend.ListOpts) {
	if opts.Query != "" {
		*out = append(*out, "Query")
	}
	if opts.TitleContains != "" {
		*out = append(*out, "TitleContains")
	}
	if opts.DescriptionContains != "" {
		*out = append(*out, "DescriptionContains")
	}
	if opts.NotesContains != "" {
		*out = append(*out, "NotesContains")
	}
}

func checkUnsupportedDates(out *[]string, opts backend.ListOpts) {
	if opts.CreatedAfter != "" {
		*out = append(*out, "CreatedAfter")
	}
	if opts.CreatedBefore != "" {
		*out = append(*out, "CreatedBefore")
	}
	if opts.ClosedAfter != "" {
		*out = append(*out, "ClosedAfter")
	}
	if opts.ClosedBefore != "" {
		*out = append(*out, "ClosedBefore")
	}
	if opts.DeferAfter != "" {
		*out = append(*out, "DeferAfter")
	}
	if opts.DeferBefore != "" {
		*out = append(*out, "DeferBefore")
	}
	if opts.DueAfter != "" {
		*out = append(*out, "DueAfter")
	}
	if opts.DueBefore != "" {
		*out = append(*out, "DueBefore")
	}
}

func checkUnsupportedAdvanced(out *[]string, opts backend.ListOpts) {
	if opts.EmptyDescription {
		*out = append(*out, "EmptyDescription")
	}
	if opts.NoAssignee {
		*out = append(*out, "NoAssignee")
	}
	if opts.NoLabels {
		*out = append(*out, "NoLabels")
	}
	if opts.Pinned != nil {
		*out = append(*out, "Pinned")
	}
	if opts.IncludeTemplates {
		*out = append(*out, "IncludeTemplates")
	}
	if opts.Ephemeral != nil {
		*out = append(*out, "Ephemeral")
	}
	if opts.MolType != "" {
		*out = append(*out, "MolType")
	}
	if len(opts.ExcludeStatus) > 0 {
		*out = append(*out, "ExcludeStatus")
	}
	if len(opts.ExcludeTypes) > 0 {
		*out = append(*out, "ExcludeTypes")
	}
	if opts.Deferred {
		*out = append(*out, "Deferred")
	}
	if opts.Overdue {
		*out = append(*out, "Overdue")
	}
	if opts.AllowStale {
		*out = append(*out, "AllowStale")
	}
}

// --- Query parameter builders ---

func listOptsToQuery(opts backend.ListOpts) string {
	q := url.Values{}
	addListCoreFilters(q, opts)
	addListSearchFilters(q, opts)
	addListDateFilters(q, opts)
	addListAdvancedFilters(q, opts)
	return q.Encode()
}

func addListCoreFilters(q url.Values, opts backend.ListOpts) {
	setNonEmpty(q, "status", opts.Status)
	setOptInt(q, "priority", opts.Priority)
	setNonEmpty(q, "issue_type", opts.IssueType)
	setNonEmpty(q, "assignee", opts.Assignee)
	addAll(q, "labels", opts.Labels)
	addAll(q, "labels_any", opts.LabelsAny)
	setNonEmpty(q, "parent_id", opts.ParentID)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	addAll(q, "ids", opts.IDs)
}

func addListSearchFilters(q url.Values, opts backend.ListOpts) {
	setNonEmpty(q, "query", opts.Query)
	setNonEmpty(q, "title_contains", opts.TitleContains)
	setNonEmpty(q, "description_contains", opts.DescriptionContains)
	setNonEmpty(q, "notes_contains", opts.NotesContains)
}

func addListDateFilters(q url.Values, opts backend.ListOpts) {
	setNonEmpty(q, "created_after", opts.CreatedAfter)
	setNonEmpty(q, "created_before", opts.CreatedBefore)
	setNonEmpty(q, "updated_after", opts.UpdatedAfter)
	setNonEmpty(q, "updated_before", opts.UpdatedBefore)
	setNonEmpty(q, "closed_after", opts.ClosedAfter)
	setNonEmpty(q, "closed_before", opts.ClosedBefore)
	setNonEmpty(q, "defer_after", opts.DeferAfter)
	setNonEmpty(q, "defer_before", opts.DeferBefore)
	setNonEmpty(q, "due_after", opts.DueAfter)
	setNonEmpty(q, "due_before", opts.DueBefore)
}

func addListAdvancedFilters(q url.Values, opts backend.ListOpts) {
	setBoolIfTrue(q, "empty_description", opts.EmptyDescription)
	setBoolIfTrue(q, "no_assignee", opts.NoAssignee)
	setBoolIfTrue(q, "no_labels", opts.NoLabels)
	setOptInt(q, "priority_min", opts.PriorityMin)
	setOptInt(q, "priority_max", opts.PriorityMax)
	setOptBool(q, "pinned", opts.Pinned)
	setOptBool(q, "ephemeral", opts.Ephemeral)
	setBoolIfTrue(q, "include_templates", opts.IncludeTemplates)
	setNonEmpty(q, "mol_type", opts.MolType)
	addAll(q, "exclude_status", opts.ExcludeStatus)
	addAll(q, "exclude_types", opts.ExcludeTypes)
	setBoolIfTrue(q, "deferred", opts.Deferred)
	setBoolIfTrue(q, "overdue", opts.Overdue)
	addAll(q, "source_repos", opts.SourceRepos)
	setBoolIfTrue(q, "allow_stale", opts.AllowStale)
}

func readyOptsToQuery(opts backend.ReadyOpts) string {
	q := url.Values{}
	setNonEmpty(q, "assignee", opts.Assignee)
	setBoolIfTrue(q, "unassigned", opts.Unassigned)
	setOptInt(q, "priority", opts.Priority)
	setNonEmpty(q, "type", opts.Type)
	setNonEmpty(q, "parent_id", opts.ParentID)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	setNonEmpty(q, "sort", opts.SortPolicy)
	addAll(q, "labels", opts.Labels)
	addAll(q, "labels_any", opts.LabelsAny)
	setNonEmpty(q, "mol_type", opts.MolType)
	setBoolIfTrue(q, "include_deferred", opts.IncludeDeferred)
	addAll(q, "source_repos", opts.SourceRepos)
	return q.Encode()
}

func blockedOptsToQuery(opts backend.BlockedOpts) string {
	q := url.Values{}
	setNonEmpty(q, "parent_id", opts.ParentID)
	setNonEmpty(q, "assignee", opts.Assignee)
	setOptInt(q, "priority", opts.Priority)
	setNonEmpty(q, "type", opts.Type)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	return q.Encode()
}

// updateParamsToPatchRequest converts backend.UpdateParams to the PATCH request
// format expected by the fleet server (PatchIssueRequest shape).
func updateParamsToPatchRequest(params backend.UpdateParams) map[string]interface{} {
	req := make(map[string]interface{})
	setStrField(req, "title", params.Title)
	setStrField(req, "description", params.Description)
	setStrField(req, "status", params.Status)
	setIntField(req, "priority", params.Priority)
	setStrField(req, "design", params.Design)
	setStrField(req, "acceptance_criteria", params.AcceptanceCriteria)
	setStrField(req, "notes", params.Notes)
	setStrField(req, "assignee", params.Assignee)
	setStrField(req, "owner", params.Owner)
	setStrField(req, "issue_type", params.IssueType)
	setStrField(req, "external_ref", params.ExternalRef)
	setIntField(req, "estimated_minutes", params.EstimatedMinutes)
	if len(params.AddLabels) > 0 {
		req["add_labels"] = params.AddLabels
	}
	if len(params.RemoveLabels) > 0 {
		req["remove_labels"] = params.RemoveLabels
	}
	if len(params.SetLabels) > 0 {
		req["set_labels"] = params.SetLabels
	}
	setStrField(req, "parent", params.Parent)
	setStrField(req, "due_at", params.DueAt)
	setStrField(req, "defer_until", params.DeferUntil)
	return req
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

func addAll(q url.Values, key string, vals []string) {
	for _, v := range vals {
		q.Add(key, v)
	}
}

func setStrField(m map[string]interface{}, key string, val *string) {
	if val != nil {
		m[key] = *val
	}
}

func setIntField(m map[string]interface{}, key string, val *int) {
	if val != nil {
		m[key] = *val
	}
}
