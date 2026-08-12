package fleet

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// --- Unsupported filter validation ---

// checkFleetUnsupportedFilters returns an error wrapping
// workitems.ErrFilterNotSupported if any list fields are set that the
// fleet-db server cannot evaluate server-side. This prevents returning
// unfiltered results when the caller expects filters to be applied.
//
// Fleet-db supported fields: Status, IssueType, Assignee, Labels,
// SourceRepos, ParentID, UpdatedAfter, UpdatedBefore, Limit.
// All others are unsupported (tracked in fleet-qx9c).
func checkFleetUnsupportedFilters(opts workitems.ListFilter) error {
	var unsupported []string
	checkUnsupportedCore(&unsupported, opts)
	checkUnsupportedSearch(&unsupported, opts)
	checkUnsupportedDates(&unsupported, opts)
	checkUnsupportedAdvanced(&unsupported, opts)
	if len(unsupported) > 0 {
		return fmt.Errorf("fleet-db: unsupported filters [%s]: %w",
			strings.Join(unsupported, ", "), workitems.ErrFilterNotSupported)
	}
	return nil
}

func checkUnsupportedCore(out *[]string, opts workitems.ListFilter) {
	if opts.Priority != nil {
		*out = append(*out, "Priority")
	}
	if len(opts.LabelsAny) > 0 {
		*out = append(*out, "LabelsAny")
	}
	// ExternalRef is not yet applied server-side (the reverse PR->card list
	// filter is deferred); fail loud rather than silently returning all issues.
	if opts.ExternalRef != "" {
		*out = append(*out, "ExternalRef")
	}
}

func checkUnsupportedSearch(out *[]string, opts workitems.ListFilter) {
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

func checkUnsupportedDates(out *[]string, opts workitems.ListFilter) {
	if opts.CreatedAfter != "" {
		*out = append(*out, "CreatedAfter")
	}
	if opts.CreatedBefore != "" {
		*out = append(*out, "CreatedBefore")
	}
}

func checkUnsupportedAdvanced(out *[]string, opts workitems.ListFilter) {
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
}

// --- Query parameter builders ---

func listFilterToQuery(opts workitems.ListFilter) string {
	q := url.Values{}
	addListCoreFilters(q, opts)
	addListSearchFilters(q, opts)
	addListDateFilters(q, opts)
	addListAdvancedFilters(q, opts)
	return q.Encode()
}

func listServerOpts(opts workitems.ListFilter) workitems.ListFilter {
	server := opts
	if len(opts.Labels) > 1 {
		server.Labels = nil
	}
	if len(opts.SourceRepos) > 1 {
		server.SourceRepos = nil
	}
	if needsListClientFilter(opts) {
		server.Limit = 0
	}
	return server
}

func needsListClientFilter(opts workitems.ListFilter) bool {
	return len(opts.Labels) > 1 || len(opts.SourceRepos) > 1
}

func addListCoreFilters(q url.Values, opts workitems.ListFilter) {
	setNonEmpty(q, "status", opts.Status)
	setOptInt(q, "priority", opts.Priority)
	// fleet-db's listIssues endpoint expects "type", not "issue_type"
	// (see fleet-db/api/openapi.yaml listIssues). The count endpoint already
	// uses "type" in addCountCoreFilters; this matches it.
	setNonEmpty(q, "type", opts.IssueType)
	setNonEmpty(q, "assignee", opts.Assignee)
	if len(opts.Labels) > 0 {
		q.Set("label", opts.Labels[0])
	}
	addAll(q, "labels_any", opts.LabelsAny)
	setNonEmpty(q, "parent_id", opts.ParentID)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
}

func addListSearchFilters(q url.Values, opts workitems.ListFilter) {
	setNonEmpty(q, "query", opts.Query)
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
	if len(opts.SourceRepos) > 0 {
		q.Set("repo", opts.SourceRepos[0])
	}
}

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
	if len(opts.Labels) > 0 {
		q.Set("label", strings.Join(opts.Labels, ","))
	}
	if len(opts.LabelsAny) > 0 {
		q.Set("label_any", strings.Join(opts.LabelsAny, ","))
	}
	setNonEmpty(q, "mol_type", opts.MolType)
	return q.Encode()
}

func readyServerQuery(opts workitems.AvailabilityQuery) workitems.AvailabilityQuery {
	server := opts
	server.SourceRepos = nil
	if len(opts.SourceRepos) > 0 {
		server.Limit = 0
	}
	return server
}

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

func blockedServerQuery(opts workitems.AvailabilityQuery) workitems.AvailabilityQuery {
	server := opts
	server.Unassigned = false
	server.Labels = nil
	server.LabelsAny = nil
	server.SourceRepos = nil
	if opts.Unassigned || len(opts.Labels) > 0 || len(opts.LabelsAny) > 0 || len(opts.SourceRepos) > 0 {
		server.Limit = 0
	}
	return server
}

// patchCommandToRequest converts the owner command to FleetDB's PATCH request
// format expected by the fleet server (UpdateIssueRequest shape).
//
// fleet-db uses strict JSON validation (disallowUnknownFields); fields not
// present on its UpdateIssueRequest must be omitted entirely, not just
// zeroed. Loom carries a richer field set than fleet-db accepts on PATCH
// (status / claim / labels go through dedicated endpoints), so we drop
// loom-only fields here. If the caller relies on a dropped field landing,
// the corresponding dedicated endpoint should be called instead.
func patchCommandToRequest(params workitems.PatchCommand) map[string]interface{} {
	req := make(map[string]interface{})
	setStrField(req, "title", params.Title)
	setStrField(req, "description", params.Description)
	setIntField(req, "priority", params.Priority)
	setStrField(req, "design", params.Design)
	setStrField(req, "design_format", params.DesignFormat)
	setStrField(req, "notes", params.Notes)
	setStrField(req, "owner", params.Owner)
	// Field rename: Work Items uses "issue_type"; fleet-db's
	// UpdateIssueRequest names the same field "type".
	setStrField(req, "type", params.IssueType)
	setStrField(req, "due_at", params.DueAt)
	setStrField(req, "external_ref", params.ExternalRef)
	return req
}

// createCommandToBody converts the owner command to FleetDB's canonical body.
//
// Dependencies are intentionally absent here too: they are not part of the
// create body. Create composes them after the issue exists via the dedicated
// POST /issues/{id}/deps endpoint (see addCreateDependencies).
func createCommandToBody(command workitems.CreateCommand) map[string]interface{} {
	return command.DurableCreateFields()
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
