//go:build fleetdbdiff

package supervisor

// Differential check of the fleetsim fake against a real FleetDB served by the
// repo local-mode stack (make local-mode-up). Each scenario runs the same real
// Loom code (FleetBackend adapter, claimTask, claimIssueForAgent,
// releaseAssignedTaskClaim, `loom data close` via ScriptedWorker's Close) once
// against the fake and once against real FleetDB, step by step in the same
// order, and compares every request (method, path, query, X-Actor, request
// body), status, the response-body fields the fake emits, and the final issue
// state + lock holder. Issues are created through the stack's Loom serve API
// (the same route local-mode-entrypoint seeds with); nothing writes Redis.
//
// Run:
//   FLEETDB_DIFF_URL=http://127.0.0.1:8680 LOOM_DIFF_API=http://127.0.0.1:8682 \
//   FLEETDB_DIFF_WS=WSTWO FLEETDB_DIFF_OUT=/tmp/out \
//   go test -tags fleetdbdiff -run TestFleetDBDiff -v ./internal/cli/daemon/supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor/fleetsim"
)

type diffRec struct {
	Seq       int       `json:"seq"`
	Attempt   string    `json:"attempt"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Query     string    `json:"query,omitempty"`
	Actor     string    `json:"actor"`
	Body      string    `json:"req_body,omitempty"`
	Status    int       `json:"status"`
	RespBody  string    `json:"resp_body,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	At        time.Time `json:"at"`
}

type diffFinal struct {
	Status      string `json:"status"`
	Assignee    string `json:"assignee"`
	CloseReason string `json:"close_reason"`
	ClosedAtSet bool   `json:"closed_at_set"`
	LockHolder  string `json:"lock_holder"`
}

// diffEnv is one side (fake or real) of a scenario.
type diffEnv struct {
	real  bool
	ws    string
	scen  string
	sim   *fleetsim.Sim
	url   string
	mu    sync.Mutex
	recs  []diffRec
	n     int
	plain *http.Client
}

type recTransport struct {
	e       *diffEnv
	attempt string
}

func (t *recTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	e := t.e
	e.mu.Lock()
	e.n++
	n := e.n
	e.mu.Unlock()
	rid := fmt.Sprintf("fd659d-%s-%03d", e.scen, n)
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.Header.Set("X-Request-ID", rid)
	at := time.Now().UTC()
	resp, err := http.DefaultTransport.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	rb, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(rb))
	e.mu.Lock()
	e.recs = append(e.recs, diffRec{
		Seq: n, Attempt: t.attempt, Method: req.Method,
		Path:  strings.TrimPrefix(req.URL.Path, "/api/v1/"+e.ws),
		Query: req.URL.RawQuery, Actor: req.Header.Get("X-Actor"), Body: string(body),
		Status: resp.StatusCode, RespBody: string(rb), RequestID: resp.Header.Get("X-Request-ID"), At: at,
	})
	e.mu.Unlock()
	return resp, nil
}

func (e *diffEnv) backend(attempt, actor string) *fleet.FleetBackend {
	if !e.real {
		return e.sim.Backend(attempt, actor)
	}
	b, err := fleet.New(fleet.Config{BaseURL: e.url, WorkspaceID: e.ws, Actor: actor,
		HTTPClient: &http.Client{Transport: &recTransport{e: e, attempt: attempt}, Timeout: 30 * time.Second}})
	if err != nil {
		panic(err)
	}
	return b
}

func (e *diffEnv) attempt(name, worktree string) *simAttempt {
	if !e.real {
		e.sim.BindAttempt(name, worktree)
	}
	s := &Supervisor{IssueBackend: e.backend(name, simDaemonActor), WorkspaceID: e.ws, NodeID: "node-" + worktree}
	if !e.real {
		s.Clock = e.sim.Clock
	}
	return &simAttempt{name: name, s: s, ap: &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: worktree, Role: "task"}}}
}

// step runs fn to completion: real side directly, fake side with FIFO delivery.
func (e *diffEnv) step(name string, fn func() error) error {
	if e.real {
		return fn()
	}
	e.sim.Go(name, fn)
	e.sim.DrainFIFO()
	if !e.sim.Finished(name) {
		return fmt.Errorf("fake step %s did not finish; pending=%+v", name, e.sim.Pending())
	}
	return e.sim.Err(name)
}

func (e *diffEnv) records() []diffRec {
	if e.real {
		e.mu.Lock()
		defer e.mu.Unlock()
		return append([]diffRec(nil), e.recs...)
	}
	var out []diffRec
	for i, r := range e.sim.Records() {
		out = append(out, diffRec{Seq: i + 1, Attempt: r.Attempt, Method: r.Method, Path: r.Path, Query: r.Query,
			Actor: r.Actor, Body: r.Body, Status: r.Status, RespBody: r.RespBody, At: r.AppliedAt.UTC()})
	}
	return out
}

func (e *diffEnv) getJSON(path string, v any) (int, string, error) {
	req, _ := http.NewRequest("GET", e.url+"/api/v1/"+e.ws+path, nil)
	req.Header.Set("X-Actor", simDaemonActor)
	req.Header.Set("X-Request-ID", fmt.Sprintf("fd659d-%s-observe", e.scen))
	resp, err := e.plain.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if v != nil && resp.StatusCode == 200 {
		_ = json.Unmarshal(b, v)
	}
	return resp.StatusCode, string(b), nil
}

func (e *diffEnv) final(id string) (diffFinal, map[string]string) {
	raw := map[string]string{}
	if !e.real {
		is, holder, _ := e.sim.Server.Snapshot(id)
		return diffFinal{Status: is.Status, Assignee: is.Assignee, CloseReason: is.CloseReason, ClosedAtSet: is.ClosedAt != nil, LockHolder: holder}, raw
	}
	var is map[string]any
	_, raw["issue"], _ = e.getJSON("/issues/"+id, &is)
	var lk map[string]any
	code, lb, _ := e.getJSON("/issues/"+id+"/lock", &lk)
	raw["lock"] = fmt.Sprintf("%d %s", code, lb)
	_, raw["history"], _ = e.getJSON("/issues/"+id+"/history", nil)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	f := diffFinal{Status: str(is, "status"), Assignee: str(is, "assignee"), CloseReason: str(is, "close_reason"), ClosedAtSet: str(is, "closed_at") != ""}
	for _, k := range []string{"holder", "actor", "owner", "lock_holder"} {
		if h := str(lk, k); h != "" {
			f.LockHolder = h
			break
		}
	}
	return f, raw
}

// reap: fake advances past TTL and runs one sweep; real waits for the real
// ClaimReaper (300s TTL + 30s cadence) by polling unrecorded GETs.
func (e *diffEnv) reap(t *testing.T, id string) {
	if !e.real {
		e.sim.Clock.Advance(fleetsim.DefaultLockTTL + time.Second)
		if got := e.sim.Server.ReapStaleClaims(); len(got) != 1 || got[0] != id {
			t.Fatalf("fake reaper reverted %v", got)
		}
		return
	}
	deadline := time.Now().Add(9 * time.Minute)
	for time.Now().Before(deadline) {
		var is map[string]any
		if code, _, err := e.getJSON("/issues/"+id, &is); err == nil && code == 200 && is["status"] == "open" {
			t.Logf("real reaper reverted %s at %s", id, time.Now().UTC().Format(time.RFC3339))
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("real reaper did not revert %s within 9m", id)
}

// createRealIssue creates the issue through the stack's Loom serve API; the
// fake side seeds the identical row.
func createRealIssue(t *testing.T, loomAPI, ws, title string) string {
	body := fmt.Sprintf(`{"title":%q,"issue_type":"task","priority":2,"description":"fd659d differential","design":"approved design"}`, title)
	resp, err := http.Post(loomAPI+"/api/workspaces/"+ws+"/issues", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("create issue: %d %s", resp.StatusCode, b)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	id, _ := m["id"].(string)
	if id == "" {
		if d, ok := m["data"].(map[string]any); ok {
			id, _ = d["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("create issue: no id in %s", b)
	}
	return id
}

var volatileKeys = map[string]bool{"created_at": true, "updated_at": true, "closed_at": true}

// compareBody checks every field the fake emits against the real body.
// Volatile timestamps are compared for presence only. Extra real fields are
// returned separately (informational).
func compareBody(fake, real string) (diffs, extra []string) {
	fake, real = strings.TrimSpace(fake), strings.TrimSpace(real)
	if fake == "" || real == "" {
		if fake != real {
			diffs = append(diffs, fmt.Sprintf(" presence: fake %q real %q", fake, real))
		}
		return
	}
	var fv, rv any
	if err := json.Unmarshal([]byte(fake), &fv); err != nil {
		return []string{" fake body not JSON"}, nil
	}
	if err := json.Unmarshal([]byte(real), &rv); err != nil {
		return []string{" real body not JSON: " + real}, nil
	}
	walkCompare("", fv, rv, &diffs, &extra)
	return
}

func walkCompare(path string, f, r any, diffs, extra *[]string) {
	switch fv := f.(type) {
	case map[string]any:
		rm, ok := r.(map[string]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("%s: fake object, real %T", path, r))
			return
		}
		keys := make([]string, 0, len(fv))
		for k := range fv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			rvk, ok := rm[k]
			if !ok {
				*diffs = append(*diffs, fmt.Sprintf("%s.%s: missing in real (fake %v)", path, k, fv[k]))
				continue
			}
			if volatileKeys[k] {
				continue
			}
			walkCompare(path+"."+k, fv[k], rvk, diffs, extra)
		}
		for k := range rm {
			if _, ok := fv[k]; !ok {
				*extra = append(*extra, path+"."+k)
			}
		}
	case []any:
		ra, ok := r.([]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("%s: fake array, real %T", path, r))
			return
		}
		if len(fv) != len(ra) {
			*diffs = append(*diffs, fmt.Sprintf("%s: len fake %d real %d", path, len(fv), len(ra)))
		}
		for i := 0; i < len(fv) && i < len(ra); i++ {
			walkCompare(fmt.Sprintf("%s[%d]", path, i), fv[i], ra[i], diffs, extra)
		}
	default:
		if !reflect.DeepEqual(f, r) {
			*diffs = append(*diffs, fmt.Sprintf("%s: fake %v real %v", path, f, r))
		}
	}
}

type scenario struct {
	name string
	run  func(t *testing.T, e *diffEnv, id string)
}

func logErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Logf("%s: err=%v (recorded, compared)", what, err)
	}
}

func claimVia(t *testing.T, e *diffEnv, a *simAttempt, id string) {
	t.Helper()
	err := e.step(a.name+"-claimTask", func() error {
		if !a.s.claimTask(a.ap, "") {
			return fmt.Errorf("claimTask failed: %v", a.ap.LastError)
		}
		return nil
	})
	if err != nil || a.ap.AssignedTaskID != id {
		t.Fatalf("%s claimTask (real=%v): err=%v assigned=%q want %q", a.name, e.real, err, a.ap.AssignedTaskID, id)
	}
}

func workerClose(t *testing.T, e *diffEnv, attempt, id string) {
	t.Helper()
	b := e.backend(attempt+"-worker", simOperator)
	w := &fleetsim.ScriptedWorker{Attempt: attempt + "-worker", Backend: b,
		Steps: []fleetsim.Step{fleetsim.CloseStep(id, "", "Local mode dogfood implementation completed.")}}
	logErr(t, attempt+" worker close", e.step(attempt+"-worker", func() error { _, err := w.Run(context.Background()); return err }))
}

var diffScenarios = []scenario{
	{"control-clean", func(t *testing.T, e *diffEnv, id string) {
		// Matched clean control: the owner's worker closes while its claim is
		// live, then the owner's supervisor releases its lock.
		old := e.attempt("old", simOldActor)
		claimVia(t, e, old, id)
		workerClose(t, e, "old", id)
		_ = e.step("old-release", func() error { old.s.releaseAssignedTaskClaim(old.ap, id); return nil })
	}},
	{"s6-release-then-write", func(t *testing.T, e *diffEnv, id string) {
		a := e.attempt("old", simOldActor)
		claimVia(t, e, a, id)
		_ = e.step("old-release", func() error { a.s.releaseAssignedTaskClaim(a.ap, id); return nil })
		workerClose(t, e, "old", id)
	}},
	{"contended-claim", func(t *testing.T, e *diffEnv, id string) {
		a := e.attempt("old", simOldActor)
		claimVia(t, e, a, id)
		b := e.attempt("successor", simNewActor)
		logErr(t, "contended claim", e.step("successor-claim", func() error { return b.s.claimIssueForAgent(b.ap, id, "diff") }))
		// non-holder release-lock (successor never got AssignedTaskID; call with id directly)
		_ = e.step("successor-release", func() error { b.s.releaseAssignedTaskClaim(b.ap, id); return nil })
		workerClose(t, e, "old", id)
	}},
	{"s5-same-actor-reclaim", func(t *testing.T, e *diffEnv, id string) {
		a := e.attempt("old", simOldActor)
		claimVia(t, e, a, id)
		a2 := e.attempt("old-resume", simOldActor)
		logErr(t, "same-actor reclaim", e.step("resume-claim", func() error { return a2.s.claimIssueForAgent(a2.ap, id, "diff") }))
		workerClose(t, e, "old", id)
	}},
	{"s10-close-unclaimed", func(t *testing.T, e *diffEnv, id string) {
		workerClose(t, e, "stray", id)
	}},
	{"stale-write-run1", func(t *testing.T, e *diffEnv, id string) {
		// Calibrated transcript shape (e034de04 run1): old claims, lock
		// expires, reaper reverts, successor claims, old worker assign+close,
		// old finalize release-lock, recovery release-lock.
		old := e.attempt("old", simOldActor)
		claimVia(t, e, old, id)
		e.reap(t, id)
		succ := e.attempt("successor", simNewActor)
		claimVia(t, e, succ, id)
		workerClose(t, e, "old", id)
		_ = e.step("old-finalize", func() error { old.s.releaseAssignedTaskClaim(old.ap, id); return nil })
		var rerr error
		_ = e.step("old-recover", func() error {
			rerr = e.backend(old.name, simDaemonActor).ReleaseIssueLock(context.Background(), id, simOldActor)
			return nil
		})
		t.Logf("recovery release-lock (real=%v) err=%v", e.real, rerr)
	}},
}

type sideResult struct {
	Records []diffRec         `json:"records"`
	Final   diffFinal         `json:"final"`
	Raw     map[string]string `json:"raw_observe,omitempty"`
}

type scenarioResult struct {
	Scenario string     `json:"scenario"`
	Issue    string     `json:"issue"`
	Fake     sideResult `json:"fake"`
	Real     sideResult `json:"real"`
	Diffs    []string   `json:"diffs"`
	Extra    []string   `json:"real_extra_fields"`
}

func TestFleetDBDiff(t *testing.T) {
	url, api, ws, out := os.Getenv("FLEETDB_DIFF_URL"), os.Getenv("LOOM_DIFF_API"), os.Getenv("FLEETDB_DIFF_WS"), os.Getenv("FLEETDB_DIFF_OUT")
	if url == "" || api == "" || ws == "" || out == "" {
		t.Skip("FLEETDB_DIFF_URL, LOOM_DIFF_API, FLEETDB_DIFF_WS, FLEETDB_DIFF_OUT required")
	}
	only := os.Getenv("FLEETDB_DIFF_ONLY")
	for _, sc := range diffScenarios {
		if only != "" && !strings.Contains(","+only+",", ","+sc.name+",") {
			continue
		}
		t.Run(sc.name, func(t *testing.T) {
			id := createRealIssue(t, api, ws, "fd659d "+sc.name)
			real := &diffEnv{real: true, ws: ws, url: url, scen: sc.name, plain: &http.Client{Timeout: 10 * time.Second}}
			var m map[string]any
			_, _, _ = real.getJSON("/issues/"+id, &m)
			title, _ := m["title"].(string)
			fake := &diffEnv{ws: ws, scen: sc.name, sim: fleetsim.New(time.Now().UTC().Truncate(time.Second), ws, fleetsim.Guards{})}
			fake.sim.Server.Seed(fleetsim.Issue{ID: id, Title: title, Design: "approved design", Priority: 2})

			res := scenarioResult{Scenario: sc.name, Issue: id}
			defer func() {
				_ = os.MkdirAll(out, 0o755)
				b, _ := json.MarshalIndent(res, "", "  ")
				_ = os.WriteFile(filepath.Join(out, sc.name+".json"), b, 0o644)
			}()

			sc.run(t, fake, id)
			res.Fake.Records = fake.records()
			res.Fake.Final, res.Fake.Raw = fake.final(id)
			sc.run(t, real, id)
			res.Real.Records = real.records()
			res.Real.Final, res.Real.Raw = real.final(id)

			if u := fake.sim.Server.Unmodeled(); len(u) != 0 {
				res.Diffs = append(res.Diffs, fmt.Sprintf("fake unmodeled routes: %v", u))
			}
			fr, rr := res.Fake.Records, res.Real.Records
			if len(fr) != len(rr) {
				res.Diffs = append(res.Diffs, fmt.Sprintf("request count: fake %d real %d", len(fr), len(rr)))
			}
			for i := 0; i < len(fr) && i < len(rr); i++ {
				f, r := fr[i], rr[i]
				if f.Attempt != r.Attempt || f.Method != r.Method || f.Path != r.Path || f.Query != r.Query || f.Actor != r.Actor || f.Body != r.Body {
					res.Diffs = append(res.Diffs, fmt.Sprintf("#%d request: fake %s %s %s?%s actor=%s body=%s | real %s %s %s?%s actor=%s body=%s",
						i+1, f.Attempt, f.Method, f.Path, f.Query, f.Actor, f.Body, r.Attempt, r.Method, r.Path, r.Query, r.Actor, r.Body))
				}
				if f.Status != r.Status {
					res.Diffs = append(res.Diffs, fmt.Sprintf("#%d %s %s status: fake %d real %d (rid %s)", i+1, f.Method, f.Path, f.Status, r.Status, r.RequestID))
				}
				d, x := compareBody(f.RespBody, r.RespBody)
				for _, s := range d {
					res.Diffs = append(res.Diffs, fmt.Sprintf("#%d %s %s body%s (rid %s)", i+1, f.Method, f.Path, s, r.RequestID))
				}
				for _, s := range x {
					res.Extra = append(res.Extra, fmt.Sprintf("#%d %s %s%s", i+1, f.Method, f.Path, s))
				}
			}
			if res.Fake.Final != res.Real.Final {
				res.Diffs = append(res.Diffs, fmt.Sprintf("final: fake %+v real %+v", res.Fake.Final, res.Real.Final))
			}
			for i, r := range rr {
				t.Logf("real #%d %s %s %s?%s actor=%s -> %d rid=%s", i+1, r.Attempt, r.Method, r.Path, r.Query, r.Actor, r.Status, r.RequestID)
			}
			t.Logf("final fake=%+v real=%+v", res.Fake.Final, res.Real.Final)
			if len(res.Diffs) != 0 {
				t.Errorf("DIVERGENCE %s (%s):\n%s", sc.name, id, strings.Join(res.Diffs, "\n"))
			}
		})
	}
}
