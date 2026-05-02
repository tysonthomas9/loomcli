//go:build parity

package paritytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// DualRunner executes a fixture's op sequence against two IssueBackend
// implementations and diffs the responses.
//
// Construction is the caller's responsibility. This keeps the runner pure
// (no process lifecycle concerns) and cheap to unit-test with mock backends.
type DualRunner struct {
	beads  backend.IssueBackend
	fleet  backend.IssueBackend
	report *Report
}

// New constructs a DualRunner with two pre-built backends and a report sink.
// Lifecycle (subprocess spawn/teardown) is the caller's responsibility.
func New(beadsBackend, fleetBackend backend.IssueBackend, report *Report) *DualRunner {
	return &DualRunner{
		beads:  beadsBackend,
		fleet:  fleetBackend,
		report: report,
	}
}

// varPattern matches ${name} placeholders for cross-step variable
// substitution. Mirrors fleet-db's pattern so fixtures move unchanged.
var varPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// RunFixture executes the fixture's op sequence against both backends and
// returns the flat list of field-level DiffEntry rows. Each backend
// accumulates its own variable namespace so ${issue_id} resolves to whatever
// that backend reported in the prior step — diffs in ID shape still show up
// because we record the original (unsubstituted) `fleet_db` and `beads`
// values separately.
//
// Returns a Go-level error only for infrastructural failures (invalid
// fixture, nil runner). Per-step errors surface as DiffEntry rows with
// Verdict="fail" and should not fail the test.
func (r *DualRunner) RunFixture(ctx context.Context, fx Fixture) ([]DiffEntry, error) {
	if len(fx.Steps) == 0 {
		return nil, fmt.Errorf("fixture %q: no steps", fx.ID)
	}

	beadsVars := map[string]string{}
	fleetVars := map[string]string{}

	var diffs []DiffEntry
	for _, step := range fx.Steps {
		fleetParams := substituteVars(step.Params, fleetVars)
		beadsParams := substituteVars(step.Params, beadsVars)

		fleetResp, fleetErr := r.executeStep(ctx, r.fleet, step.Method, fleetParams)
		beadsResp, beadsErr := r.executeStep(ctx, r.beads, step.Method, beadsParams)

		// Variable capture — per-backend. For now we only capture the issue
		// `id` field from successful responses; fleet-db's richer
		// result_captures syntax is an extension for future fixtures.
		if fleetErr == nil {
			captureID(fleetResp, "issue_id", fleetVars)
			captureID(fleetResp, step.ID+"_id", fleetVars)
		}
		if beadsErr == nil {
			captureID(beadsResp, "issue_id", beadsVars)
			captureID(beadsResp, step.ID+"_id", beadsVars)
		}

		stepDiffs := diffResponses(fx.ID, step.ID, step.Method, fleetResp, beadsResp, fleetErr, beadsErr)
		diffs = append(diffs, stepDiffs...)
	}

	return diffs, nil
}

// executeStep dispatches step.Method to the right IssueBackend method,
// unmarshals the params JSON into that method's Opts/Params struct, and
// returns a normalized map[string]any so diffResponses can compare across
// backends without caring about Go types. Errors surface as (nil, err)
// and are handled at the diff layer as _outcome diffs.
func (r *DualRunner) executeStep(ctx context.Context, be backend.IssueBackend, method string, rawParams json.RawMessage) (map[string]any, error) {
	switch method {
	case "issue.create":
		var p struct {
			Title       string   `json:"title"`
			Description string   `json:"description,omitempty"`
			Priority    int      `json:"priority,omitempty"`
			Type        string   `json:"type,omitempty"`
			Assignee    string   `json:"assignee,omitempty"`
			Owner       string   `json:"owner,omitempty"`
			Labels      []string `json:"labels,omitempty"`
			Parent      string   `json:"parent,omitempty"`
			Actor       string   `json:"actor,omitempty"` // accepted but unused — adapters supply actor separately
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		data, err := be.Create(ctx, backend.CreateParams{
			Title:       p.Title,
			Description: p.Description,
			Priority:    p.Priority,
			IssueType:   p.Type,
			Assignee:    p.Assignee,
			Owner:       p.Owner,
			Labels:      p.Labels,
			Parent:      p.Parent,
		})
		if err != nil {
			return nil, err
		}
		return issueDataToMap(data)

	case "issue.show":
		var p struct {
			ID string `json:"id"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		details, err := be.Get(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return issueDataToMap(&details.IssueData)

	case "issue.update":
		// Fixtures send flat params (e.g. {"id": "...", "title": "new",
		// "priority": 1}). We split out `id` and unmarshal the rest into
		// UpdateParams. Pointer fields on UpdateParams make partial updates
		// work naturally — absent keys remain nil.
		var idOnly struct {
			ID string `json:"id"`
		}
		if err := unmarshalParams(rawParams, &idOnly); err != nil {
			return nil, err
		}
		// Translate flat fixture shape -> UpdateParams. We map the fixture's
		// bare scalar fields to pointers by using an intermediate struct —
		// json.Unmarshal into pointer fields sets them only when the JSON
		// key is present, which is exactly the semantic we want.
		var flat struct {
			Title       *string `json:"title,omitempty"`
			Description *string `json:"description,omitempty"`
			Status      *string `json:"status,omitempty"`
			Priority    *int    `json:"priority,omitempty"`
			Design      *string `json:"design,omitempty"`
			Notes       *string `json:"notes,omitempty"`
			Assignee    *string `json:"assignee,omitempty"`
			Owner       *string `json:"owner,omitempty"`
			IssueType   *string `json:"type,omitempty"`
			DueAt       *string `json:"due_at,omitempty"`
		}
		if err := unmarshalParams(rawParams, &flat); err != nil {
			return nil, err
		}
		up := backend.UpdateParams{
			Title:       flat.Title,
			Description: flat.Description,
			Status:      flat.Status,
			Priority:    flat.Priority,
			Design:      flat.Design,
			Notes:       flat.Notes,
			Assignee:    flat.Assignee,
			Owner:       flat.Owner,
			IssueType:   flat.IssueType,
			DueAt:       flat.DueAt,
		}
		if err := be.Update(ctx, idOnly.ID, up); err != nil {
			return nil, err
		}
		// Update returns no payload — echo the id so the diff engine sees a
		// trivially-equal response on success. The real signal lives in the
		// follow-up issue.show step.
		return map[string]any{"id": idOnly.ID}, nil

	case "issue.close":
		var p struct {
			ID          string `json:"id"`
			Reason      string `json:"reason,omitempty"`
			Session     string `json:"session,omitempty"`
			SuggestNext bool   `json:"suggest_next,omitempty"`
			Force       bool   `json:"force,omitempty"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		cr, err := be.Close(ctx, p.ID, backend.CloseParams{
			Reason:      p.Reason,
			Session:     p.Session,
			SuggestNext: p.SuggestNext,
			Force:       p.Force,
		})
		if err != nil {
			return nil, err
		}
		if cr == nil || cr.Closed == nil {
			// Backends that don't return a body on close (shouldn't happen, but
			// keeps the runner defensive) still get a sane response map.
			return map[string]any{"id": p.ID}, nil
		}
		return issueDataToMap(cr.Closed)

	case "issue.reopen":
		var p struct {
			ID     string `json:"id"`
			Reason string `json:"reason,omitempty"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		if err := be.Reopen(ctx, p.ID, backend.ReopenParams{Reason: p.Reason}); err != nil {
			return nil, err
		}
		return map[string]any{"id": p.ID}, nil

	case "issue.delete":
		var p struct {
			ID      string   `json:"id,omitempty"`
			IDs     []string `json:"ids,omitempty"`
			Reason  string   `json:"reason,omitempty"`
			Force   bool     `json:"force,omitempty"`
			Cascade bool     `json:"cascade,omitempty"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		ids := p.IDs
		if p.ID != "" {
			ids = append([]string{p.ID}, ids...)
		}
		if err := be.Delete(ctx, backend.DeleteParams{
			IDs:     ids,
			Reason:  p.Reason,
			Force:   p.Force,
			Cascade: p.Cascade,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"ids": ids}, nil

	case "dep.add":
		var p struct {
			FromID  string `json:"from_id"`
			ToID    string `json:"to_id"`
			DepType string `json:"dep_type,omitempty"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		if p.DepType == "" {
			p.DepType = "blocks"
		}
		if err := be.AddDependency(ctx, backend.DepAddParams{
			FromID:  p.FromID,
			ToID:    p.ToID,
			DepType: p.DepType,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "dep.remove":
		var p struct {
			FromID  string `json:"from_id"`
			ToID    string `json:"to_id"`
			DepType string `json:"dep_type,omitempty"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		if p.DepType == "" {
			p.DepType = "blocks"
		}
		if err := be.RemoveDependency(ctx, backend.DepRemoveParams{
			FromID:  p.FromID,
			ToID:    p.ToID,
			DepType: p.DepType,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "comment.add":
		var p struct {
			IssueID string `json:"issue_id"`
			Author  string `json:"author,omitempty"`
			Text    string `json:"text"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		if p.Author == "" {
			p.Author = "parity-harness"
		}
		comment, err := be.AddComment(ctx, backend.CommentAddParams{
			IssueID: p.IssueID,
			Author:  p.Author,
			Text:    p.Text,
		})
		if err != nil {
			return nil, err
		}
		return commentDataToComparableMap(comment), nil

	case "comment.list":
		var p struct {
			IssueID string `json:"issue_id"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		comments, err := be.ListComments(ctx, p.IssueID)
		if err != nil {
			return nil, err
		}
		return commentsToComparableMap(comments), nil

	case "event.list":
		var p struct {
			IssueID string `json:"issue_id"`
			Limit   int    `json:"limit,omitempty"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		events, err := be.ListEvents(ctx, p.IssueID, p.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"count": len(events)}, nil

	case "batch":
		var p struct {
			Ops []backend.BatchOp `json:"ops"`
		}
		if err := unmarshalParams(rawParams, &p); err != nil {
			return nil, err
		}
		results, err := be.Batch(ctx, p.Ops)
		if err != nil {
			return nil, err
		}
		return batchResultsToComparableMap(results), nil

	default:
		return nil, fmt.Errorf("paritytest: unsupported method %q", method)
	}
}

func commentDataToComparableMap(c *backend.CommentData) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{
		"author":         c.Author,
		"text":           c.Text,
		"has_created_at": !c.CreatedAt.IsZero(),
	}
}

func commentsToComparableMap(comments []backend.CommentData) map[string]any {
	texts := make([]string, 0, len(comments))
	authors := make([]string, 0, len(comments))
	hasCreatedAt := make([]bool, 0, len(comments))
	for _, c := range comments {
		texts = append(texts, c.Text)
		authors = append(authors, c.Author)
		hasCreatedAt = append(hasCreatedAt, !c.CreatedAt.IsZero())
	}
	return map[string]any{
		"count":          len(comments),
		"texts":          texts,
		"authors":        authors,
		"has_created_at": hasCreatedAt,
	}
}

func batchResultsToComparableMap(results []backend.BatchResult) map[string]any {
	successes := make([]bool, 0, len(results))
	errorKinds := make([]string, 0, len(results))
	for _, result := range results {
		successes = append(successes, result.Success)
		errorKinds = append(errorKinds, batchErrorKind(result.Error))
	}
	return map[string]any{
		"count":       len(results),
		"successes":   successes,
		"error_kinds": errorKinds,
	}
}

func batchErrorKind(message string) string {
	message = strings.ToLower(message)
	switch {
	case message == "":
		return ""
	case strings.Contains(message, string(backend.KindNotFound)) || strings.Contains(message, "not found"):
		return string(backend.KindNotFound)
	case strings.Contains(message, string(backend.KindConflict)) ||
		strings.Contains(message, "already claimed") ||
		strings.Contains(message, "already closed") ||
		strings.Contains(message, "is closed"):
		return string(backend.KindConflict)
	case strings.Contains(message, string(backend.KindValidation)) ||
		strings.Contains(message, "validation") ||
		strings.Contains(message, "invalid") ||
		strings.Contains(message, "missing id") ||
		strings.Contains(message, "unsupported batch operation"):
		return string(backend.KindValidation)
	case strings.Contains(message, string(backend.KindUnavailable)):
		return string(backend.KindUnavailable)
	case strings.Contains(message, string(backend.KindTimeout)):
		return string(backend.KindTimeout)
	default:
		return string(backend.KindInternal)
	}
}

// unmarshalParams decodes step.Params into the provided struct, treating
// nil/empty JSON as "no params" (some fixtures omit it).
func unmarshalParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// substituteVars replaces ${name} tokens in the raw JSON body using the
// per-backend vars map. Returns the original bytes unchanged if no tokens
// are present.
func substituteVars(raw json.RawMessage, vars map[string]string) json.RawMessage {
	if len(raw) == 0 || len(vars) == 0 {
		return raw
	}
	s := varPattern.ReplaceAllStringFunc(string(raw), func(match string) string {
		name := varPattern.FindStringSubmatch(match)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		return match
	})
	return json.RawMessage(s)
}

// captureID stores the `id` field from an issue response into vars under
// the given key. Silently no-op if the response is nil or has no id.
func captureID(resp map[string]any, key string, vars map[string]string) {
	if resp == nil {
		return
	}
	if id, ok := resp["id"].(string); ok && id != "" {
		vars[key] = id
	}
}

// issueDataToMap turns a strongly-typed backend.IssueData into a
// field-accessible map[string]any by round-tripping through JSON. This
// exactly matches the JSON shape that fixtures reason about and keeps the
// diff logic backend-agnostic.
//
// Returns (nil, nil) for a nil input — that's "no data to convert", not an
// error. Returns (nil, err) on marshal/unmarshal failure so callers surface
// the problem as an _outcome diff rather than silently producing a zero
// map that would compare equal to whatever the other backend reported (a
// false-pass).
func issueDataToMap(d *backend.IssueData) (map[string]any, error) {
	if d == nil {
		return nil, nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal IssueData: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal IssueData: %w", err)
	}
	return m, nil
}

// diffResponses compares two normalized responses (or their errors) and
// emits DiffEntry rows. Semantics:
//
//   - both succeeded → compare every field; emit one row per differing field
//   - both errored   → compare ErrorKind + message; emit one _error_kind row
//     if they differ
//   - one succeeded, one errored → emit a single _outcome row (hard diff)
//   - both succeeded identical   → no rows (noise-free report)
//
// All emitted rows use DriftTag="strict" — normalization/waivers are
// reserved for future fixtures.
func diffResponses(fixtureID, stepID, method string, fleetResp, beadsResp map[string]any, fleetErr, beadsErr error) []DiffEntry {
	fleetOK := fleetErr == nil
	beadsOK := beadsErr == nil

	// Outcome mismatch.
	if fleetOK != beadsOK {
		return []DiffEntry{{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     "_outcome",
			DriftTag:  "strict",
			FleetDB:   describeOutcome(fleetResp, fleetErr),
			Beads:     describeOutcome(beadsResp, beadsErr),
			Verdict:   "fail",
		}}
	}

	// Both errored.
	if !fleetOK && !beadsOK {
		fleetKind := errorKind(fleetErr)
		beadsKind := errorKind(beadsErr)
		if fleetKind == beadsKind {
			return nil // same error class — we don't drill into messages yet
		}
		return []DiffEntry{{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     "_error_kind",
			DriftTag:  "strict",
			FleetDB:   fleetKind,
			Beads:     beadsKind,
			Verdict:   "fail",
		}}
	}

	// Both succeeded — field-by-field compare.
	return diffMaps(fixtureID, stepID, method, fleetResp, beadsResp)
}

// rpcDiffIgnored is the RPC-level diff ignore set. created_at / updated_at
// drift by microseconds between backends. id drifts because each backend
// assigns its own ID generator. If future fixtures need to lock down ID
// shape (e.g. PARITY-123 prefix check) they should add a dedicated
// assertion step instead of re-enabling id here.
var rpcDiffIgnored = map[string]bool{
	"created_at": true,
	"updated_at": true,
	"id":         true,
}

// diffMaps delegates to the shared DiffMaps routine with the RPC-level
// ignore set. Fields present on only one side are emitted with nil on the
// missing side so downstream tooling can distinguish "different value"
// from "missing field". DriftTag is "strict" — normalization/waivers are
// reserved for future fixtures.
//
// The field-equality semantics (nil/empty-string equivalence) live in
// diffcore.go's defaultFieldsEqual; callers that need additional
// normalization should supply DiffOpts.Equal directly.
func diffMaps(fixtureID, stepID, method string, fleetMap, beadsMap map[string]any) []DiffEntry {
	return DiffMaps(DiffOpts{Ignored: rpcDiffIgnored}, fixtureID, stepID, method, fleetMap, beadsMap)
}

// describeOutcome formats a human-readable string for an outcome mismatch
// row: either "success(<id>)" or "error(<kind>: <msg>)".
func describeOutcome(resp map[string]any, err error) string {
	if err != nil {
		return fmt.Sprintf("error(%s: %s)", errorKind(err), err.Error())
	}
	if id, ok := resp["id"].(string); ok && id != "" {
		return fmt.Sprintf("success(id=%s)", id)
	}
	return "success"
}

// errorKind returns a stable string name for a *backend.BackendError kind,
// or "unknown" for errors that don't unwrap to one. This is the diff
// axis for cross-backend error-classification drift.
func errorKind(err error) string {
	if err == nil {
		return ""
	}
	var be *backend.BackendError
	if errors.As(err, &be) {
		return string(be.Kind)
	}
	return "unknown"
}
