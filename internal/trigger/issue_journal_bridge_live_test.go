package trigger_test

// Opt-in LIVE integration test: the real fleet-db HTTP client, the real
// IssueJournalBridge and the real memstore router, against a running fleet-db.
// Skipped unless LOOM_LIVE_FLEET_URL is set, so the default `go test` run stays
// hermetic.
//
// It exists because every other test in this package hand-writes its journal
// fixtures, and two defects in this feature survived exactly that: the label
// action is label.add (not issue.update), and the reader's entity_type=issue
// filter silently dropped it server-side. Both were invisible to fixtures
// written from the same wrong assumption and obvious the moment a real label
// write went through.
//
//	fleet-db -addr :8099 -redis-db 15 \
//	  -auth-bootstrap-admin-actor gatetest -auth-bootstrap-admin-key KEY
//	POST /api/v1/admin/workspaces {"key":"GATE"}
//	POST /api/v1/GATE/issues      {"title":"..."}
//	POST /api/v1/GATE/issues/GATE-1/labels {"label":"needs-review"}
//	LOOM_LIVE_FLEET_URL=http://localhost:8099 LOOM_LIVE_FLEET_KEY=KEY \
//	  go test ./internal/trigger/ -run Live -v

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

func TestLiveIssueJournalBridgeLabelWrite(t *testing.T) {
	baseURL := os.Getenv("LOOM_LIVE_FLEET_URL")
	if baseURL == "" {
		t.Skip("set LOOM_LIVE_FLEET_URL (and LOOM_LIVE_FLEET_KEY) to run the live bridge probe")
	}
	ws := os.Getenv("LOOM_LIVE_FLEET_WORKSPACE")
	if ws == "" {
		ws = "GATE"
	}
	ctx := t.Context()

	client, err := fleetdb.New(fleetdb.Config{
		BaseURL: baseURL, Actor: "gatetest", APIKey: os.Getenv("LOOM_LIVE_FLEET_KEY"),
	})
	if err != nil {
		t.Fatalf("fleetdb.New: %v", err)
	}
	reader, ok := client.TriggerEvents().(store.IssueJournalReader)
	if !ok {
		t.Fatalf("fleetdb client does not implement store.IssueJournalReader")
	}

	// 1. The real reader must SEE the label write. This is what the
	//    entity_type=issue filter used to drop.
	events, _, _, err := reader.ListIssueEvents(ctx, ws, "0", 100)
	if err != nil {
		t.Fatalf("ListIssueEvents: %v", err)
	}
	var labelEntry *store.JournalEvent
	for i := range events {
		if events[i].Action == trigger.IssueLabelAddAction {
			labelEntry = &events[i]
		}
	}
	if labelEntry == nil {
		t.Fatalf("no %s entry in %d journal entries — is the reader still filtering by entity_type?",
			trigger.IssueLabelAddAction, len(events))
	}
	// 2. The label must arrive in METADATA (the only scalar source).
	if got := labelEntry.Metadata["label"]; got == "" {
		t.Fatalf("live label entry metadata = %v, want a label key", labelEntry.Metadata)
	}
	label := labelEntry.Metadata["label"]

	// 3. Drive the real bridge into a memstore router.
	s := memstore.New()
	seedLiveRouter(t, s, ws)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey:    ws,
		ActionAllowlist: []string{trigger.IssueLabelAddAction},
		Cursors:         liveCursors{},
	}
	res, err := bridge.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.Emitted == 0 {
		t.Fatalf("bridge emitted nothing (skipped=%d)", res.Skipped)
	}

	// 4. The emitted event addresses the ISSUE and renders {{attrs.label}}.
	evs, err := s.TriggerEvents().List(ctx, ws, store.TriggerEventFilter{})
	if err != nil || len(evs) == 0 {
		t.Fatalf("router events = %+v (err %v), want at least one", evs, err)
	}
	ev := evs[0]
	if ev.EventType != trigger.IssueLabelAddAction {
		t.Fatalf("event type = %q, want %q", ev.EventType, trigger.IssueLabelAddAction)
	}
	wantSubject := trigger.IssueJournalSubjectRefPrefix + labelEntry.EntityID
	if ev.SubjectRef != wantSubject {
		t.Fatalf("subject ref = %q, want %q (entity_type is label, entity_id is the ISSUE)", ev.SubjectRef, wantSubject)
	}

	ds, err := s.TriggerDeliveries().List(ctx, ws, store.TriggerDeliveryFilter{})
	if err != nil || len(ds) == 0 {
		t.Fatalf("deliveries = %+v (err %v), want the binding to have been dispatched", ds, err)
	}
	// The binding's template is "{{subject_ref}}|{{attrs.label}}": a rendered
	// key proves the label reached the attrs lane end to end.
	wantKey := wantSubject + "|" + label
	if ds[0].SubjectKey != wantKey {
		t.Fatalf("subject key = %q, want %q — {{attrs.label}} did not render", ds[0].SubjectKey, wantKey)
	}
	t.Logf("live: %s on %s -> await key %q, subject key %q",
		ev.EventType, ev.SubjectRef, domain.AwaitEventKey(ev.EventType, ev.SubjectRef), ds[0].SubjectKey)
}

func seedLiveRouter(t *testing.T, s *memstore.Store, ws string) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws, DriverID: "label-bot", Name: "label-bot",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: ws, VersionID: "v1", DriverID: "label-bot", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: ws, BindingID: "b-label", Name: "b-label",
		SourceKind: "internal", RouteKey: "internal.label.add",
		DriverID: "label-bot", DriverVersionID: "v1", TargetEntrypoint: "run",
		SubjectKeyTemplate: "{{subject_ref}}|{{attrs.label}}",
		ConcurrencyPolicy:  domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
}

// liveCursors reports a stored cursor of "0" so the first pass drains the
// journal from the beginning instead of bootstrap-fast-forwarding to the tail.
type liveCursors struct{}

func (liveCursors) Load(string) (string, bool) { return "0", true }
func (liveCursors) Save(string, string)        {}
