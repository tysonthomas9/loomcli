package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	webuiservice "github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestAuditServiceMergesFleetAndDaemonEventsChronologically(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	t3 := t2.Add(time.Minute)
	reader := &auditReaderStub{
		events: []store.AuditEvent{{
			ID: fmt.Sprintf("%d-0", t2.UnixMilli()), Timestamp: t2, Actor: "agent-1", Action: "issue.claim",
			EntityType: "issue", EntityID: "ISSUE-1", WorkspaceID: "WS",
		}},
		cursor: fmt.Sprintf("%d-0", t2.UnixMilli()),
	}
	service := newAuditServiceHarness(t, reader, []events.Event{
		mustRuntimeEvent(t, events.DaemonStarted, t1, "", events.DaemonStartedData{PID: 42, WorkspaceID: "WS"}),
		mustRuntimeEvent(t, events.AgentStarted, t3, "agent-1", events.AgentStartedData{PID: 99}),
	})

	got, next, err := service.ListAuditEvents(t.Context(), "WS", "", 10, "", "")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("events = %+v", got)
	}
	if got[0].Action != "daemon.start" || got[0].ID != "" || got[0].Details["source"] != "daemon" {
		t.Fatalf("first synthetic event = %+v", got[0])
	}
	if got[1].Action != "issue.claim" || got[1].ID == "" {
		t.Fatalf("fleet event = %+v", got[1])
	}
	if got[2].Action != "agent.session_start" || got[2].Actor != "agent-1" || got[2].EntityID != "agent-1" {
		t.Fatalf("session event = %+v", got[2])
	}
	if next != reader.cursor {
		t.Fatalf("next cursor = %q, want %q", next, reader.cursor)
	}
}

func TestAuditServiceIncludesDaemonBlockReasonAndAppliesFilters(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	reader := &auditReaderStub{events: []store.AuditEvent{{
		ID: fmt.Sprintf("%d-0", timestamp.UnixMilli()), Timestamp: timestamp, Actor: "other", Action: "issue.update",
		EntityType: "issue", EntityID: "ISSUE-1", WorkspaceID: "WS",
	}}, cursor: fmt.Sprintf("%d-0", timestamp.UnixMilli())}
	service := newAuditServiceHarness(t, reader, []events.Event{
		mustRuntimeEvent(t, events.TaskFailed, timestamp.Add(time.Second), "agent-1", events.TaskFailedData{
			TaskID: "ISSUE-1", Error: "backend authentication expired", ErrorClass: "auth_failure",
		}),
		mustRuntimeEvent(t, events.AgentStarted, timestamp.Add(2*time.Second), "agent-2", events.AgentStartedData{PID: 10}),
	})

	got, _, err := service.ListAuditEvents(t.Context(), "WS", "", 10, "agent-1", "agent-1")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 1 || got[0].Action != "agent.blocked" {
		t.Fatalf("filtered events = %+v", got)
	}
	if got[0].Details["error"] != "backend authentication expired" || got[0].Details["error_class"] != "auth_failure" {
		t.Fatalf("block details = %#v", got[0].Details)
	}
	if reader.filter != (store.AuditEventFilter{EntityID: "agent-1", Actor: "agent-1"}) {
		t.Fatalf("fleet filter = %+v", reader.filter)
	}
}

func TestDaemonAuditEventAttributesCircuitBlockToAgent(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	runtimeEvent := mustRuntimeEvent(t, events.CircuitOpened, timestamp, "agent-1", events.CircuitOpenedData{
		RateLimitCount: 5,
	})

	got, ok := daemonAuditEvent("WS", runtimeEvent)
	if !ok {
		t.Fatal("circuit event was not mapped")
	}
	if got.Action != "agent.blocked" || got.Actor != "agent-1" || got.EntityID != "agent-1" {
		t.Fatalf("circuit audit event = %+v", got)
	}
	if got.Details["rate_limit_count"] != float64(5) {
		t.Fatalf("circuit details = %#v", got.Details)
	}
}

func TestAuditServiceSinceCursorExcludesOlderDaemonEvents(t *testing.T) {
	t.Parallel()
	sinceTime := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	service := newAuditServiceHarness(t, &auditReaderStub{cursor: fmt.Sprintf("%d-0", sinceTime.UnixMilli())}, []events.Event{
		mustRuntimeEvent(t, events.AgentStarted, sinceTime, "agent-1", events.AgentStartedData{PID: 1}),
		mustRuntimeEvent(t, events.AgentStopped, sinceTime.Add(time.Second), "agent-1", events.AgentStoppedData{PID: 1, ExitCode: 0}),
	})
	got, _, err := service.ListAuditEvents(t.Context(), "WS", fmt.Sprintf("%d-0", sinceTime.UnixMilli()), 10, "", "")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 1 || got[0].Action != "agent.session_exit" {
		t.Fatalf("events after cursor = %+v", got)
	}
}

func TestAuditServiceTruncatedDaemonPageDoesNotSkipFleetCursor(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, time.August, 14, 9, 59, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t1.Add(time.Minute)
	since := fmt.Sprintf("%d-0", t0.UnixMilli())
	reader := &auditReaderStub{
		events: []store.AuditEvent{{
			ID: fmt.Sprintf("%d-0", t2.UnixMilli()), Timestamp: t2, Actor: "agent-1", Action: "issue.claim",
			EntityType: "issue", EntityID: "ISSUE-1", WorkspaceID: "WS",
		}},
		cursor: fmt.Sprintf("%d-0", t2.UnixMilli()),
	}
	service := newAuditServiceHarness(t, reader, []events.Event{
		mustRuntimeEvent(t, events.AgentStarted, t1, "agent-1", events.AgentStartedData{PID: 99}),
	})

	got, next, err := service.ListAuditEvents(t.Context(), "WS", since, 1, "", "")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 1 || got[0].Action != "agent.session_start" {
		t.Fatalf("truncated page = %+v", got)
	}
	if next != since {
		t.Fatalf("next cursor = %q, want unchanged %q so unseen fleet event is not skipped", next, since)
	}
}

func TestAuditServiceUnavailableAndWorkspaceNotFoundMappings(t *testing.T) {
	t.Parallel()
	_, _, err := NewAuditService(nil).ListAuditEvents(t.Context(), "WS", "", 100, "", "")
	var serviceErr *webuiservice.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != webuiservice.KindUnavailable {
		t.Fatalf("nil store error = %#v", err)
	}

	base := memstore.New()
	st := auditServiceStore{Store: base, trigger: &auditReaderStub{}}
	_, _, err = NewAuditService(st).ListAuditEvents(t.Context(), "MISSING", "", 100, "", "")
	if !errors.As(err, &serviceErr) || serviceErr.Kind != webuiservice.KindNotFound {
		t.Fatalf("missing workspace error = %#v", err)
	}
}

func TestAuditServiceResolvesConfiguredDaemonEventsDir(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	base := memstore.New()
	if _, err := base.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workspacePath := t.TempDir()
	if err := bootstrap.MutateWorkspaceLocalState("WS", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		return nil
	}); err != nil {
		t.Fatalf("seed local workspace: %v", err)
	}
	if _, err := base.Daemon().Upsert(t.Context(), &domain.DaemonProfile{WorkspaceKey: "WS", EventsDir: "runtime/audit"}); err != nil {
		t.Fatalf("set daemon profile: %v", err)
	}
	service := NewAuditService(base)
	if got, want := service.daemonEventsDir(t.Context(), "WS"), filepath.Join(workspacePath, "runtime", "audit"); got != want {
		t.Fatalf("events dir = %q, want %q", got, want)
	}
}

func newAuditServiceHarness(t *testing.T, reader *auditReaderStub, runtimeEvents []events.Event) *auditService {
	t.Helper()
	base := memstore.New()
	if _, err := base.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	service := NewAuditService(auditServiceStore{Store: base, trigger: reader})
	service.resolveEventsDir = func(context.Context, string) string { return "/events" }
	service.readDaemonEvents = func(string) ([]events.Event, error) {
		return append([]events.Event(nil), runtimeEvents...), nil
	}
	return service
}

func mustRuntimeEvent(t *testing.T, eventType events.EventType, timestamp time.Time, agent string, data any) events.Event {
	t.Helper()
	event, err := events.NewEvent(eventType, agent, "backend-dev", "", data)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	event.Timestamp = timestamp
	return event
}

type auditServiceStore struct {
	store.Store
	trigger store.TriggerEventStore
}

func (s auditServiceStore) TriggerEvents() store.TriggerEventStore { return s.trigger }

type auditReaderStub struct {
	events []store.AuditEvent
	cursor string
	filter store.AuditEventFilter
}

func (*auditReaderStub) Get(context.Context, string, string) (*domain.TriggerEvent, error) {
	return nil, domain.ErrNotFound
}

func (*auditReaderStub) List(context.Context, string, store.TriggerEventFilter) ([]*domain.TriggerEvent, error) {
	return nil, nil
}

func (s *auditReaderStub) ListAuditEvents(
	_ context.Context,
	_ string,
	_ string,
	_ int,
	filter store.AuditEventFilter,
) ([]store.AuditEvent, string, bool, error) {
	s.filter = filter
	return append([]store.AuditEvent(nil), s.events...), s.cursor, false, nil
}

func (*auditReaderStub) SubscribeAuditEvents(context.Context, string, string, store.AuditEventFilter) (<-chan store.AuditEvent, <-chan error) {
	eventCh := make(chan store.AuditEvent)
	errCh := make(chan error)
	close(eventCh)
	close(errCh)
	return eventCh, errCh
}
