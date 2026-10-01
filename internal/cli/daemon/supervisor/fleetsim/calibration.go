package fleetsim

import (
	"fmt"
	"strings"
	"time"
)

// Calibration source: task e034de04 ("Verify real stale-worker issue write"),
// APPROVED medium confidence. Real local Loom 28e657bdc + Redis FleetDB
// 40e8431d, localdogfood backend, fv-live proxy capture of timestamp, method,
// path, X-Actor, HTTP status and request body. Response bodies were NOT
// captured; there is no matched clean control; S9 resetTask was not observed.
// Nothing below encodes a response body.

// ObservedWrite is one captured request/response pair. Body is the captured
// request body; BodyRecorded=false means the capture did not report it, so
// it is not compared.
type ObservedWrite struct {
	At           time.Time
	Method       string
	Path         string
	Actor        string
	Body         string
	BodyRecorded bool
	Status       int
}

// ObservedFinal is the captured supported-API final state. ClosedAt is zero
// when the capture did not record it for that run.
type ObservedFinal struct {
	Status      string
	Assignee    string // "" is the captured null
	CloseReason string
	ClosedAt    time.Time
}

// ObservedRun is one real run's post-successor-claim segment.
type ObservedRun struct {
	Name              string
	Issue             string
	OldClaimActor     string // agent whose session wrote stale
	SuccessorActor    string
	OldSession        string // "" when not recorded
	SuccessorSession  string
	Writes            []ObservedWrite
	Final             ObservedFinal
	ReaperLockExpired time.Time // zero when not recorded for this run
}

const (
	observedIssue       = "LOCALMODE-3"
	observedOperator    = "operator@local"
	observedCloseReason = "Local mode dogfood implementation completed."
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

// ObservedRuns are the two independently verified product runs.
var ObservedRuns = []ObservedRun{
	{
		Name:              "run1",
		Issue:             observedIssue,
		OldClaimActor:     "local-coder",
		SuccessorActor:    "local-coder2",
		OldSession:        "20260928-041733-local-coder--ad9759ea",
		SuccessorSession:  "20260928-042420-local-coder2--9e7b1114",
		ReaperLockExpired: ts("2026-09-28T04:22:50Z"),
		Writes: []ObservedWrite{
			{At: ts("2026-09-28T04:24:20.904Z"), Method: "POST", Path: "/issues/LOCALMODE-3/claim", Actor: "local-coder2", Status: 200},
			{At: ts("2026-09-28T04:24:30.471Z"), Method: "POST", Path: "/issues/LOCALMODE-3/assign", Actor: observedOperator, Body: `{"assignee":""}`, BodyRecorded: true, Status: 200},
			{At: ts("2026-09-28T04:24:30.472Z"), Method: "POST", Path: "/issues/LOCALMODE-3/close", Actor: observedOperator, Body: `{"reason":"` + observedCloseReason + `"}`, BodyRecorded: true, Status: 200},
			{At: ts("2026-09-28T04:24:30.504Z"), Method: "POST", Path: "/issues/LOCALMODE-3/release-lock", Actor: "local-coder", Status: 409},
			{At: ts("2026-09-28T04:24:30.506Z"), Method: "POST", Path: "/issues/LOCALMODE-3/release-lock", Actor: "local-coder", Status: 409},
		},
		Final: ObservedFinal{Status: "closed", Assignee: "", CloseReason: observedCloseReason},
	},
	{
		Name:           "run2",
		Issue:          observedIssue,
		OldClaimActor:  "local-coder",
		SuccessorActor: "local-coder2",
		Writes: []ObservedWrite{
			{At: ts("2026-09-28T04:26:45.456Z"), Method: "POST", Path: "/issues/LOCALMODE-3/claim", Actor: "local-coder2", Status: 200},
			{At: ts("2026-09-28T04:27:55.356Z"), Method: "POST", Path: "/issues/LOCALMODE-3/assign", Actor: observedOperator, Body: `{"assignee":""}`, BodyRecorded: true, Status: 200},
			{At: ts("2026-09-28T04:27:55.358Z"), Method: "POST", Path: "/issues/LOCALMODE-3/close", Actor: observedOperator, Body: `{"reason":"` + observedCloseReason + `"}`, BodyRecorded: true, Status: 200},
			{At: ts("2026-09-28T04:27:55.389Z"), Method: "POST", Path: "/issues/LOCALMODE-3/release-lock", Actor: "local-coder", Status: 409},
			{At: ts("2026-09-28T04:27:55.390Z"), Method: "POST", Path: "/issues/LOCALMODE-3/release-lock", Actor: "local-coder", Status: 409},
		},
		Final: ObservedFinal{Status: "closed", Assignee: "", CloseReason: observedCloseReason, ClosedAt: ts("2026-09-28T04:27:55.358Z")},
	},
}

// CompareWrites checks the simulated POST sequence on run.Issue, from the
// successor's claim onward, against the capture: same count, and per entry
// the same method, path, X-Actor, status and (when recorded) request body.
// Timestamps are compared to the millisecond. It returns one line per
// divergence; empty means conformant.
func CompareWrites(run ObservedRun, records []Record) []string {
	var sim []Record
	started := false
	for _, r := range records {
		if r.Method != "POST" || r.IssueID != run.Issue || r.Delivery == Drop {
			continue
		}
		if !started && strings.HasSuffix(r.Path, "/claim") && r.Actor == run.SuccessorActor {
			started = true
		}
		if started {
			sim = append(sim, r)
		}
	}
	var diffs []string
	if len(sim) != len(run.Writes) {
		diffs = append(diffs, fmt.Sprintf("write count: sim %d, observed %d", len(sim), len(run.Writes)))
	}
	for i := 0; i < len(sim) && i < len(run.Writes); i++ {
		got, want := sim[i], run.Writes[i]
		if got.Method != want.Method || got.Path != want.Path || got.Actor != want.Actor || got.Status != want.Status {
			diffs = append(diffs, fmt.Sprintf("#%d: sim %s %s actor=%s status=%d, observed %s %s actor=%s status=%d",
				i, got.Method, got.Path, got.Actor, got.Status, want.Method, want.Path, want.Actor, want.Status))
		}
		if want.BodyRecorded && got.Body != want.Body {
			diffs = append(diffs, fmt.Sprintf("#%d body: sim %q, observed %q", i, got.Body, want.Body))
		}
		if !got.AppliedAt.Truncate(time.Millisecond).Equal(want.At) {
			diffs = append(diffs, fmt.Sprintf("#%d time: sim %s, observed %s", i,
				got.AppliedAt.UTC().Format(time.RFC3339Nano), want.At.UTC().Format(time.RFC3339Nano)))
		}
	}
	return diffs
}

// CompareFinal checks the model's final issue state against the capture.
func CompareFinal(run ObservedRun, is Issue) []string {
	var diffs []string
	if is.Status != run.Final.Status {
		diffs = append(diffs, fmt.Sprintf("status: sim %q, observed %q", is.Status, run.Final.Status))
	}
	if is.Assignee != run.Final.Assignee {
		diffs = append(diffs, fmt.Sprintf("assignee: sim %q, observed %q", is.Assignee, run.Final.Assignee))
	}
	if is.CloseReason != run.Final.CloseReason {
		diffs = append(diffs, fmt.Sprintf("close_reason: sim %q, observed %q", is.CloseReason, run.Final.CloseReason))
	}
	if !run.Final.ClosedAt.IsZero() && (is.ClosedAt == nil || !is.ClosedAt.Equal(run.Final.ClosedAt)) {
		diffs = append(diffs, fmt.Sprintf("closed_at: sim %v, observed %s", is.ClosedAt, run.Final.ClosedAt.Format(time.RFC3339Nano)))
	}
	return diffs
}
