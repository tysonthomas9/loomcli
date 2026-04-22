//go:build parity

package paritytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// DualRunner executes a fixture's op sequence against both a beads
// IssueBackend and a fleet IssueBackend, then diffs the responses.
//
// Construction is the caller's responsibility — see spawnBeads + spawnFleetDB.
// This keeps the runner pure (no process lifecycle concerns) and cheap to
// unit-test with mock backends.
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
		}
		if beadsErr == nil {
			captureID(beadsResp, "issue_id", beadsVars)
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
		return issueDataToMap(data), nil

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
		return issueDataToMap(&details.IssueData), nil

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
		return issueDataToMap(cr.Closed), nil

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

	default:
		return nil, fmt.Errorf("paritytest: unsupported method %q", method)
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
func issueDataToMap(d *backend.IssueData) map[string]any {
	if d == nil {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
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

// diffMaps returns one DiffEntry per field where the two backends disagree.
// Fields that are present on only one side are emitted with a null on the
// missing side so downstream tooling can distinguish "different value" from
// "missing field".
//
// Fields considered equal-by-design (created_at, updated_at timestamps) are
// skipped since their values will always drift between backends that issued
// the mutation at microsecond-different times. These ignores are the only
// normalization in the MVP — all other drift surfaces as strict diffs.
func diffMaps(fixtureID, stepID, method string, fleetMap, beadsMap map[string]any) []DiffEntry {
	if fleetMap == nil && beadsMap == nil {
		return nil
	}

	// Fields whose values are expected to diverge by design. created_at /
	// updated_at drift by microseconds between backends. id drifts because
	// each backend assigns its own ID generator. If future fixtures need to
	// lock down ID shape (e.g. PARITY-123 prefix check) they should add a
	// dedicated assertion step instead of re-enabling id here.
	ignored := map[string]bool{
		"created_at": true,
		"updated_at": true,
		"id":         true,
	}

	keys := map[string]bool{}
	for k := range fleetMap {
		keys[k] = true
	}
	for k := range beadsMap {
		keys[k] = true
	}

	// Deterministic order for reproducible reports.
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var diffs []DiffEntry
	for _, k := range sorted {
		if ignored[k] {
			continue
		}
		fv := fleetMap[k]
		bv := beadsMap[k]
		if fieldsEqual(fv, bv) {
			continue
		}
		diffs = append(diffs, DiffEntry{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     k,
			DriftTag:  "strict",
			FleetDB:   fv,
			Beads:     bv,
			Verdict:   "fail",
		})
	}
	return diffs
}

// fieldsEqual checks deep equality on two unmarshaled JSON values, with one
// minor accommodation: empty strings and nil are treated identically (both
// backends serialize a missing string field inconsistently). This matches
// fleet-db's normalizer behavior for string fields.
func fieldsEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if aStr, aOK := a.(string); aOK {
		if bStr, bOK := b.(string); bOK {
			return aStr == bStr
		}
		if b == nil && aStr == "" {
			return true
		}
	}
	if bStr, bOK := b.(string); bOK {
		if a == nil && bStr == "" {
			return true
		}
	}
	return reflect.DeepEqual(a, b)
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
