package fleetsim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cpDo serves one request directly (no interposer) and decodes the body.
func cpDo(t *testing.T, s *Server, method, path, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, "/api/v1/LOCALMODE"+path, strings.NewReader(body))
	r.Header.Set("X-Actor", "harness")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// Ownership leases follow the pinned Redis Lua scripts and handlers: a live
// lease of another owner refuses acquire with 409 already_claimed; every
// successful acquire bumps the fence; a live same-owner re-acquire keeps the
// token; renew checks token (403) before liveness (410); release checks the
// token only — no fence, no liveness.
func TestControlPlaneOwnershipLeaseSemantics(t *testing.T) {
	sim := New(t0, "LOCALMODE", Guards{})
	s := sim.Server
	acq := func(owner string) (int, map[string]any) {
		return cpDo(t, s, "POST", "/agent-ownership-leases/ag/acquire", `{"owner_id":"`+owner+`","ttl_seconds":1800}`, nil)
	}
	tok := func(token string) map[string]string { return map[string]string{"X-Agent-Ownership-Lease-Token": token} }

	if code, m := cpDo(t, s, "POST", "/agent-ownership-leases/ag/acquire", `{}`, nil); code != 400 || errCode(m) != "validation_failed" {
		t.Fatalf("acquire without owner = %d %v", code, m)
	}
	code, a1 := acq("A")
	if code != 200 || a1["fencing_token"].(float64) != 1 {
		t.Fatalf("first acquire = %d %v", code, a1)
	}
	tokenA := a1["token"].(string)
	if code, m := acq("B"); code != 409 || errCode(m) != "already_claimed" {
		t.Fatalf("contested acquire = %d %v", code, m)
	}
	code, a2 := acq("A")
	if code != 200 || a2["token"] != tokenA || a2["fencing_token"].(float64) != 2 {
		t.Fatalf("live same-owner re-acquire = %d %v; want same token, fence 2", code, a2)
	}
	if code, _ := cpDo(t, s, "POST", "/agent-ownership-leases/ag/heartbeat?ttl_seconds=1800", "", tok("wrong")); code != 403 {
		t.Fatalf("renew wrong token = %d", code)
	}
	if code, _ := cpDo(t, s, "POST", "/agent-ownership-leases/nobody/heartbeat", "", tok(tokenA)); code != 404 {
		t.Fatalf("renew missing = %d", code)
	}
	sim.Clock.Advance(10 * time.Minute)
	if code, m := cpDo(t, s, "POST", "/agent-ownership-leases/ag/heartbeat?ttl_seconds=1800", "", tok(tokenA)); code != 200 ||
		m["expires_at"] != t0.Add(40*time.Minute).Format(time.RFC3339) {
		t.Fatalf("renew = %d %v", code, m)
	}
	if code, m := cpDo(t, s, "POST", "/agent-ownership-leases/ag/release", "", tok(tokenA)); code != 200 || m["status"] != "released" {
		t.Fatalf("release = %d %v", code, m)
	}
	if code, m := cpDo(t, s, "POST", "/agent-ownership-leases/ag/heartbeat", "", tok(tokenA)); code != 410 || errCode(m) != "lease_expired" {
		t.Fatalf("renew released = %d %v", code, m)
	}
	code, b := acq("B")
	if code != 200 || b["token"] == tokenA || b["fencing_token"].(float64) != 3 {
		t.Fatalf("acquire after release = %d %v", code, b)
	}
	sim.Clock.Advance(30 * time.Minute)
	if code, _ := cpDo(t, s, "POST", "/agent-ownership-leases/ag/heartbeat", "", tok(b["token"].(string))); code != 410 {
		t.Fatalf("renew at expiry = %d, want 410", code)
	}
	if code, m := cpDo(t, s, "POST", "/agent-ownership-leases/ag/release", "", tok(b["token"].(string))); code != 200 || m["status"] != "released" {
		t.Fatalf("release of expired lease = %d %v (token-only check)", code, m)
	}
}

// Sessions are an unconditional read-modify-write (only the status value is
// validated); session leases: any number per session, token-then-liveness
// renew, token-only release.
func TestControlPlaneSessionAndLeaseSemantics(t *testing.T) {
	sim := New(t0, "LOCALMODE", Guards{})
	s := sim.Server
	if code, m := cpDo(t, s, "POST", "/agent-sessions", `{"session_id":"s1","agent_id":"ag","status":"starting"}`, nil); code != 201 || m["status"] != "starting" {
		t.Fatalf("create = %d %v", code, m)
	}
	if code, m := cpDo(t, s, "POST", "/agent-sessions", `{"session_id":"s1","agent_id":"ag"}`, nil); code != 409 || errCode(m) != "already_exists" {
		t.Fatalf("duplicate create = %d %v", code, m)
	}
	for _, st := range []string{"completed", "running"} {
		if code, m := cpDo(t, s, "PATCH", "/agent-sessions/s1", `{"status":"`+st+`"}`, nil); code != 200 || m["status"] != st {
			t.Fatalf("patch %s = %d %v", st, code, m)
		}
	}
	if code, _ := cpDo(t, s, "PATCH", "/agent-sessions/s1", `{"status":"bogus"}`, nil); code != 400 {
		t.Fatalf("invalid status = %d", code)
	}
	if code, _ := cpDo(t, s, "PATCH", "/agent-sessions/nope", `{"status":"running"}`, nil); code != 404 {
		t.Fatalf("patch missing = %d", code)
	}
	code, l1 := cpDo(t, s, "POST", "/agent-sessions/s1/leases", `{"lease_id":"l1","agent_id":"ag","ttl_seconds":1800}`, nil)
	code2, l2 := cpDo(t, s, "POST", "/agent-sessions/s1/leases", `{"lease_id":"l2","agent_id":"ag"}`, nil)
	if code != 201 || code2 != 201 || l1["fencing_token"].(float64) != 1 || l2["fencing_token"].(float64) != 2 ||
		l2["expires_at"] != t0.Add(5*time.Minute).Format(time.RFC3339) {
		t.Fatalf("leases = %d %v / %d %v", code, l1, code2, l2)
	}
	tok := func(v any) map[string]string { return map[string]string{"X-Agent-Lease-Token": v.(string)} }
	if code, _ := cpDo(t, s, "POST", "/agent-leases/l1/heartbeat", "", tok("x")); code != 403 {
		t.Fatalf("lease renew wrong token = %d", code)
	}
	sim.Clock.Advance(30 * time.Minute)
	if code, _ := cpDo(t, s, "POST", "/agent-leases/l1/heartbeat", "", tok(l1["token"])); code != 410 {
		t.Fatalf("lease renew at expiry = %d", code)
	}
	if code, m := cpDo(t, s, "GET", "/agent-leases/l1", "", nil); code != 200 || m["status"] != "active" {
		t.Fatalf("expired lease before any reaper sweep = %d %v; want still active", code, m)
	}
	if code, m := cpDo(t, s, "POST", "/agent-leases/l1/release", "", tok(l1["token"])); code != 200 || m["status"] != "released" {
		t.Fatalf("release expired lease = %d %v", code, m)
	}
}

// ReapControlPlane follows LeaseReaper.ReapWorkspace: leases expire at
// expires_at <= now; a lease of a terminal session is released; a running
// session with no vouching lease is retired expired/lease_lost after the 5m
// grace from its freshest timestamp; starting waits 30m; the heartbeat rule
// (failed/heartbeat_lost) needs heartbeat evidence 60s past creation.
func TestControlPlaneReaperSemantics(t *testing.T) {
	sim := New(t0, "LOCALMODE", Guards{})
	s := sim.Server
	for _, sess := range []string{
		`{"session_id":"run","agent_id":"ag","status":"running"}`,
		`{"session_id":"start","agent_id":"ag","status":"starting"}`,
		`{"session_id":"done","agent_id":"ag","status":"completed"}`,
		`{"session_id":"vouched","agent_id":"ag","status":"running"}`,
		`{"session_id":"beating","agent_id":"ag","status":"running"}`,
	} {
		if code, _ := cpDo(t, s, "POST", "/agent-sessions", sess, nil); code != 201 {
			t.Fatalf("create %s = %d", sess, code)
		}
	}
	cpDo(t, s, "POST", "/agent-sessions/done/leases", `{"lease_id":"ld","ttl_seconds":1800}`, nil)
	cpDo(t, s, "POST", "/agent-sessions/vouched/leases", `{"lease_id":"lv","ttl_seconds":600}`, nil)
	cpDo(t, s, "POST", "/agent-sessions/beating/leases", `{"lease_id":"lb","ttl_seconds":3600}`, nil)
	hb := t0.Add(2 * time.Minute).Format(time.RFC3339)
	cpDo(t, s, "PATCH", "/agent-sessions/beating", `{"last_heartbeat":"`+hb+`"}`, nil)

	sim.Clock.Set(t0.Add(5*time.Minute - time.Second))
	res := s.ReapControlPlane()
	if len(res.SessionsRetired) != 0 || len(res.LeasesReleased) != 1 || res.LeasesReleased[0] != "ld" {
		t.Fatalf("sweep before grace = %+v", res)
	}
	sim.Clock.Set(t0.Add(5 * time.Minute))
	res = s.ReapControlPlane()
	if len(res.SessionsRetired) != 1 || res.SessionsRetired["run"] != "expired" {
		t.Fatalf("sweep at grace = %+v", res)
	}
	sim.Clock.Set(t0.Add(10 * time.Minute))
	res = s.ReapControlPlane()
	if res.SessionsRetired["vouched"] != "expired" || len(res.LeasesExpired) != 1 {
		t.Fatalf("sweep at vouching lease expiry = %+v", res)
	}
	sim.Clock.Set(t0.Add(12 * time.Minute))
	res = s.ReapControlPlane()
	if res.SessionsRetired["beating"] != "failed" {
		t.Fatalf("heartbeat rule = %+v", res)
	}
	if sess, _ := s.SessionSnapshot("beating"); sess.ErrorClass != "heartbeat_lost" {
		t.Fatalf("beating = %+v", sess)
	}
	if sess, _ := s.SessionSnapshot("start"); sess.Status != "starting" {
		t.Fatalf("starting retired before 30m: %+v", sess)
	}
	sim.Clock.Set(t0.Add(30 * time.Minute))
	if res := s.ReapControlPlane(); res.SessionsRetired["start"] != "expired" {
		t.Fatalf("starting at 30m = %+v", res)
	}
}

// Abandon releases the caller with a deadline error but leaves the request
// in flight; it applies, unanswered, whenever it is delivered.
func TestAbandonThenLateApply(t *testing.T) {
	sim := New(t0, "LOCALMODE", Guards{})
	cs := sim.ControlStore("a", "harness")
	var err error
	sim.Go("rel", func() error {
		_, err = cs.AgentOwnershipLeases().Release(context.Background(), "LOCALMODE", "ag", "tok")
		return nil
	})
	p := sim.Pending()[0]
	sim.Abandon(p.Seq)
	if !sim.Finished("rel") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller not released with deadline error: finished=%v err=%v", sim.Finished("rel"), err)
	}
	if ps := sim.Pending(); len(ps) != 1 || !ps[0].Abandoned {
		t.Fatalf("pending after abandon = %+v", ps)
	}
	rec := sim.Deliver(p.Seq, Apply, time.Time{})
	if !rec.Abandoned || rec.Status != http.StatusNotFound {
		t.Fatalf("late apply = %+v", rec)
	}
	if len(sim.Pending()) != 0 {
		t.Fatal("request still pending after late apply")
	}
}
