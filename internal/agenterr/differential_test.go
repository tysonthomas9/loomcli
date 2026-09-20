package agenterr

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"
)

// The differential parity gate for moving the residual regex table into
// harness-wrapper.
//
// The table's eight rows were the last harness-output patterns loom owned.
// Moving them is only safe if the COMPLETE pipeline — markers, the wrapper's
// classifier, the residual rows behind it, the exit-code fallback, the
// messages, the retry hints and the evidence record — produces the same verdict
// it did before. Arguing that from the code is not the same as measuring it, so
// this replays a corpus through the pipeline and compares it against a capture
// taken from local/union at fe4b608ec, the commit BEFORE the move.
//
// A mismatch here is fixed in the migration, never in the expectation: the
// golden is evidence about the old behavior, and editing it to agree with the
// new behavior would delete the only thing that makes the move checkable.
//
// A DELIBERATE change is declared in declaredDeviations below, with its reason,
// rather than absorbed by regenerating the golden — so the capture stays a
// faithful record of the old behavior and every intended difference from it
// stays readable next to the row it changes.
const (
	differentialCorpus = "testdata/differential_corpus.json"
	differentialGolden = "testdata/differential_union_fe4b608ec.golden.json"
)

// declaredDeviations names the corpus rows whose verdict a change moves ON
// PURPOSE, keyed by row name. Everything else must match the baseline exactly.
//
// Keep this list short and each entry argued. A deviation that cannot be
// explained in a sentence is a regression wearing a note.
//
// becomes is the class the row must now produce, or "" when the deviation is
// evidence-only and the VERDICT must not have moved at all. The distinction is
// the point: most intended changes should leave the class alone, and one that
// does move it has to say so out loud and pin where it moved TO — so a second,
// unintended drift on the same row still fails.
type deviation struct {
	why     string
	becomes string
}

var declaredDeviations = map[string]deviation{
	"marker billing wall": {becomes: "", why: "" +
		"The billing arm gains an Evidence record (source=harness_marker, " +
		"rule=BillingWallMarker). It shipped without one because it shipped " +
		"without an EMITTER: nothing could raise the marker, so no production " +
		"verdict ever took this arm and no record was ever missing. " +
		"harness-wrapper names a billing wall now, so the arm is reachable — " +
		"and it is FATAL, which by ADR-0002 is exactly the disposition that has " +
		"to be judgeable from one occurrence. Nothing can regress: there is no " +
		"prior behavior here to preserve."},

	"residual.billing": {becomes: "Unknown", why: "" +
		"`your billing needs attention` is not a billing wall. The row matched " +
		"a bare `billing`, so an agent editing billing code — or a filename " +
		"like docs/billing.md — classified ErrBilling, which is FATAL and arms " +
		"an account-wide cooldown. The bare word rows are gone from " +
		"harness-wrapper; a real wall is named by the harness's own transcript " +
		"tag now, so the row no longer has to guess at one. Unknown is the safe " +
		"direction: a bounded restart, not a fatal stop."},

	"residual.auth env var name": {becomes: "Unknown", why: "" +
		"`reading ANTHROPIC_API_KEY from the environment` is an agent naming a " +
		"variable, not a credential failure. The row listed the five *_API_KEY " +
		"names as literals, so printing one fatally stopped the agent. That is " +
		"ADR-0002's third revisit trigger by construction — an AuthFailure from " +
		"residual.auth whose Match is ordinary task output — and the ADR's own " +
		"named remedy is to narrow the pattern, not the disposition. The name " +
		"still classifies when a failure word keeps it company on the same " +
		"line (`OPENAI_API_KEY is not set`), which is the case that mattered."},
}

// checkDeclaredDeviation enforces what a declared deviation may change. An
// evidence-only entry must not move the verdict; a verdict entry must land on
// exactly the class it declared.
func checkDeclaredDeviation(t *testing.T, name string, d deviation, got, want differentialRow) {
	t.Helper()
	if d.becomes == "" {
		if got.Class != want.Class || got.Message != want.Message || got.RetryAfterMS != want.RetryAfterMS {
			t.Errorf("declared deviation %q is evidence-only but moved the VERDICT:\n got  class=%q msg=%q retry=%d\n want class=%q msg=%q retry=%d",
				name, got.Class, got.Message, got.RetryAfterMS, want.Class, want.Message, want.RetryAfterMS)
		}
		return
	}
	if got.Class != d.becomes {
		t.Errorf("declared deviation %q: class = %q, declared to become %q (baseline was %q)",
			name, got.Class, d.becomes, want.Class)
	}
}

type differentialInput struct {
	Name     string
	Backend  string
	Text     string
	ExitCode int
}

// differentialRow is the whole observable verdict, flattened so a diff points
// at the field that moved.
type differentialRow struct {
	Name         string
	Backend      string
	ExitCode     int
	Class        string
	Message      string
	RetryAfterMS int64
	Evidence     Evidence
}

func TestDifferentialParityAgainstUnionBaseline(t *testing.T) {
	var corpus []differentialInput
	readJSON(t, differentialCorpus, &corpus)
	var want []differentialRow
	readJSON(t, differentialGolden, &want)

	if len(corpus) == 0 || len(corpus) != len(want) {
		t.Fatalf("corpus/golden length mismatch: %d vs %d", len(corpus), len(want))
	}

	for i, in := range corpus {
		got := differentialRow{
			Name:     in.Name,
			Backend:  in.Backend,
			ExitCode: in.ExitCode,
		}
		e := ClassifyFromOutput(in.Text, in.ExitCode, in.Backend)
		ev := e.Evidence
		// ScannedBytes is len(text) by construction on both sides; dropping it
		// keeps the golden about classification rather than about arithmetic.
		ev.ScannedBytes = 0
		got.Class = fmt.Sprint(e.Class)
		got.Message = normalizeResumeTime(e.Message)
		got.RetryAfterMS = e.RetryAfter.Milliseconds()
		ev.Detail = normalizeResumeTime(ev.Detail)
		ev.Match = normalizeResumeTime(ev.Match)
		ev.Excerpt = normalizeResumeTime(ev.Excerpt)
		got.Evidence = ev

		w := want[i]
		if got.Name != w.Name || got.Backend != w.Backend || got.ExitCode != w.ExitCode {
			t.Fatalf("row %d: corpus and golden are out of step (%q/%q vs %q/%q) — regenerate both together",
				i, got.Name, got.Backend, w.Name, w.Backend)
		}
		if equalJSON(t, got, w) {
			continue
		}
		if d, declared := declaredDeviations[in.Name]; declared {
			checkDeclaredDeviation(t, in.Name, d, got, w)
			continue
		}
		t.Errorf("verdict moved for %q (backend=%q, exit=%d):\n--- before the move ---\n%s\n--- after ---\n%s",
			in.Name, in.Backend, in.ExitCode, mustJSON(t, w), mustJSON(t, got))
	}
}

// TestDifferentialCorpusCoverage keeps the corpus honest about what it claims
// to cover. A parity gate that quietly stopped exercising the residual rows
// would pass forever while proving nothing.
func TestDifferentialCorpusCoverage(t *testing.T) {
	var want []differentialRow
	readJSON(t, differentialGolden, &want)

	rules := map[string]int{}
	sources := map[EvidenceSource]int{}
	backends := map[string]int{}
	for _, w := range want {
		rules[w.Evidence.Rule]++
		sources[w.Evidence.Source]++
		backends[w.Backend]++
	}

	for _, id := range []string{
		"residual.ratelimit", "residual.auth", "residual.billing",
		"residual.model_version", "residual.model_not_found",
		"residual.context", "residual.timeout", "residual.transient",
	} {
		if rules[id] == 0 {
			t.Errorf("no corpus row exercises %s", id)
		}
	}
	for _, id := range []string{
		"BackendUnavailableMarker", "AgentLaunchFailedMarker", "RunTurnDeadlineMarker",
		"AuthRequiredMarker", "UsageLimitedMarker",
	} {
		if rules[id] == 0 {
			t.Errorf("no corpus row exercises the %s arm", id)
		}
	}
	for _, src := range []EvidenceSource{
		EvidenceHarnessMarker, EvidenceWrapper, EvidenceResidual, EvidenceExitCode,
	} {
		if sources[src] == 0 {
			t.Errorf("no corpus row produces source=%s", src)
		}
	}
	// Every supported harness plus an unknown one and the empty backend: the
	// residual rows are backend-agnostic, and "it works for claude" was never
	// the question.
	for _, b := range []string{"claude", "claude-code", "codex", "cursor", "opencode", "pi", "", "some-unknown-harness"} {
		if backends[b] == 0 {
			t.Errorf("no corpus row for backend %q", b)
		}
	}

	// A deviation that no longer differs is a note nobody will delete. Fail on
	// it so the list stays a list of real, current exceptions.
	var corpus []differentialInput
	readJSON(t, differentialCorpus, &corpus)
	named := map[string]bool{}
	for _, in := range corpus {
		named[in.Name] = true
	}
	for name := range declaredDeviations {
		if !named[name] {
			t.Errorf("declared deviation %q names no corpus row", name)
		}
	}
}

// resumeTimeRe matches the RFC3339 instant a session-limit verdict carries.
//
// harness-wrapper parses "resets 10:20pm" into an ABSOLUTE time — the next
// 22:20 in the machine's zone — and formats it into the reason, which loom
// then records as the message and as the evidence Detail. That makes the
// verdict a function of today's date and $TZ, so a golden holding one is red
// tomorrow and red in CI. The instant is not what this gate is about: it pins
// which verdict the pipeline reaches and what it recorded about it.
var resumeTimeRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[+-]\d{2}:\d{2}|Z)`)

func normalizeResumeTime(s string) string {
	return resumeTimeRe.ReplaceAllString(s, "<resume-time>")
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// equalJSON compares through the serialized form so the pointer-bearing
// ScreenEvidence compares by value.
func equalJSON(t *testing.T, a, b differentialRow) bool {
	t.Helper()
	return mustJSON(t, a) == mustJSON(t, b)
}

func mustJSON(t *testing.T, v differentialRow) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
