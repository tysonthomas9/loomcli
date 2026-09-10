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
// backend.ErrFilterNotSupported if any ListOpts fields are set that neither the
// fleet-db server nor List's own client-side pass can evaluate. This prevents
// returning unfiltered results when the caller expects filters to be applied.
//
// Evaluated server-side: Status, IssueType, Assignee, Labels, SourceRepos,
// ParentID, Priority, UpdatedAfter, UpdatedBefore, Limit.
//
// Evaluated client-side by List, over the complete paged result set (see
// list_filters.go): CreatedAfter, CreatedBefore, Query, TitleContains,
// DescriptionContains, NotesContains, EmptyDescription, NoAssignee, NoLabels,
// Pinned.
//
// Everything still listed below is genuinely unimplemented on this route.
//
// The error is a *backend.BackendError of KindValidation so the webui's
// translateBackendError can classify it — as a bare fmt.Errorf it fell through
// to a 500. Unwrap() returns the cause, so errors.Is(err,
// ErrFilterNotSupported) and the CLI's message are both unchanged.
func checkFleetUnsupportedFilters(opts backend.ListOpts) error {
	var unsupported []string
	checkUnsupportedCore(&unsupported, opts)
	checkUnsupportedDates(&unsupported, opts)
	checkUnsupportedAdvanced(&unsupported, opts)
	if len(unsupported) > 0 {
		msg := fmt.Sprintf("fleet-db: unsupported filters [%s]", strings.Join(unsupported, ", "))
		return backend.NewBackendError(backend.KindValidation, "List", msg,
			backend.ErrFilterNotSupported)
	}
	return nil
}

func checkUnsupportedCore(out *[]string, opts backend.ListOpts) {
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

func checkUnsupportedDates(out *[]string, opts backend.ListOpts) {
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
	addListDateFilters(q, opts)
	addListAdvancedFilters(q, opts)
	return q.Encode()
}

func listServerOpts(opts backend.ListOpts) backend.ListOpts {
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

// needsListClientFilter reports whether List has work to do after the server
// answers. Its other job is to make listServerOpts zero the server-side limit:
// without that, `?limit=10&no_assignee=true` returns the unassigned subset of
// the first ten rows instead of ten unassigned rows.
func needsListClientFilter(opts backend.ListOpts) bool {
	return len(opts.Labels) > 1 || len(opts.SourceRepos) > 1 ||
		opts.CreatedAfter != "" || opts.CreatedBefore != "" ||
		opts.Query != "" || opts.TitleContains != "" ||
		opts.DescriptionContains != "" || opts.NotesContains != "" ||
		opts.EmptyDescription || opts.NoAssignee || opts.NoLabels || opts.Pinned != nil
}

func addListCoreFilters(q url.Values, opts backend.ListOpts) {
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
	if opts.Offset > 0 {
		q.Set("offset", strconv.Itoa(opts.Offset))
	}
	addAll(q, "ids", opts.IDs)
}

// addListDateFilters emits only the date filters fleet-db actually evaluates.
// created_after/created_before are absent on purpose: fleet-db ignores them, and
// sending them is precisely what made the wire look filtered when it was not.
// List applies them client-side instead.
//
// updated_* are normalized to RFC3339 because fleet-db parses them strictly and
// 400s on the bare YYYY-MM-DD that this layer accepts.
func addListDateFilters(q url.Values, opts backend.ListOpts) {
	setNonEmpty(q, "updated_after", normalizeFleetDate(opts.UpdatedAfter))
	setNonEmpty(q, "updated_before", normalizeFleetDate(opts.UpdatedBefore))
	setNonEmpty(q, "closed_after", opts.ClosedAfter)
	setNonEmpty(q, "closed_before", opts.ClosedBefore)
	setNonEmpty(q, "defer_after", opts.DeferAfter)
	setNonEmpty(q, "defer_before", opts.DeferBefore)
	setNonEmpty(q, "due_after", opts.DueAfter)
	setNonEmpty(q, "due_before", opts.DueBefore)
}

// addListAdvancedFilters omits empty_description, no_assignee, no_labels,
// pinned and the priority_min/max pair: fleet-db's list route has no such
// parameters, so emitting them only made the request look filtered.
func addListAdvancedFilters(q url.Values, opts backend.ListOpts) {
	setOptBool(q, "ephemeral", opts.Ephemeral)
	setBoolIfTrue(q, "include_templates", opts.IncludeTemplates)
	setNonEmpty(q, "mol_type", opts.MolType)
	addAll(q, "exclude_status", opts.ExcludeStatus)
	addAll(q, "exclude_types", opts.ExcludeTypes)
	setBoolIfTrue(q, "deferred", opts.Deferred)
	setBoolIfTrue(q, "overdue", opts.Overdue)
	if len(opts.SourceRepos) > 0 {
		q.Set("repo", opts.SourceRepos[0])
	}
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
	if len(opts.Labels) > 0 {
		q.Set("label", strings.Join(opts.Labels, ","))
	}
	if len(opts.LabelsAny) > 0 {
		q.Set("label_any", strings.Join(opts.LabelsAny, ","))
	}
	setNonEmpty(q, "mol_type", opts.MolType)
	return q.Encode()
}

func readyServerOpts(opts backend.ReadyOpts) backend.ReadyOpts {
	server := opts
	server.SourceRepos = nil
	if len(opts.SourceRepos) > 0 {
		server.Limit = 0
	}
	return server
}

// countOptsToQuery serializes the subset of backend.CountOpts fields that the
// fleet-db count endpoint evaluates server-side (see openapi.yaml
// /issues/count). Callers validate fields that would otherwise be lossy via
// checkFleetUnsupportedCountFilters before invoking this serializer.
//
// Supported: Status, IssueType (mapped to "type"), Assignee, Labels
// (mapped to singular "label"; fleet's count endpoint accepts a single label
// filter, not a list), SourceRepos (mapped to singular "repo"), GroupBy.
func countOptsToQuery(opts backend.CountOpts) string {
	q := url.Values{}
	setNonEmpty(q, "status", opts.Status)
	setNonEmpty(q, "type", opts.IssueType)
	setNonEmpty(q, "assignee", opts.Assignee)
	// fleet-db's count endpoint takes a single label/repo; validation rejects
	// multi-value inputs before this serializer runs.
	if len(opts.Labels) > 0 {
		q.Set("label", opts.Labels[0])
	}
	if len(opts.SourceRepos) > 0 {
		q.Set("repo", opts.SourceRepos[0])
	}
	setNonEmpty(q, "group_by", opts.GroupBy)
	return q.Encode()
}

func checkFleetUnsupportedCountFilters(opts backend.CountOpts) error {
	var unsupported []string
	if len(opts.Labels) > 1 {
		unsupported = append(unsupported, "Labels")
	}
	if len(opts.SourceRepos) > 1 {
		unsupported = append(unsupported, "SourceRepos")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("fleet-db: unsupported count filters [%s]: %w",
			strings.Join(unsupported, ", "), backend.ErrFilterNotSupported)
	}
	return nil
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

func blockedServerOpts(opts backend.BlockedOpts) backend.BlockedOpts {
	server := opts
	server.Labels = nil
	server.SourceRepos = nil
	if len(opts.Labels) > 0 || len(opts.SourceRepos) > 0 {
		server.Limit = 0
	}
	return server
}

// updateParamsToPatchRequest converts backend.UpdateParams to the PATCH request
// format expected by the fleet server (UpdateIssueRequest shape).
//
// fleet-db uses strict JSON validation (disallowUnknownFields); fields not
// present on its UpdateIssueRequest must be omitted entirely, not just
// zeroed. Loom carries a richer field set than fleet-db accepts on PATCH
// (status / claim / labels go through dedicated endpoints), so we drop
// loom-only fields here. If the caller relies on a dropped field landing,
// the corresponding dedicated endpoint should be called instead.
func updateParamsToPatchRequest(params backend.UpdateParams) map[string]interface{} {
	req := make(map[string]interface{})
	setStrField(req, "title", params.Title)
	setStrField(req, "description", params.Description)
	setIntField(req, "priority", params.Priority)
	setStrField(req, "design", params.Design)
	setStrField(req, "design_format", params.DesignFormat)
	setStrField(req, "notes", params.Notes)
	setStrField(req, "owner", params.Owner)
	// Field rename: loom's IssueBackend uses "issue_type"; fleet-db's
	// UpdateIssueRequest names the same field "type".
	setStrField(req, "type", params.IssueType)
	setStrField(req, "due_at", params.DueAt)
	setStrField(req, "external_ref", params.ExternalRef)
	return req
}

// createParamsToBody converts backend.CreateParams to the POST /issues body
// shape fleet-db's CreateIssueRequest expects. The projection itself lives on
// backend.CreateParams (FleetCreateBody) because the CLI hashes the identical
// bytes into the default X-Idempotency-Key -- cli/data may not import this
// package (depguard data-isolation), so the shared source of truth sits in
// the backend package.
//
// Dependencies are intentionally absent here too: they are not part of the
// create body. Create composes them after the issue exists via the dedicated
// POST /issues/{id}/deps endpoint (see addCreateDependencies).
func createParamsToBody(params backend.CreateParams) map[string]interface{} {
	return params.FleetCreateBody()
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
