//go:build parity

package paritytest

import (
	"reflect"
	"sort"
)

// DiffOpts tunes how DiffMaps compares two maps. Callers supply their own
// ignore sets and aliases; the core walk (collect keys → sort → compare →
// emit) is shared.
//
// Ignored keys are skipped after aliasing. Aliases rewrite each key to a
// canonical name before diffing — the post-alias map is what Ignored
// consults. Equal, if non-nil, is called per-field to decide equality; if
// it returns true for any field, that field is considered matched.
// Otherwise DiffMaps falls back to a default equality check that treats
// nil and empty-string as equivalent (fixtures have historically seen
// both on the same "missing optional" field).
type DiffOpts struct {
	Ignored map[string]bool
	Aliases map[string]string
	// NormalizeMap, if set, is applied to each side before diffing. The
	// CLI path uses this to hoist nested shapes (fleet-db's
	// {issue: {...}, blockers: [...]} wrapper) up to flat form.
	NormalizeMap func(map[string]any) map[string]any
	Equal        func(field string, a, b any) bool
}

// DiffMaps walks two maps and emits one DiffEntry per disagreeing field.
// See runner.go's diffMaps and cli_parity_test.go's diffMapsCLI for the
// two concrete ignore/alias sets used in practice.
//
// The caller supplies fixtureID/stepID/method for the emitted entries so
// the shared routine doesn't need to know about the outer test structure.
// DriftTag is fixed to "strict" — if future fixtures need waived or
// normalized rows, this is the seam to add a per-entry override.
func DiffMaps(opts DiffOpts, fixtureID, stepID, method string, left, right map[string]any) []DiffEntry {
	if left == nil && right == nil {
		return nil
	}

	// NormalizeMap first (hoist nested wrappers), then alias, then ignore.
	// The order matters: hoisting can surface fields that were only
	// visible under a nested key, and aliases collapse synonyms so the
	// ignore set doesn't need to list both names.
	left = applyAliases(applyNormalize(opts, left), opts.Aliases)
	right = applyAliases(applyNormalize(opts, right), opts.Aliases)

	keys := map[string]struct{}{}
	for k := range left {
		keys[k] = struct{}{}
	}
	for k := range right {
		keys[k] = struct{}{}
	}

	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	eq := opts.Equal
	if eq == nil {
		eq = defaultFieldsEqual
	}

	var diffs []DiffEntry
	for _, k := range sorted {
		if opts.Ignored[k] {
			continue
		}
		lv := left[k]
		rv := right[k]
		if eq(k, lv, rv) {
			continue
		}
		diffs = append(diffs, DiffEntry{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     k,
			DriftTag:  "strict",
			// Convention across the harness: the "left" slot is fleet_db,
			// the "right" slot is beads. CLI callers pass (fdb, bd); RPC
			// callers pass (fleet, beads). Maintaining this convention
			// keeps the emitted JSON shape consistent.
			FleetDB: lv,
			Beads:   rv,
			Verdict: "fail",
		})
	}
	return diffs
}

// applyNormalize returns opts.NormalizeMap(m) if NormalizeMap is set,
// otherwise m verbatim. Kept tiny so callers of DiffMaps don't need a
// nil-check at the call site.
func applyNormalize(opts DiffOpts, m map[string]any) map[string]any {
	if opts.NormalizeMap == nil {
		return m
	}
	return opts.NormalizeMap(m)
}

// applyAliases rewrites keys in m using aliases. The original map is not
// mutated. If an alias collides with an existing key, the first writer
// wins — callers that need a different policy should pre-normalize.
func applyAliases(m map[string]any, aliases map[string]string) map[string]any {
	if len(aliases) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if alias, ok := aliases[k]; ok {
			if _, exists := out[alias]; !exists {
				out[alias] = v
			}
			continue
		}
		out[k] = v
	}
	return out
}

// defaultFieldsEqual is DiffOpts.Equal when the caller doesn't set one.
// Handles the nil/empty-string ambiguity that JSON-over-HTTP tends to
// produce on optional string fields. Callers that need backend-specific
// normalizations (priority-as-string, empty slices, etc.) should supply
// their own Equal.
func defaultFieldsEqual(_ string, a, b any) bool {
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
