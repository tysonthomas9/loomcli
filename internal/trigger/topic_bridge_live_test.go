package trigger_test

// Opt-in LIVE test for the pub/sub bridge: the real fleet-db topic client and
// the real TopicBridge against a running broker. Skipped unless
// LOOM_LIVE_FLEET_URL is set.
//
// This exists for the same reason the journal bridge's live test does — the
// durable-consume contract (lease token as a header capability, CAS ack,
// broker-tracked attempts, opaque cursors) is exactly the kind of thing a fake
// can agree with while the real broker disagrees.
//
//	fleet-db -addr :8099 -redis-db 15 \
//	  -auth-bootstrap-admin-actor gatetest -auth-bootstrap-admin-key KEY
//	POST /api/v1/admin/workspaces {"key":"BUS"}
//	POST /api/v1/BUS/topics/deploys/messages {"id":"m1","kind":"deploy.requested",...}
//	LOOM_LIVE_FLEET_URL=http://localhost:8099 LOOM_LIVE_FLEET_KEY=KEY \
//	  go test ./internal/trigger/ -run LiveTopic -v

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

func TestLiveTopicBridgeConsumesAndDispatches(t *testing.T) {
	baseURL := os.Getenv("LOOM_LIVE_FLEET_URL")
	if baseURL == "" {
		t.Skip("set LOOM_LIVE_FLEET_URL (and LOOM_LIVE_FLEET_KEY) to run the live topic probe")
	}
	ws := os.Getenv("LOOM_LIVE_TOPIC_WORKSPACE")
	if ws == "" {
		ws = "BUS"
	}
	topic := "deploys"
	ctx := t.Context()

	client, err := fleetdb.New(fleetdb.Config{
		BaseURL: baseURL, Actor: "gatetest", APIKey: os.Getenv("LOOM_LIVE_FLEET_KEY"),
	})
	if err != nil {
		t.Fatalf("fleetdb.New: %v", err)
	}
	consumer, ok := client.TriggerEvents().(store.TopicConsumer)
	if !ok {
		t.Fatalf("fleetdb client does not implement store.TopicConsumer")
	}
	publisher, ok := client.TriggerEvents().(store.TopicPublisher)
	if !ok {
		t.Fatalf("fleetdb client does not implement store.TopicPublisher")
	}

	// The broadcast half round-trips, and the cursor it hands back is usable.
	msgs, cursor, _, err := publisher.Read(ctx, ws, topic, "0", 100, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatalf("topic %q is empty — publish the fixture messages first", topic)
	}
	if cursor == "" {
		t.Fatalf("Read returned no cursor")
	}
	t.Logf("live: read %d messages, cursor %q, first kind %q trace %v",
		len(msgs), cursor, msgs[0].Kind, msgs[0].Trace)

	// The durable half: drive the real bridge with a unique subscriber so this
	// run owns its own cursor and does not fight a previous one.
	s := memstore.New()
	seedTopicRouter(t, s, ws)
	bridge := &trigger.TopicBridge{
		Source: &trigger.InternalSource{Store: s}, Consumer: consumer,
		WorkspaceKey: ws, Topic: topic,
		Subscriber: "live-probe-" + t.Name(),
	}
	t.Cleanup(func() { _ = bridge.Close(ctx) })

	out, err := bridge.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.LeaseHeldElsewhere {
		t.Fatalf("lease held elsewhere on a private subscriber — unexpected")
	}
	if out.Emitted == 0 {
		t.Fatalf("bridge emitted nothing (result %+v)", out)
	}
	if out.Acked != out.Emitted {
		t.Fatalf("result = %+v, want every dispatched message acked", out)
	}

	evs, err := s.TriggerEvents().List(ctx, ws, store.TriggerEventFilter{})
	if err != nil || len(evs) == 0 {
		t.Fatalf("router events = %+v (err %v), want at least one", evs, err)
	}
	var found *domain.TriggerEvent
	for _, ev := range evs {
		if ev.EventType == "deploy.requested" {
			found = ev
		}
	}
	if found == nil {
		t.Fatalf("no deploy.requested event; got %+v", evs)
	}
	t.Logf("live: emitted=%d acked=%d, event %s subject=%s actor=%q",
		out.Emitted, out.Acked, found.EventType, found.SubjectRef, found.ActorRef)

	// A second pass must be a clean no-op: the cursor was durably acked, so
	// nothing redelivers.
	again, err := bridge.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if again.Emitted != 0 {
		t.Fatalf("second pass = %+v, want nothing redelivered after a durable ack", again)
	}
}

func seedTopicRouter(t *testing.T, s *memstore.Store, ws string) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws, DriverID: "topic-bot", Name: "topic-bot",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: ws, VersionID: "v1", DriverID: "topic-bot", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: ws, BindingID: "b-deploy", Name: "b-deploy",
		SourceKind: "internal", RouteKey: "internal.deploy.requested", Topic: "deploys",
		DriverID: "topic-bot", DriverVersionID: "v1", TargetEntrypoint: "run",
		SubjectKeyTemplate: "{{subject_ref}}|{{attrs.env}}",
		ConcurrencyPolicy:  domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
}
