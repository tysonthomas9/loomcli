package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// newRouterTestStore seeds a driver + validated version so trigger bindings
// can be created against them.
func newRouterTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := t.Context()
	s := New()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source-v1",
		BundleDigest:     "sha256:bundle-v1",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	return s
}

func routerBindingCreate(bindingID string) store.TriggerBindingCreate {
	return store.TriggerBindingCreate{
		WorkspaceKey:    "WS",
		BindingID:       bindingID,
		Name:            "Router binding " + bindingID,
		SourceKind:      "internal",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		Enabled:         true,
	}
}

func TestTriggerBindingRouterFieldsCreateRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*store.TriggerBindingCreate)
		wantErr error
		check   func(t *testing.T, b *domain.TriggerBinding)
	}{
		{
			name: "all router fields round-trip",
			mutate: func(in *store.TriggerBindingCreate) {
				in.SourceKind = "cron"
				in.SubjectKeyTemplate = "{{subject_ref}}|{{attrs.repo}}"
				in.ActorFilter = &domain.TriggerActorFilter{
					ExcludeActorKinds: []string{"workflow"},
					AllowActors:       []string{"agent:lead"},
				}
				in.RetryMaxAttempts = 3
				in.RetryBackoffSeconds = 60
				in.Schedule = "*/5 * * * *"
				in.ScheduleTimezone = "Europe/Berlin"
			},
			check: func(t *testing.T, b *domain.TriggerBinding) {
				if b.SubjectKeyTemplate != "{{subject_ref}}|{{attrs.repo}}" {
					t.Fatalf("subject_key_template = %q", b.SubjectKeyTemplate)
				}
				if b.ActorFilter == nil || len(b.ActorFilter.ExcludeActorKinds) != 1 || b.ActorFilter.ExcludeActorKinds[0] != "workflow" || len(b.ActorFilter.AllowActors) != 1 || b.ActorFilter.AllowActors[0] != "agent:lead" {
					t.Fatalf("actor_filter = %+v", b.ActorFilter)
				}
				if b.RetryMaxAttempts != 3 || b.RetryBackoffSeconds != 60 {
					t.Fatalf("retry = %d/%d, want 3/60", b.RetryMaxAttempts, b.RetryBackoffSeconds)
				}
				if b.Schedule != "*/5 * * * *" || b.ScheduleTimezone != "Europe/Berlin" {
					t.Fatalf("schedule = %q tz=%q", b.Schedule, b.ScheduleTimezone)
				}
			},
		},
		{
			name: "retry defaults and empty actor filter normalized",
			mutate: func(in *store.TriggerBindingCreate) {
				in.ActorFilter = &domain.TriggerActorFilter{}
			},
			check: func(t *testing.T, b *domain.TriggerBinding) {
				if b.RetryMaxAttempts != domain.DefaultTriggerRetryMaxAttempts || b.RetryBackoffSeconds != domain.DefaultTriggerRetryBackoffSeconds {
					t.Fatalf("retry defaults = %d/%d, want %d/%d", b.RetryMaxAttempts, b.RetryBackoffSeconds, domain.DefaultTriggerRetryMaxAttempts, domain.DefaultTriggerRetryBackoffSeconds)
				}
				if b.ActorFilter != nil {
					t.Fatalf("empty actor_filter = %+v, want nil", b.ActorFilter)
				}
				if b.SubjectKeyTemplate != "" || b.Schedule != "" || b.ScheduleTimezone != "" {
					t.Fatalf("unset router fields leaked: %+v", b)
				}
			},
		},
		{
			name:    "negative retry_max_attempts rejected",
			mutate:  func(in *store.TriggerBindingCreate) { in.RetryMaxAttempts = -1 },
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "negative retry_backoff_seconds rejected",
			mutate:  func(in *store.TriggerBindingCreate) { in.RetryBackoffSeconds = -30 },
			wantErr: domain.ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := newRouterTestStore(t)
			in := routerBindingCreate("binding-1")
			tt.mutate(&in)
			created, err := s.TriggerBindings().Create(ctx, in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Create err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			tt.check(t, created)
			got, err := s.TriggerBindings().Get(ctx, "WS", "binding-1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			tt.check(t, got)
			listed, err := s.TriggerBindings().List(ctx, "WS", store.TriggerBindingFilter{DriverID: "driver-1"})
			if err != nil || len(listed) != 1 {
				t.Fatalf("List = %d bindings err=%v, want 1", len(listed), err)
			}
			tt.check(t, listed[0])
		})
	}
}

func TestTriggerBindingRouterFieldsUpdate(t *testing.T) {
	template := "{{event_type}}|{{subject_ref}}"
	attempts := 7
	backoff := 120
	schedule := "0 9 * * 1"
	tz := "America/Los_Angeles"
	filter := domain.TriggerActorFilter{ExcludeActorKinds: []string{"workflow"}}
	emptyFilter := domain.TriggerActorFilter{}
	tests := []struct {
		name  string
		seed  func(*store.TriggerBindingCreate)
		patch store.TriggerBindingUpdate
		check func(t *testing.T, b *domain.TriggerBinding)
	}{
		{
			name:  "set subject key template",
			patch: store.TriggerBindingUpdate{SubjectKeyTemplate: &template},
			check: func(t *testing.T, b *domain.TriggerBinding) {
				if b.SubjectKeyTemplate != template {
					t.Fatalf("subject_key_template = %q, want %q", b.SubjectKeyTemplate, template)
				}
			},
		},
		{
			name:  "set actor filter",
			patch: store.TriggerBindingUpdate{ActorFilter: &filter},
			check: func(t *testing.T, b *domain.TriggerBinding) {
				if b.ActorFilter == nil || len(b.ActorFilter.ExcludeActorKinds) != 1 || b.ActorFilter.ExcludeActorKinds[0] != "workflow" {
					t.Fatalf("actor_filter = %+v", b.ActorFilter)
				}
			},
		},
		{
			name: "empty actor filter clears existing",
			seed: func(in *store.TriggerBindingCreate) {
				in.ActorFilter = &domain.TriggerActorFilter{AllowActors: []string{"agent:lead"}}
			},
			patch: store.TriggerBindingUpdate{ActorFilter: &emptyFilter},
			check: func(t *testing.T, b *domain.TriggerBinding) {
				if b.ActorFilter != nil {
					t.Fatalf("actor_filter = %+v, want cleared", b.ActorFilter)
				}
			},
		},
		{
			name:  "retry and schedule fields",
			patch: store.TriggerBindingUpdate{RetryMaxAttempts: &attempts, RetryBackoffSeconds: &backoff, Schedule: &schedule, ScheduleTimezone: &tz},
			check: func(t *testing.T, b *domain.TriggerBinding) {
				if b.RetryMaxAttempts != attempts || b.RetryBackoffSeconds != backoff {
					t.Fatalf("retry = %d/%d, want %d/%d", b.RetryMaxAttempts, b.RetryBackoffSeconds, attempts, backoff)
				}
				if b.Schedule != schedule || b.ScheduleTimezone != tz {
					t.Fatalf("schedule = %q tz=%q", b.Schedule, b.ScheduleTimezone)
				}
			},
		},
		{
			name: "empty patch leaves router fields untouched",
			seed: func(in *store.TriggerBindingCreate) {
				in.SubjectKeyTemplate = template
				in.RetryMaxAttempts = 2
			},
			patch: store.TriggerBindingUpdate{},
			check: func(t *testing.T, b *domain.TriggerBinding) {
				if b.SubjectKeyTemplate != template || b.RetryMaxAttempts != 2 {
					t.Fatalf("router fields drifted: template=%q retry=%d", b.SubjectKeyTemplate, b.RetryMaxAttempts)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := newRouterTestStore(t)
			in := routerBindingCreate("binding-1")
			if tt.seed != nil {
				tt.seed(&in)
			}
			if _, err := s.TriggerBindings().Create(ctx, in); err != nil {
				t.Fatalf("Create: %v", err)
			}
			updated, err := s.TriggerBindings().Update(ctx, "WS", "binding-1", tt.patch)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			tt.check(t, updated)
			got, err := s.TriggerBindings().Get(ctx, "WS", "binding-1")
			if err != nil {
				t.Fatalf("Get after update: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func TestTriggerDeliverySubjectKeyAndRouterStatuses(t *testing.T) {
	ctx := t.Context()
	s := newRouterTestStore(t)
	now := time.Now().UTC()
	deliveries := []*domain.TriggerDelivery{
		{WorkspaceKey: "WS", DeliveryID: "delivery-1", TriggerEventID: "event-1", TriggerBindingID: "binding-1", Status: domain.TriggerDeliverySuperseded, SubjectKey: "binding-1|repo-a", CreatedAt: now, UpdatedAt: now},
		{WorkspaceKey: "WS", DeliveryID: "delivery-2", TriggerEventID: "event-2", TriggerBindingID: "binding-1", Status: domain.TriggerDeliveryHeld, SubjectKey: "binding-1|repo-b", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
	}
	for _, delivery := range deliveries {
		if err := s.deliveries.create(delivery); err != nil {
			t.Fatalf("create delivery %s: %v", delivery.DeliveryID, err)
		}
	}

	got, err := s.TriggerDeliveries().Get(ctx, "WS", "delivery-1")
	if err != nil {
		t.Fatalf("Get delivery: %v", err)
	}
	if got.SubjectKey != "binding-1|repo-a" || got.Status != domain.TriggerDeliverySuperseded {
		t.Fatalf("delivery = status %q subject_key %q, want superseded binding-1|repo-a", got.Status, got.SubjectKey)
	}

	held, err := s.TriggerDeliveries().List(ctx, "WS", store.TriggerDeliveryFilter{Status: domain.TriggerDeliveryHeld})
	if err != nil || len(held) != 1 || held[0].DeliveryID != "delivery-2" || held[0].SubjectKey != "binding-1|repo-b" {
		t.Fatalf("List held = %+v err=%v, want delivery-2 with subject key", held, err)
	}
}
