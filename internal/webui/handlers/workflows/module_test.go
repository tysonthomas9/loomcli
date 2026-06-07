package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestCreateWorkflowRunPassesRawPayload(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo", stringsReader(`{"nested":{"ok":true},"items":[1,2]}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	stored, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
	if err != nil {
		t.Fatalf("get stored run: %v", err)
	}
	if string(stored.Payload) != `{"nested":{"ok":true},"items":[1,2]}` {
		t.Fatalf("payload = %s, want raw request JSON", stored.Payload)
	}
}

func TestGetRunEventsReturnsDriverRunEvents(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	run, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "TEST",
		RunID:           "run-1",
		DriverID:        "demo",
		DriverVersionID: "version-1",
		Payload:         json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST/runs/"+run.RunID+"/events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var page domain.PlatformEventsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode events page: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].EntityID != "run-1" || page.Events[0].EntityType != "driver_run" {
		t.Fatalf("events page = %+v, want one driver_run event", page)
	}
}

func TestCreateWorkflowVersionRejectsPackageManifest(t *testing.T) {
	st := memstore.New()
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	body := `{"files":{"package.json":"{}","workflows/demo.ts":"export async function run(){ return {}; }"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions", stringsReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func seededWorkflowStore(t *testing.T, ctx context.Context) store.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey:    "TEST",
		DriverID:        "demo",
		Name:            "demo",
		OwnerType:       domain.DriverOwnerUser,
		ActiveVersionID: "version-1",
		Status:          domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-1",
		DriverID:         "demo",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	return st
}

func stringsReader(s string) *strings.Reader {
	return strings.NewReader(s)
}
