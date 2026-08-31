package fleet

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/advisoryactor"
)

const (
	testAdvisoryActor = "operator@local"
	testProcessActor  = "process@local"
)

// writeFleetDenial emits fleet-db's *native nested* error envelope. Getting
// this shape wrong is how a negative test passes vacuously: loomcli flattens
// {"error":{"code":...,"message":...}} into apiResponse, and the flat form is
// something fleet-db never sends.
func writeFleetDenial(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

// advisoryTestServer records the X-Actor of every request and answers each one
// from respond, which sees the 1-based request index.
type advisoryTestServer struct {
	mu     sync.Mutex
	actors []string
}

func (s *advisoryTestServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.actors...)
}

func newAdvisoryBackend(t *testing.T, respond func(w http.ResponseWriter, n int, actor string)) (*FleetBackend, *advisoryTestServer, func()) {
	t.Helper()
	rec := &advisoryTestServer{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-Actor")
		rec.mu.Lock()
		rec.actors = append(rec.actors, actor)
		n := len(rec.actors)
		rec.mu.Unlock()
		respond(w, n, actor)
	}))
	fb, err := New(Config{BaseURL: ts.URL, WorkspaceID: "test-ws", Actor: testProcessActor})
	if err != nil {
		ts.Close()
		t.Fatalf("New: %v", err)
	}
	return fb, rec, ts.Close
}

func advisoryCtx(actor string) context.Context {
	return advisoryactor.With(context.Background(), actor)
}

func updateTitle(ctx context.Context, fb *FleetBackend, actor string) error {
	title := "Updated"
	return fb.Update(ctx, "test-1", backend.UpdateParams{Actor: actor, Title: &title})
}

// The #542 regression guard: when the operator actor IS authorized, the write
// is attributed to them and nothing about the fallback interferes.
func TestAdvisoryActor_AuthorizedWriteKeepsAttribution(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
		respondOK(w, json.RawMessage(`{}`))
	})
	defer closeFn()

	if err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := rec.seen(); len(got) != 1 || got[0] != testAdvisoryActor {
		t.Errorf("actors = %v, want one request as %q", got, testAdvisoryActor)
	}
}

// PUPPET-320's acceptance criterion: a 403 on the operator actor must not
// leave the board write-dead.
func TestAdvisoryActor_NoRoleDenialRetriesAsProcessActor(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, n int, _ string) {
		if n == 1 {
			writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
			return
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer closeFn()

	if err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor); err != nil {
		t.Fatalf("Update returned %v, want nil (the write must survive a role-less operator)", err)
	}
	got := rec.seen()
	if len(got) != 2 {
		t.Fatalf("requests = %v, want exactly two (advisory attempt then process retry)", got)
	}
	if got[0] != testAdvisoryActor || got[1] != testProcessActor {
		t.Errorf("actors = %v, want [%q %q]", got, testAdvisoryActor, testProcessActor)
	}
}

// An unstamped context means the caller never asked for advisory attribution
// (claim/release pass the lock holder's identity). The 403 must surface so the
// lock is never rewritten under the wrong actor.
func TestAdvisoryActor_UnstampedOverrideIsNeverRetried(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
	}{
		{name: "no stamp", ctx: context.Background()},
		{name: "stamp names a different actor", ctx: advisoryCtx("someone-else@local")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
				writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
			})
			defer closeFn()

			err := fb.ClaimIssueAsActor(tc.ctx, "test-1", time.Minute, "agent-1")
			if err == nil {
				t.Fatal("ClaimIssueAsActor succeeded; the 403 must surface")
			}
			if got := rec.seen(); len(got) != 1 || got[0] != "agent-1" {
				t.Errorf("actors = %v, want a single claim as agent-1", got)
			}
		})
	}
}

// "insufficient permissions" means the actor has a role that lacks the
// permission. Retrying as the process actor would be privilege escalation.
func TestAdvisoryActor_InsufficientPermissionsIsHonestAndNotRetried(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
		writeFleetDenial(w, http.StatusForbidden, "forbidden", "insufficient permissions")
	})
	defer closeFn()

	err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor)
	if err == nil {
		t.Fatal("Update succeeded; a permission denial must not fall back")
	}
	if got := rec.seen(); len(got) != 1 {
		t.Errorf("requests = %v, want exactly one", got)
	}
	if strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("403 still reported as an authentication failure: %v", err)
	}
	for _, want := range []string{testAdvisoryActor, "test-ws", "insufficient permissions"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A 401 is a real credential failure: never retried, and its message keeps the
// wording it had before 403 was split out of the same branch.
func TestAdvisoryActor_UnauthorizedIsUnchanged(t *testing.T) {
	for _, msg := range []string{"authentication required", "invalid credentials"} {
		t.Run(msg, func(t *testing.T) {
			fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
				writeFleetDenial(w, http.StatusUnauthorized, "unauthorized", msg)
			})
			defer closeFn()

			err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor)
			if err == nil {
				t.Fatal("Update succeeded; a 401 must keep failing")
			}
			if got := rec.seen(); len(got) != 1 {
				t.Errorf("requests = %v, want exactly one", got)
			}
			// "invalid credentials" is claimed earlier by the error-string
			// matcher (it contains "invalid"); only the message that reaches
			// the status switch carries the 401 wording.
			if msg == "authentication required" && !strings.Contains(err.Error(), "authentication failed") {
				t.Errorf("401 message changed: %v", err)
			}
		})
	}
}

// Both actors role-less: exactly one retry, ever, and the error names both.
func TestAdvisoryActor_ProcessActorAlsoDeniedDoesNotLoop(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
		writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
	})
	defer closeFn()

	err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor)
	if err == nil {
		t.Fatal("Update succeeded; both actors were denied")
	}
	got := rec.seen()
	if len(got) != 2 {
		t.Fatalf("requests = %v, want exactly two", got)
	}
	if got[0] != testAdvisoryActor || got[1] != testProcessActor {
		t.Errorf("actors = %v, want the advisory attempt then one process retry", got)
	}
	if !strings.Contains(err.Error(), testAdvisoryActor) || !strings.Contains(err.Error(), testProcessActor) {
		t.Errorf("error %q does not name both actors", err)
	}
}

// LOOM_OPERATOR_ACTOR pointed at the process actor: the retry would be a
// pointless resend, so it is skipped up front.
func TestAdvisoryActor_EqualToProcessActorSkipsRetry(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
		writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
	})
	defer closeFn()

	if err := updateTitle(advisoryCtx(testProcessActor), fb, testProcessActor); err == nil {
		t.Fatal("Update succeeded; the denial must surface")
	}
	if got := rec.seen(); len(got) != 1 {
		t.Errorf("requests = %v, want exactly one", got)
	}
}

// Once denied, later writes skip the doomed attempt until the TTL lapses.
func TestAdvisoryActor_DenialIsCachedAndReprobedAfterTTL(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, actor string) {
		if actor == testAdvisoryActor {
			writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
			return
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer closeFn()

	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	fb.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	// First write probes then retries; the next two go straight out as the
	// process actor.
	if got := rec.seen(); len(got) != 4 {
		t.Fatalf("requests = %v, want 4 (probe+retry, then two direct)", got)
	}

	now = now.Add(advisoryDenialTTL + time.Minute)
	if err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor); err != nil {
		t.Fatalf("write after TTL: %v", err)
	}
	got := rec.seen()
	if len(got) != 6 {
		t.Fatalf("requests = %v, want 6 (the advisory actor is re-probed after the TTL)", got)
	}
	if got[4] != testAdvisoryActor {
		t.Errorf("request 5 actor = %q, want a fresh probe as %q", got[4], testAdvisoryActor)
	}
}

func TestAdvisoryActor_WarnsOncePerWindow(t *testing.T) {
	var buf strings.Builder
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(restore)

	fb, _, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, actor string) {
		if actor == testAdvisoryActor {
			writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
			return
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer closeFn()

	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	fb.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if err := updateTitle(advisoryCtx(testAdvisoryActor), fb, testAdvisoryActor); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	logged := buf.String()
	if n := strings.Count(logged, "has no role in the issue backend"); n != 1 {
		t.Errorf("warned %d times, want once per actor per window\n%s", n, logged)
	}
	for _, want := range []string{testAdvisoryActor, "test-ws", "fleet-db:acl:global-roles"} {
		if !strings.Contains(logged, want) {
			t.Errorf("warning does not mention %q:\n%s", want, logged)
		}
	}
}

// The doctor probe must report the denial, not paper over it.
func TestCheckActorAccess_ReportsDenialWithoutFallback(t *testing.T) {
	fb, rec, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
		writeFleetDenial(w, http.StatusForbidden, "forbidden", "workspace access denied")
	})
	defer closeFn()

	err := fb.CheckActorAccess(context.Background(), testAdvisoryActor)
	if err == nil {
		t.Fatal("CheckActorAccess returned nil for a role-less actor")
	}
	if got := rec.seen(); len(got) != 1 || got[0] != testAdvisoryActor {
		t.Errorf("requests = %v, want a single probe as %q", got, testAdvisoryActor)
	}
	if !strings.Contains(err.Error(), "is not authorized in workspace") {
		t.Errorf("probe error %q is not the authorization verdict doctor matches on", err)
	}
}

func TestCheckActorAccess_PassAndValidation(t *testing.T) {
	fb, _, closeFn := newAdvisoryBackend(t, func(w http.ResponseWriter, _ int, _ string) {
		respondOK(w, json.RawMessage(`{"count":3}`))
	})
	defer closeFn()

	if err := fb.CheckActorAccess(context.Background(), testAdvisoryActor); err != nil {
		t.Errorf("CheckActorAccess = %v, want nil", err)
	}
	if err := fb.CheckActorAccess(context.Background(), ""); err == nil {
		t.Error("empty actor accepted")
	}
	if fb.Workspace() != "test-ws" {
		t.Errorf("Workspace() = %q, want %q", fb.Workspace(), "test-ws")
	}
}
