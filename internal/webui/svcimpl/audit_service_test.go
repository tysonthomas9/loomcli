package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	if got[0].Action != "daemon.start" || !strings.HasPrefix(got[0].ID, "daemon:") || got[0].Details["source"] != "daemon" {
		t.Fatalf("first synthetic event = %+v", got[0])
	}
	if got[1].Action != "issue.claim" || got[1].ID == "" {
		t.Fatalf("fleet event = %+v", got[1])
	}
	if got[2].Action != "agent.session_start" || got[2].Actor != "agent-1" || got[2].EntityID != "agent-1" {
		t.Fatalf("session event = %+v", got[2])
	}
	if next != "" {
		t.Fatalf("next cursor = %q, want end of trail", next)
	}
}

func TestAuditServiceInitialPageReturnsNewestWindow(t *testing.T) {
	t0 := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	history := make([]store.AuditEvent, 51)
	for i := range history {
		ts := t0.Add(time.Duration(i) * time.Minute)
		history[i] = store.AuditEvent{
			ID: fmt.Sprintf("%d-0", ts.UnixMilli()), Timestamp: ts,
			Action: fmt.Sprintf("event-%02d", i), WorkspaceID: "WS",
		}
	}
	service := newAuditServiceHarness(t, &auditReaderStub{history: history}, nil)

	got, next, err := service.ListAuditEvents(t.Context(), "WS", "", 50, "", "")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 50 || got[0].Action != "event-01" || got[49].Action != "event-50" {
		t.Fatalf("event window = %q..%q (%d), want event-01..event-50", got[0].Action, got[len(got)-1].Action, len(got))
	}
	if next != got[0].ID {
		t.Fatalf("next cursor = %q, want oldest returned event %q", next, got[0].ID)
	}
}

func TestAuditServiceDaemonOnlyPagesAdvanceAndTerminate(t *testing.T) {
	t0 := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	service := newAuditServiceHarness(t, &auditReaderStub{}, []events.Event{
		mustRuntimeEvent(t, events.AgentStarted, t0, "agent-1", events.AgentStartedData{PID: 1}),
		mustRuntimeEvent(t, events.AgentStopped, t0.Add(time.Second), "agent-1", events.AgentStoppedData{PID: 1, ExitCode: 0}),
	})

	newest, cursor, err := service.ListAuditEvents(t.Context(), "WS", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	older, terminal, err := service.ListAuditEvents(t.Context(), "WS", cursor, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if newest[0].Action != "agent.session_exit" || older[0].Action != "agent.session_start" {
		t.Fatalf("pages = %q then %q", newest[0].Action, older[0].Action)
	}
	if cursor == "" || terminal != "" {
		t.Fatalf("cursors = %q then %q, want advancing cursor then terminal empty cursor", cursor, terminal)
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

func TestAuditServiceCursorPagesTowardOlderDaemonEvents(t *testing.T) {
	t.Parallel()
	sinceTime := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	service := newAuditServiceHarness(t, &auditReaderStub{}, []events.Event{
		mustRuntimeEvent(t, events.AgentStarted, sinceTime, "agent-1", events.AgentStartedData{PID: 1}),
		mustRuntimeEvent(t, events.AgentStopped, sinceTime.Add(time.Second), "agent-1", events.AgentStoppedData{PID: 1, ExitCode: 0}),
	})
	newest, cursor, err := service.ListAuditEvents(t.Context(), "WS", "", 1, "", "")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	got, _, err := service.ListAuditEvents(t.Context(), "WS", cursor, 1, "", "")
	if err != nil {
		t.Fatalf("ListAuditEvents older page: %v", err)
	}
	if len(newest) != 1 || newest[0].Action != "agent.session_exit" || len(got) != 1 || got[0].Action != "agent.session_start" {
		t.Fatalf("newest = %+v, older = %+v", newest, got)
	}
}

func TestAuditServiceMixedPagesDoNotSkipFleetEvents(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, time.August, 14, 9, 59, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t1.Add(time.Minute)
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

	got, next, err := service.ListAuditEvents(t.Context(), "WS", "", 1, "", "")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 1 || got[0].Action != "issue.claim" {
		t.Fatalf("truncated page = %+v", got)
	}
	older, terminal, err := service.ListAuditEvents(t.Context(), "WS", next, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 1 || older[0].Action != "agent.session_start" || terminal != "" {
		t.Fatalf("older page = %+v, terminal cursor = %q", older, terminal)
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
	events  []store.AuditEvent
	history []store.AuditEvent
	cursor  string
	filter  store.AuditEventFilter
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
	after string,
	limit int,
	filter store.AuditEventFilter,
) ([]store.AuditEvent, string, bool, error) {
	s.filter = filter
	if s.history != nil {
		start := 0
		for i, event := range s.history {
			if event.ID == after {
				start = i + 1
				break
			}
		}
		end := len(s.history)
		if limit > 0 && start+limit < end {
			end = start + limit
		}
		events := append([]store.AuditEvent(nil), s.history[start:end]...)
		cursor := after
		if len(events) > 0 {
			cursor = events[len(events)-1].ID
		}
		return events, cursor, end < len(s.history), nil
	}
	return append([]store.AuditEvent(nil), s.events...), s.cursor, false, nil
}

func (*auditReaderStub) SubscribeAuditEvents(context.Context, string, string, store.AuditEventFilter) (<-chan store.AuditEvent, <-chan error) {
	eventCh := make(chan store.AuditEvent)
	errCh := make(chan error)
	close(eventCh)
	close(errCh)
	return eventCh, errCh
}
