//go:build parity

package paritytest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// TestParityTest_PackageBuilds is a smoke test confirming the package
// compiles under the parity tag and exported types round-trip through JSON
// in the same shape fleet-db emits.
func TestParityTest_PackageBuilds(t *testing.T) {
	r := NewReport("1.0.0", "dual_run")
	r.AddFixture("smoke", "smoke fixture", []DiffEntry{
		{
			FixtureID: "smoke",
			StepID:    "step_01",
			Method:    "issue.create",
			Field:     "title",
			DriftTag:  "strict",
			FleetDB:   "hello",
			Beads:     "hello",
			Verdict:   "pass",
		},
	}, 1)
	r.Finalize()

	if r.Verdict != "pass" {
		t.Fatalf("verdict: got %q want pass", r.Verdict)
	}
	if r.Summary.FixturesRun != 1 || r.Summary.TotalComparisons != 1 {
		t.Fatalf("summary counts wrong: %+v", r.Summary)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "diff-report.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Confirm wire-format keys match fleet-db's report shape.
	for _, key := range []string{
		`"version"`, `"generated_at"`, `"contract_version"`, `"mode"`,
		`"beads_available"`, `"summary"`, `"verdict"`, `"fixtures"`,
		`"fixture_id"`, `"step_id"`, `"method"`, `"field"`,
		`"drift_tag"`, `"fleet_db"`, `"beads"`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("output missing expected JSON key %s", key)
		}
	}

	var roundtrip map[string]any
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
}

// TestParityTest_FixtureLoader exercises the fixture JSON loader against
// the MVP fixture. Keeps error paths covered without requiring subprocess
// spawn.
func TestParityTest_FixtureLoader(t *testing.T) {
	fx, err := LoadFixture("testdata/fixtures/crud_create_show.json")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if fx.ID != "crud_create_show" {
		t.Errorf("id: got %q want %q", fx.ID, "crud_create_show")
	}
	if len(fx.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(fx.Steps))
	}
	if fx.Steps[0].Method != "issue.create" {
		t.Errorf("step 0 method: got %q want issue.create", fx.Steps[0].Method)
	}
	if fx.Steps[1].Method != "issue.show" {
		t.Errorf("step 1 method: got %q want issue.show", fx.Steps[1].Method)
	}

	// Error paths.
	if _, err := LoadFixture("testdata/fixtures/does_not_exist.json"); err == nil {
		t.Error("expected error for missing fixture")
	}
}

// TestIssueDataToMap_NilReturnsNoData exercises the nil-in/nil-out path —
// a nil IssueData is "nothing to report", not an error.
func TestIssueDataToMap_NilReturnsNoData(t *testing.T) {
	m, err := issueDataToMap(nil)
	if err != nil {
		t.Fatalf("nil input should not error: %v", err)
	}
	if m != nil {
		t.Errorf("nil input should yield nil map; got %v", m)
	}
}

// TestIssueDataToMap_RoundTrip confirms the normal path emits a flat
// JSON-shaped map with expected keys. If the shape ever changes the
// diff layer's field list has to change with it, so this test is the
// canary.
func TestIssueDataToMap_RoundTrip(t *testing.T) {
	prio := 2
	d := &backend.IssueData{
		ID:       "PARITY-1",
		Title:    "hello",
		Priority: prio,
		Status:   "open",
	}
	m, err := issueDataToMap(d)
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if m["title"] != "hello" {
		t.Errorf("title: got %v want hello", m["title"])
	}
	if m["id"] != "PARITY-1" {
		t.Errorf("id: got %v want PARITY-1", m["id"])
	}
}

// TestParityTest_CrudCreateShow is the flagship end-to-end harness run: it
// spawns bd + fleet-db subprocesses, loads a 2-step fixture, executes both
// steps against both backends, and emits a diff report.
//
// Semantic: subprocess spawn failures, fixture load failures, and panics
// fail the Go test. Diffs in the report are DATA — a nonzero diff count
// does NOT fail the test. Callers inspect the report to triage signal.
//
// This is intentionally the only orchestration test in the MVP so that
// failures are easy to bisect: all wiring is under one function.
func TestParityTest_CrudCreateShow(t *testing.T) {
	// Spawn backends. Each helper calls t.Skip() or t.Fatal() itself if its
	// prerequisites aren't met, so we get structured failures.
	beadsBE, _ := spawnBeads(t)
	fleetBE, _ := spawnFleetDB(t)

	fx, err := LoadFixture("testdata/fixtures/crud_create_show.json")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	report := NewReport("1.0.0", "dual_run")
	runner := New(beadsBE, fleetBE, report)

	diffs, err := runner.RunFixture(t.Context(), *fx)
	if err != nil {
		t.Fatalf("RunFixture: %v", err)
	}

	report.AddFixture(fx.ID, fx.Title, diffs, len(fx.Steps))
	report.Finalize()

	// Write the report to a temp location so operators can inspect it.
	outPath := filepath.Join(t.TempDir(), "parity-report.json")
	if err := report.WriteJSON(outPath); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	// Log a compact summary — diffs are expected but non-fatal.
	t.Logf("fixture %s: %d diffs, verdict=%s, report=%s", fx.ID, len(diffs), report.Verdict, outPath)
	for _, d := range diffs {
		fleetJSON, _ := json.Marshal(d.FleetDB)
		beadsJSON, _ := json.Marshal(d.Beads)
		t.Logf("  diff: step=%s field=%s fleet=%s beads=%s verdict=%s",
			d.StepID, d.Field, string(fleetJSON), string(beadsJSON), d.Verdict)
	}

	// Structural assertions — shape/counts must be sane even if values drift.
	if report.Summary.FixturesRun != 1 {
		t.Errorf("FixturesRun: got %d want 1", report.Summary.FixturesRun)
	}
	if report.Summary.StepsExecuted != len(fx.Steps) {
		t.Errorf("StepsExecuted: got %d want %d", report.Summary.StepsExecuted, len(fx.Steps))
	}
	if len(report.Fixtures) != 1 {
		t.Fatalf("expected 1 fixture in report, got %d", len(report.Fixtures))
	}
	if report.Fixtures[0].FixtureID != fx.ID {
		t.Errorf("FixtureID: got %q want %q", report.Fixtures[0].FixtureID, fx.ID)
	}
}

// TestParityTest_AllFixtures auto-discovers every fixture JSON file under
// testdata/fixtures/ and runs each as a subtest against a single shared
// bd + fleet-db pair. Each fixture gets an isolated beads workspace / fleet
// workspace so state bleed between fixtures is impossible.
//
// Semantic (same as TestParityTest_CrudCreateShow):
//   - infra failures (spawn, fixture load) fail the subtest
//   - diff entries are DATA, not failures — a fixture with 10 diffs still
//     passes the subtest; operators inspect the report JSON for signal
//   - the test failing indicates broken wiring, not backend drift
//
// The aggregated report is written to one file per invocation so operators
// can triage cross-fixture drift in a single pass. See doc.go for the wire
// format.
func TestParityTest_AllFixtures(t *testing.T) {
	fixtures, err := discoverFixtures("testdata/fixtures")
	if err != nil {
		t.Fatalf("discoverFixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found under testdata/fixtures — expected at least one")
	}

	report := NewReport("1.0.0", "dual_run")

	for _, path := range fixtures {
		fx, err := LoadFixture(path)
		if err != nil {
			t.Fatalf("LoadFixture(%s): %v", path, err)
		}

		t.Run(fx.ID, func(t *testing.T) {
			// Spawn backends per-subtest so workspaces are isolated and a
			// failure in one fixture can't poison another. spawnBeads +
			// spawnFleetDB both register t.Cleanup, so this is safe.
			beadsBE, _ := spawnBeads(t)
			fleetBE, _ := spawnFleetDB(t)

			runner := New(beadsBE, fleetBE, report)
			diffs, err := runner.RunFixture(t.Context(), *fx)
			if err != nil {
				t.Fatalf("RunFixture: %v", err)
			}

			report.AddFixture(fx.ID, fx.Title, diffs, len(fx.Steps))

			// Compact per-fixture log: one line per diff so operators can
			// eyeball the report without opening the JSON.
			t.Logf("fixture %s: %d diffs", fx.ID, len(diffs))
			for _, d := range diffs {
				fleetJSON, _ := json.Marshal(d.FleetDB)
				beadsJSON, _ := json.Marshal(d.Beads)
				t.Logf("  diff: step=%s method=%s field=%s fleet=%s beads=%s verdict=%s",
					d.StepID, d.Method, d.Field, string(fleetJSON), string(beadsJSON), d.Verdict)
			}
		})
	}

	report.Finalize()
	outPath := filepath.Join(t.TempDir(), "parity-report-all.json")
	if err := report.WriteJSON(outPath); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	t.Logf("aggregate report: fixtures=%d steps=%d diffs=%d verdict=%s path=%s",
		report.Summary.FixturesRun, report.Summary.StepsExecuted,
		report.Summary.DiffsFound, report.Verdict, outPath)

	// Structural assertion — we should have exactly one FixtureReport per
	// discovered fixture.
	if report.Summary.FixturesRun != len(fixtures) {
		t.Errorf("FixturesRun: got %d want %d", report.Summary.FixturesRun, len(fixtures))
	}
}

func TestFleetDBOnlyBackendBatchAndErrorSemantics(t *testing.T) {
	fleetBE, _ := spawnFleetDB(t)
	ctx := context.Background()

	seedUpdate, err := fleetBE.Create(ctx, backend.CreateParams{
		Title:     "Batch update seed",
		Priority:  2,
		IssueType: "task",
		Owner:     parityActor,
	})
	if err != nil {
		t.Fatalf("create update seed: %v", err)
	}
	seedClose, err := fleetBE.Create(ctx, backend.CreateParams{
		Title:     "Batch close seed",
		Priority:  2,
		IssueType: "task",
		Owner:     parityActor,
	})
	if err != nil {
		t.Fatalf("create close seed: %v", err)
	}

	updateTitle := "Batch-updated issue"
	updatePriority := 1
	successOps := []backend.BatchOp{
		{Operation: "create", Args: mustRawJSON(t, backend.CreateParams{
			Title:     "Batch-created issue",
			Priority:  2,
			IssueType: "task",
			Owner:     parityActor,
		})},
		{Operation: "update", Args: mustRawJSON(t, map[string]any{
			"id":       seedUpdate.ID,
			"title":    updateTitle,
			"priority": updatePriority,
		})},
		{Operation: "close", Args: mustRawJSON(t, map[string]any{
			"id":     seedClose.ID,
			"reason": "batch close",
		})},
	}
	results, err := fleetBE.Batch(ctx, successOps)
	if err != nil {
		t.Fatalf("successful Batch returned method error: %v", err)
	}
	assertBatchShape(t, results, []bool{true, true, true}, []string{"", "", ""})

	updated, err := fleetBE.Get(ctx, seedUpdate.ID)
	if err != nil {
		t.Fatalf("get updated issue: %v", err)
	}
	if updated.Title != updateTitle {
		t.Errorf("updated.Title = %q, want %q", updated.Title, updateTitle)
	}
	if updated.Priority != updatePriority {
		t.Errorf("updated.Priority = %d, want %d", updated.Priority, updatePriority)
	}
	closed, err := fleetBE.Get(ctx, seedClose.ID)
	if err != nil {
		t.Fatalf("get closed issue: %v", err)
	}
	if closed.Status != "closed" {
		t.Errorf("closed.Status = %q, want closed", closed.Status)
	}

	errorOps := []backend.BatchOp{
		{Operation: "update", Args: mustRawJSON(t, map[string]any{"title": "missing id"})},
		{Operation: "delete", Args: mustRawJSON(t, map[string]any{"id": "does-not-exist-ever-parity-probe"})},
		{Operation: "close", Args: mustRawJSON(t, map[string]any{"id": seedClose.ID})},
		{Operation: "teleport", Args: mustRawJSON(t, map[string]any{})},
	}
	results, err = fleetBE.Batch(ctx, errorOps)
	if err != nil {
		t.Fatalf("error Batch returned method error: %v", err)
	}
	assertBatchShape(t, results,
		[]bool{false, false, false, false},
		[]string{
			string(backend.KindValidation),
			string(backend.KindNotFound),
			string(backend.KindConflict),
			string(backend.KindValidation),
		},
	)
}

func TestFleetDBOnlyBackendClaimAndLockSemantics(t *testing.T) {
	fleetBE, _, mr := spawnFleetDBWithRedis(t)
	ctx := context.Background()
	fleetAdapter, ok := fleetBE.(*fleetDBAdapter)
	if !ok {
		t.Fatalf("spawnFleetDB returned %T, want *fleetDBAdapter", fleetBE)
	}

	issue, err := fleetBE.Create(ctx, backend.CreateParams{
		Title:     "Claim lock seed",
		Priority:  2,
		IssueType: "task",
		Owner:     parityActor,
	})
	if err != nil {
		t.Fatalf("create claim seed: %v", err)
	}
	if err := fleetBE.ClaimIssue(ctx, issue.ID, time.Second); err != nil {
		t.Fatalf("initial claim: %v", err)
	}
	claimed, err := fleetBE.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get claimed issue: %v", err)
	}
	if claimed.Status != "in_progress" {
		t.Errorf("claimed.Status = %q, want in_progress", claimed.Status)
	}
	if claimed.Assignee != parityActor {
		t.Errorf("claimed.Assignee = %q, want %q", claimed.Assignee, parityActor)
	}

	// Same-actor re-claim is a heartbeat-style refresh.
	if err := fleetBE.ClaimIssue(ctx, issue.ID, time.Second); err != nil {
		t.Fatalf("same actor reclaim: %v", err)
	}

	other := newFleetDBAdapter(fleetAdapter.baseURL, fleetAdapter.workspaceID, "other-agent")
	err = other.ClaimIssue(ctx, issue.ID, 2*time.Second)
	if err == nil {
		t.Fatal("cross-actor claim while lock is live succeeded; want conflict")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("cross-actor live-lock claim error = %v, want KindConflict", err)
	}

	mr.FastForward(2 * time.Second)
	if err := other.ClaimIssue(ctx, issue.ID, 2*time.Second); err != nil {
		t.Fatalf("stale claim recovery after TTL expiry: %v", err)
	}
	reclaimed, err := other.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get reclaimed issue: %v", err)
	}
	if reclaimed.Status != "in_progress" {
		t.Errorf("reclaimed.Status = %q, want in_progress", reclaimed.Status)
	}
	if reclaimed.Assignee != "other-agent" {
		t.Errorf("reclaimed.Assignee = %q, want other-agent", reclaimed.Assignee)
	}

	concurrentIssue, err := fleetBE.Create(ctx, backend.CreateParams{
		Title:     "Concurrent claim seed",
		Priority:  2,
		IssueType: "task",
		Owner:     parityActor,
	})
	if err != nil {
		t.Fatalf("create concurrent seed: %v", err)
	}

	const contenders = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes []string
	conflicts := 0
	for i := 0; i < contenders; i++ {
		actor := "contender-" + strconv.Itoa(i)
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			<-start
			adapter := newFleetDBAdapter(fleetAdapter.baseURL, fleetAdapter.workspaceID, actor)
			err := adapter.ClaimIssue(ctx, concurrentIssue.ID, 5*time.Second)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes = append(successes, actor)
			case backend.IsKind(err, backend.KindConflict):
				conflicts++
			default:
				t.Errorf("claim by %s returned unexpected error: %v", actor, err)
			}
		}(actor)
	}
	close(start)
	wg.Wait()

	if len(successes) != 1 {
		t.Fatalf("successful concurrent claims = %v, want exactly one", successes)
	}
	if conflicts != contenders-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, contenders-1)
	}
	final, err := fleetBE.Get(ctx, concurrentIssue.ID)
	if err != nil {
		t.Fatalf("get concurrent issue: %v", err)
	}
	if final.Assignee != successes[0] {
		t.Errorf("final.Assignee = %q, want winning actor %q", final.Assignee, successes[0])
	}
}

func TestFleetDBOnlyBackendMultiActorAuditAndAuth(t *testing.T) {
	fleetBE, _ := spawnFleetDB(t)
	ctx := context.Background()
	alice, ok := fleetBE.(*fleetDBAdapter)
	if !ok {
		t.Fatalf("spawnFleetDB returned %T, want *fleetDBAdapter", fleetBE)
	}
	alice.actor = "alice"
	bob := newFleetDBAdapter(alice.baseURL, alice.workspaceID, "bob")

	issue, err := alice.Create(ctx, backend.CreateParams{
		Title:     "Audit actor seed",
		Priority:  2,
		IssueType: "task",
		Owner:     "alice",
	})
	if err != nil {
		t.Fatalf("alice create: %v", err)
	}
	if issue.CreatedBy != "alice" {
		t.Fatalf("created_by = %q, want alice", issue.CreatedBy)
	}

	if err := bob.ClaimIssue(ctx, issue.ID, 5*time.Second); err != nil {
		t.Fatalf("bob claim: %v", err)
	}
	claimed, err := bob.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("bob get claimed: %v", err)
	}
	if claimed.Assignee != "bob" {
		t.Fatalf("assignee = %q, want bob", claimed.Assignee)
	}

	comment, err := bob.AddComment(ctx, backend.CommentAddParams{IssueID: issue.ID, Text: "bob audit comment"})
	if err != nil {
		t.Fatalf("bob comment: %v", err)
	}
	if comment.Author != "bob" {
		t.Fatalf("comment author = %q, want bob", comment.Author)
	}

	events, err := bob.ListEvents(ctx, issue.ID, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	seenCreate := false
	seenClaim := false
	seenComment := false
	for _, event := range events {
		switch {
		case event.Kind == "issue.create" && event.Actor == "alice":
			seenCreate = true
		case event.Kind == "issue.claim" && event.Actor == "bob":
			seenClaim = true
		case event.Kind == "comment.add" && event.Actor == "bob":
			seenComment = true
		}
	}
	if !seenCreate || !seenClaim || !seenComment {
		t.Fatalf("audit events missing actors: create=%v claim=%v comment=%v events=%+v", seenCreate, seenClaim, seenComment, events)
	}

	unauthenticated := newFleetDBAdapter(alice.baseURL, alice.workspaceID, "")
	_, err = unauthenticated.Create(ctx, backend.CreateParams{
		Title:     "Should be unauthorized",
		Priority:  2,
		IssueType: "task",
	})
	if err == nil {
		t.Fatal("unauthenticated create succeeded; want auth failure")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("unauthenticated create error = %v, want KindUnavailable auth failure", err)
	}
}

func TestFleetDBOnlyBackendWorkerHeartbeatRenewsAndStaleRecovers(t *testing.T) {
	fleetBE, _, mr := spawnFleetDBWithRedis(t)
	ctx := context.Background()
	worker, ok := fleetBE.(*fleetDBAdapter)
	if !ok {
		t.Fatalf("spawnFleetDB returned %T, want *fleetDBAdapter", fleetBE)
	}
	worker.actor = "supervisor-a"
	other := newFleetDBAdapter(worker.baseURL, worker.workspaceID, "supervisor-b")

	issue, err := worker.Create(ctx, backend.CreateParams{
		Title:     "Heartbeat stale recovery seed",
		Priority:  2,
		IssueType: "task",
		Owner:     "supervisor-a",
	})
	if err != nil {
		t.Fatalf("create heartbeat seed: %v", err)
	}
	if err := worker.ClaimIssue(ctx, issue.ID, time.Second); err != nil {
		t.Fatalf("initial claim: %v", err)
	}

	mr.FastForward(500 * time.Millisecond)
	hb, err := worker.heartbeatWorker(ctx, "supervisor-a")
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !hb.Success || hb.TTL <= 1 {
		t.Fatalf("heartbeat result = %+v, want success with renewed TTL > original claim TTL", hb)
	}

	mr.FastForward(2 * time.Second)
	err = other.ClaimIssue(ctx, issue.ID, time.Second)
	if err == nil {
		t.Fatal("other supervisor claimed after original TTL despite heartbeat renewal")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("claim after heartbeat error = %v, want KindConflict", err)
	}

	mr.FastForward(time.Duration(hb.TTL+1) * time.Second)
	if err := other.ClaimIssue(ctx, issue.ID, time.Second); err != nil {
		t.Fatalf("stale recovery claim after renewed TTL expiry: %v", err)
	}
	recovered, err := other.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get recovered issue: %v", err)
	}
	if recovered.Assignee != "supervisor-b" {
		t.Fatalf("assignee after stale recovery = %q, want supervisor-b", recovered.Assignee)
	}
}

func TestFleetDBOnlyBackendRemoteWorkspaceIsolation(t *testing.T) {
	fleetBE, _ := spawnFleetDB(t)
	ctx := context.Background()
	wsA, ok := fleetBE.(*fleetDBAdapter)
	if !ok {
		t.Fatalf("spawnFleetDB returned %T, want *fleetDBAdapter", fleetBE)
	}
	const workspaceB = "PARITY-B"
	if err := createFleetWorkspace(wsA.baseURL, workspaceB); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	wsB := newFleetDBAdapter(wsA.baseURL, workspaceB, parityActor)

	issueA, err := wsA.Create(ctx, backend.CreateParams{Title: "Workspace A issue", Priority: 2, IssueType: "task"})
	if err != nil {
		t.Fatalf("create workspace A issue: %v", err)
	}
	issueB, err := wsB.Create(ctx, backend.CreateParams{Title: "Workspace B issue", Priority: 2, IssueType: "task"})
	if err != nil {
		t.Fatalf("create workspace B issue: %v", err)
	}

	if _, err := wsA.Get(ctx, issueB.ID); !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("workspace A get workspace B issue error = %v, want KindNotFound", err)
	}
	if _, err := wsB.Get(ctx, issueA.ID); !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("workspace B get workspace A issue error = %v, want KindNotFound", err)
	}
	if err := wsA.ClaimIssue(ctx, issueB.ID, time.Second); !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("workspace A claim workspace B issue error = %v, want KindNotFound", err)
	}
	if err := wsB.ClaimIssue(ctx, issueA.ID, time.Second); !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("workspace B claim workspace A issue error = %v, want KindNotFound", err)
	}

	listA, err := wsA.listIssueIDs(ctx)
	if err != nil {
		t.Fatalf("list workspace A: %v", err)
	}
	listB, err := wsB.listIssueIDs(ctx)
	if err != nil {
		t.Fatalf("list workspace B: %v", err)
	}
	if slicesContainsString(listA, issueB.ID) {
		t.Fatalf("workspace A list leaked workspace B issue %q", issueB.ID)
	}
	if slicesContainsString(listB, issueA.ID) {
		t.Fatalf("workspace B list leaked workspace A issue %q", issueA.ID)
	}
}

func slicesContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal raw json: %v", err)
	}
	return raw
}

func assertBatchShape(t *testing.T, results []backend.BatchResult, wantSuccess []bool, wantKinds []string) {
	t.Helper()
	if len(results) != len(wantSuccess) {
		t.Fatalf("len(results) = %d, want %d: %+v", len(results), len(wantSuccess), results)
	}
	for i, result := range results {
		if result.Success != wantSuccess[i] {
			t.Errorf("results[%d].Success = %v, want %v (error=%q)", i, result.Success, wantSuccess[i], result.Error)
		}
		if got := batchErrorKind(result.Error); got != wantKinds[i] {
			t.Errorf("results[%d] error kind = %q, want %q (error=%q)", i, got, wantKinds[i], result.Error)
		}
	}
}

// discoverFixtures returns sorted absolute paths to every *.json file under
// dir. Sort order keeps test output deterministic so flaky runs are easy
// to bisect.
func discoverFixtures(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}
