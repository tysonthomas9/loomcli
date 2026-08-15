package auditcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAuditPhraseRendering(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		event store.AuditEvent
		want  string
	}{
		{
			name:  "claim",
			event: store.AuditEvent{Actor: "api-architect-1", Action: "issue.claim", EntityID: "TEAMBACKEND-1"},
			want:  "api-architect-1 claimed TEAMBACKEND-1",
		},
		{
			name: "label removal",
			event: store.AuditEvent{
				Actor: "reviewer", Action: "label.remove", EntityID: "TEAMBACKEND-1",
				Metadata: map[string]string{"label": "architect"},
			},
			want: "label architect removed on TEAMBACKEND-1 by reviewer",
		},
		{
			name:  "agent role language",
			event: store.AuditEvent{Actor: "operator", Action: "role.update", EntityID: "backend-dev"},
			want:  "operator updated agent role backend-dev",
		},
		{
			name:  "unknown stays raw",
			event: store.AuditEvent{Actor: "operator", Action: "future.action", EntityID: "x"},
			want:  "future.action",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := auditPhrase(tc.event); got != tc.want {
				t.Fatalf("auditPhrase() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuditPhraseCoversFleetDBActions(t *testing.T) {
	t.Parallel()
	actions := []string{
		"issue.create", "issue.update", "issue.close", "issue.reopen", "issue.delete",
		"issue.claim", "issue.release", "issue.assign", "issue.defer", "issue.undefer",
		"dep.add", "dep.remove", "label.add", "label.remove", "metadata.set", "metadata.remove", "comment.add",
		"workspace.create", "workspace.update", "workspace.delete",
		"repo.create", "repo.update", "repo.delete", "agent.create", "agent.update", "agent.delete",
		"driver_run.create", "driver_run.claim", "driver_run.heartbeat", "driver_run.finish", "driver_run.recover",
		"driver_run.suspend", "driver_run.resume", "role.create", "role.update", "role.delete", "daemon.update",
	}
	for _, action := range actions {
		event := store.AuditEvent{Actor: "actor", Action: action, EntityID: "entity"}
		if got := auditPhrase(event); got == action || got == "" {
			t.Errorf("action %q has no human phrase, got %q", action, got)
		}
	}
}

func TestAuditEventMatchesEntityAndActor(t *testing.T) {
	t.Parallel()
	event := store.AuditEvent{EntityID: "ISSUE-1", Actor: "agent-1"}
	cases := []struct {
		filter store.AuditEventFilter
		want   bool
	}{
		{filter: store.AuditEventFilter{}, want: true},
		{filter: store.AuditEventFilter{EntityID: "ISSUE-1"}, want: true},
		{filter: store.AuditEventFilter{Actor: "agent-1"}, want: true},
		{filter: store.AuditEventFilter{EntityID: "ISSUE-1", Actor: "agent-1"}, want: true},
		{filter: store.AuditEventFilter{EntityID: "ISSUE-2"}, want: false},
		{filter: store.AuditEventFilter{Actor: "agent-2"}, want: false},
	}
	for _, tc := range cases {
		if got := auditEventMatches(event, tc.filter); got != tc.want {
			t.Errorf("auditEventMatches(%+v) = %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestAuditJSONModeKeepsStdoutPureAndDecorationOnStderr(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, time.August, 14, 12, 30, 0, 0, time.UTC)
	reader := &fakeAuditTriggerStore{
		history: []store.AuditEvent{{
			ID: "1770000000000-0", Timestamp: timestamp, Actor: "agent-1", Action: "issue.claim",
			EntityType: "issue", EntityID: "ISSUE-1", WorkspaceID: "WS",
		}},
		cursor: "1770000000000-0",
	}
	cmd := newAuditCommand(auditCommandTestDeps(reader))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--output", "json", "--follow"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("audit command: %v", err)
	}
	if strings.Contains(stdout.String(), "Following") {
		t.Fatalf("stdout contains decoration: %q", stdout.String())
	}
	var raw map[string]any
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&raw); err != nil {
		t.Fatalf("decode NDJSON event: %v\n%s", err, stdout.String())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout contains more than one JSON event: %v\n%s", err, stdout.String())
	}
	for _, key := range []string{"id", "timestamp", "actor", "action", "entity_type", "entity_id", "workspace_id"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("raw event missing snake_case key %q: %#v", key, raw)
		}
	}
	if got := stderr.String(); got != "Following audit events for workspace WS...\n" {
		t.Fatalf("stderr = %q", got)
	}
	if reader.gotFilter != (store.AuditEventFilter{}) {
		t.Fatalf("history filter = %+v", reader.gotFilter)
	}
	if reader.followCursor != reader.cursor {
		t.Fatalf("follow cursor = %q, want %q", reader.followCursor, reader.cursor)
	}
}

func auditCommandTestDeps(reader *fakeAuditTriggerStore) commandDeps {
	base := memstore.New()
	st := auditStore{Store: base, trigger: reader}
	return commandDeps{
		withActiveWorkspace: func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
			return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, "WS")
		},
	}
}

type auditStore struct {
	store.Store
	trigger store.TriggerEventStore
}

func (s auditStore) TriggerEvents() store.TriggerEventStore { return s.trigger }

type fakeAuditTriggerStore struct {
	history      []store.AuditEvent
	cursor       string
	gotFilter    store.AuditEventFilter
	followCursor string
}

func (f *fakeAuditTriggerStore) Get(context.Context, string, string) (*domain.TriggerEvent, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeAuditTriggerStore) List(context.Context, string, store.TriggerEventFilter) ([]*domain.TriggerEvent, error) {
	return nil, nil
}

func (f *fakeAuditTriggerStore) ListAuditEvents(
	_ context.Context,
	_ string,
	_ string,
	_ int,
	filter store.AuditEventFilter,
) ([]store.AuditEvent, string, bool, error) {
	f.gotFilter = filter
	return append([]store.AuditEvent(nil), f.history...), f.cursor, false, nil
}

func (f *fakeAuditTriggerStore) SubscribeAuditEvents(
	_ context.Context,
	_ string,
	cursor string,
	_ store.AuditEventFilter,
) (<-chan store.AuditEvent, <-chan error) {
	f.followCursor = cursor
	eventsCh := make(chan store.AuditEvent)
	errsCh := make(chan error)
	close(eventsCh)
	close(errsCh)
	return eventsCh, errsCh
}
