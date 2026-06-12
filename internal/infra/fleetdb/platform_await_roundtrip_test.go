// Round-trip contract test: the shared AW4 await conformance suite
// (store/storetest) running through the AW5 HTTP client against an ephemeral
// embedded fleet-db, proving the memstore and fleet-db backends expose
// identical await semantics through one client.
//
// External test package: the harness uses internal/bootstrap, which itself
// imports this package.
//
// Env-gated like TestEmbeddedFleetDBPersistenceSmoke: it needs a fleet-db
// binary BUILT FROM A TREE THAT INCLUDES THE AW5 AWAIT ROUTES on PATH (an
// older binary 404s every await call). The run-lifecycle harness is nil:
// fleet-db's park->suspend window semantics (pending-resume marker +
// driver_run_already_resumed) deliberately diverge from memstore's strict
// ErrInvalidTransition mechanism, and are covered by fleet-db's own storage
// suite (platform_suspend_test.go) plus the stub-server wire tests here.
package fleetdb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestFleetDBAwaitConformanceRoundTrip(t *testing.T) {
	if os.Getenv("LOOM_RUN_EMBEDDED_SMOKE") != "1" {
		t.Skip("set LOOM_RUN_EMBEDDED_SMOKE=1 (with a freshly built fleet-db binary) to run the await round-trip")
	}
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err != nil {
		t.Skipf("fleet-db binary unavailable: %v", diag.Err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	emb, err := bootstrap.StartEmbedded(ctx, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("StartEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = emb.Stop() })

	client, err := fleetdb.New(fleetdb.Config{BaseURL: emb.URL(), Actor: "await-roundtrip"})
	if err != nil {
		t.Fatalf("fleetdb client: %v", err)
	}

	var wsSeq atomic.Int64
	storetest.RunAwaitConformance(t, func(t testing.TB) *storetest.AwaitHarness {
		ws := fmt.Sprintf("AWRT%d", wsSeq.Add(1))
		if _, err := client.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
			t.Fatalf("create workspace %s: %v", ws, err)
		}
		return &storetest.AwaitHarness{
			Workspace:   ws,
			Awaits:      client.Awaits(),
			AppendEvent: fleetDBAppendEvent(emb.URL(), ws),
			// Runs is nil: see the package comment.
		}
	})
}

// fleetDBAppendEvent journals one trigger event through fleet-db's
// POST /trigger-events route (which indexes it into the await-eligible
// journal in the same transaction) and returns the assigned event ID. Raw
// HTTP because the loomcli store contract has no client-side trigger-event
// create — production events arrive via webhook/loopback dispatch.
func fleetDBAppendEvent(baseURL, ws string) func(t testing.TB, eventType, subjectRef, actorRef string) string {
	return func(t testing.TB, eventType, subjectRef, actorRef string) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"source_kind": "test",
			"event_type":  eventType,
			"subject_ref": subjectRef,
			"actor_ref":   actorRef,
		})
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/"+ws+"/trigger-events", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("build event request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Actor", "await-roundtrip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("create trigger event: %v", err)
		}
		defer resp.Body.Close()
		var created struct {
			EventID string `json:"event_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || resp.StatusCode != http.StatusCreated || created.EventID == "" {
			t.Fatalf("create trigger event: status %d decode err %v id %q", resp.StatusCode, err, created.EventID)
		}
		return created.EventID
	}
}
