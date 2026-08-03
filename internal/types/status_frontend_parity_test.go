package types

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// One vocabulary, two languages. Go owns the nine statuses (builtinStatuses, in
// enums.go) and the browser cannot import Go, so status.ts writes them out a
// second time. That copy is legitimate — a TypeScript union is what gives the UI
// compile-time checking, and there is no way to get it from here. A copy that
// drifts is not legitimate, and until these tests there was nothing between the
// two but attention: the lists had already fallen into different orders, review
// and closed swapped on the TypeScript side, and no build anywhere noticed.
//
// So the file is read and compared rather than generated. Generating status.ts
// would put the vocabulary under one authority, but status.ts is not a table —
// it is hand-written doc comments, two type guards and a
// `KnownStatus | (string & {})` widening trick around the list — so codegen
// would have to own that hand-written code or split the file in two, and it
// would add a generator plus a staleness check to the frontend build. Reading
// the file costs one test and no build wiring, and it is how this repo already
// pins behavior across the Go/TypeScript line (see
// internal/cli/blocker_resolution_parity_test.go).
//
// Order is compared too, not just membership. Order is the part that drifted,
// and in one of these lists it is load-bearing: USER_SELECTABLE_STATUSES is what
// StatusDropdown maps over to build its options.

const (
	// Paths are relative to this package directory, where the test binary runs.
	frontendStatusTS    = "../webui/frontend/src/types/issue/status.ts"
	frontendAPIClientTS = "../webui/frontend/tests/e2e/api/api-client.ts"
)

// Each declaration in status.ts that has to hold the vocabulary. Captured group
// 1 is the body the status literals are pulled out of.
var (
	tsKnownStatusUnion = regexp.MustCompile(`(?s)export type KnownStatus =(.*?);`)
	tsKnownStatuses    = regexp.MustCompile(`(?s)export const KNOWN_STATUSES:[^=]*=\s*\[(.*?)]`)
	tsUserSelectable   = regexp.MustCompile(`(?s)export const USER_SELECTABLE_STATUSES:[^=]*=\s*\[(.*?)]`)
	tsStatusConst      = regexp.MustCompile(`export const (Status\w+): Status = "([a-z_]+)";`)
	tsStatusLiteral    = regexp.MustCompile(`"([a-z_]+)"`)
	tsIssueStatusAlias = regexp.MustCompile(`export type IssueStatus = (.*)`)
)

func TestFrontendStatusVocabulary(t *testing.T) {
	src := readFrontendFile(t, frontendStatusTS)

	t.Run("KnownStatus union", func(t *testing.T) {
		assertStatusList(t, frontendStatusTS, "the KnownStatus union",
			tsStatusList(t, src, tsKnownStatusUnion, "export type KnownStatus"),
			BuiltinStatuses())
	})

	t.Run("KNOWN_STATUSES", func(t *testing.T) {
		assertStatusList(t, frontendStatusTS, "KNOWN_STATUSES",
			tsStatusList(t, src, tsKnownStatuses, "export const KNOWN_STATUSES"),
			BuiltinStatuses())
	})

	// The named constants are how most call sites reach a status, so a value
	// present in the union but missing a constant is still a half-added status.
	t.Run("Status constants", func(t *testing.T) {
		declared := map[string]string{} // constant name -> value
		var values []string
		for _, m := range tsStatusConst.FindAllStringSubmatch(src, -1) {
			declared[m[1]] = m[2]
			values = append(values, m[2])
		}
		assertStatusList(t, frontendStatusTS, "the Status* constant list", values, BuiltinStatuses())

		for _, s := range BuiltinStatuses() {
			name := goStatusConstName(string(s))
			got, ok := declared[name]
			if !ok {
				t.Errorf("%s declares no %q for built-in status %q — Go has types.%s",
					frontendStatusTS, name, s, name)
				continue
			}
			if got != string(s) {
				t.Errorf("%s: %s = %q, want %q", frontendStatusTS, name, got, s)
			}
		}
	})

	// The narrower cut: the statuses a client may name at all. Go spells it
	// UserFacingStatuses and the DTO layer's isAPIStatus reads the same list, so
	// all three agree or this fails.
	t.Run("USER_SELECTABLE_STATUSES", func(t *testing.T) {
		assertStatusList(t, frontendStatusTS, "USER_SELECTABLE_STATUSES",
			tsStatusList(t, src, tsUserSelectable, "export const USER_SELECTABLE_STATUSES"),
			UserFacingStatuses())
	})
}

// The Playwright API client used to declare a fourth copy of the union. It now
// aliases the app's KnownStatus, which cannot drift; this keeps it that way,
// because re-inlining the list is a one-line edit that nothing else would catch.
func TestFrontendAPIClientReusesTheAppStatusType(t *testing.T) {
	src := readFrontendFile(t, frontendAPIClientTS)

	m := tsIssueStatusAlias.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no `export type IssueStatus = ...` declaration found", frontendAPIClientTS)
	}
	if got := strings.TrimSpace(m[1]); got != "KnownStatus" {
		t.Errorf("%s: IssueStatus = %q, want %q — type it from the app's KnownStatus "+
			"(src/types/issue/status.ts) instead of re-listing the vocabulary, which is "+
			"how this file came to hold a fourth copy of it",
			frontendAPIClientTS, got, "KnownStatus")
	}
	if !strings.Contains(src, "import type { KnownStatus }") {
		t.Errorf("%s: IssueStatus names KnownStatus but the file does not import it",
			frontendAPIClientTS)
	}
}

func readFrontendFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the frontend copy of the status vocabulary: %v — "+
			"if the file moved, this test has to move with it", err)
	}
	return string(b)
}

// tsStatusList pulls the status literals out of one TypeScript declaration.
func tsStatusList(t *testing.T, src string, decl *regexp.Regexp, what string) []string {
	t.Helper()
	m := decl.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: could not find `%s` — it was renamed or reshaped, and this test "+
			"has to be taught the new shape rather than left passing vacuously",
			frontendStatusTS, what)
	}
	var out []string
	for _, lit := range tsStatusLiteral.FindAllStringSubmatch(m[1], -1) {
		out = append(out, lit[1])
	}
	return out
}

// assertStatusList reports every way a TypeScript list can disagree with the Go
// one, by name, so the failure says what to add and where.
func assertStatusList(t *testing.T, path, what string, got []string, want []Status) {
	t.Helper()

	inTS := make(map[string]bool, len(got))
	for _, s := range got {
		inTS[s] = true
	}
	inGo := make(map[string]bool, len(want))
	for _, s := range want {
		inGo[string(s)] = true
	}

	var missing, extra []string
	for _, s := range want {
		if !inTS[string(s)] {
			missing = append(missing, string(s))
		}
	}
	for _, s := range got {
		if !inGo[s] {
			extra = append(extra, s)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%s: %s is missing %s — Go has %s and TypeScript does not. "+
			"Add it to status.ts (the KnownStatus union, a Status* constant and both arrays it belongs in).",
			path, what, quoteAll(missing), quoteAll(missing))
	}
	if len(extra) > 0 {
		t.Errorf("%s: %s lists %s, which Go does not — either add it to "+
			"internal/types/enums.go or drop it here.",
			path, what, quoteAll(extra))
	}
	if len(missing) > 0 || len(extra) > 0 {
		return
	}

	// Same members: the only thing left to disagree on is the order.
	for i := range want {
		if got[i] != string(want[i]) {
			t.Errorf("%s: %s is in a different order than Go's list.\n  Go: %v\n  TS: %v\n"+
				"Go's order is canonical — reorder status.ts to match.",
				path, what, want, got)
			return
		}
	}
}

// goStatusConstName maps a status value to the Go constant that holds it
// ("in_progress" -> "StatusInProgress"), which is the name TypeScript uses too.
func goStatusConstName(status string) string {
	name := "Status"
	for _, word := range strings.Split(status, "_") {
		if word == "" {
			continue
		}
		name += strings.ToUpper(word[:1]) + word[1:]
	}
	return name
}

func quoteAll(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}
