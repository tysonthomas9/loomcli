package automation

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCanonicalModelWireFieldParity(t *testing.T) {
	tests := []struct {
		name   string
		typeOf reflect.Type
		want   []string
	}{
		{
			name: "binding", typeOf: reflect.TypeOf(Binding{}),
			want: []string{
				"WorkspaceKey:workspace_key", "BindingID:binding_id", "Name:name", "SourceKind:source_kind",
				"SourceRef:source_ref,omitempty", "SourceConfigRef:source_config_ref,omitempty", "RouteKey:route_key,omitempty",
				"Method:method,omitempty", "PathTemplate:path_template,omitempty", "Topic:topic,omitempty",
				"EventTypePatterns:event_type_patterns,omitempty", "FilterRef:filter_ref,omitempty", "DriverID:driver_id",
				"DriverVersionID:driver_version_id", "TargetEntrypoint:target_entrypoint,omitempty",
				"TargetAgentServiceID:target_agent_service_id,omitempty", "ConcurrencyPolicy:concurrency_policy",
				"IdempotencyPolicy:idempotency_policy,omitempty", "AuthPolicy:auth_policy,omitempty",
				"SubjectKeyTemplate:subject_key_template,omitempty",
				"ActorFilter:actor_filter,omitempty", "RetryMaxAttempts:retry_max_attempts,omitempty",
				"RetryBackoffSeconds:retry_backoff_seconds,omitempty", "Schedule:schedule,omitempty",
				"ScheduleTimezone:schedule_timezone,omitempty", "Permissions:permissions,omitempty", "Enabled:enabled",
				"CreatedAt:created_at", "UpdatedAt:updated_at",
			},
		},
		{
			name: "event", typeOf: reflect.TypeOf(Event{}),
			want: []string{
				"WorkspaceKey:workspace_key", "EventID:event_id", "TriggerBindingID:trigger_binding_id,omitempty",
				"SourceKind:source_kind", "SourceEventID:source_event_id,omitempty", "EventType:event_type",
				"RouteKey:route_key,omitempty", "SubjectRef:subject_ref,omitempty", "ActorRef:actor_ref,omitempty",
				"EmittingRunID:emitting_run_id,omitempty", "ParentEventID:parent_event_id,omitempty", "EpicID:epic_id,omitempty",
				"Origin:origin,omitempty", "HopDepth:hop_depth,omitempty", "OccurredAt:occurred_at", "ReceivedAt:received_at",
				"IdempotencyKey:idempotency_key,omitempty", "RawPayloadRef:raw_payload_ref,omitempty",
				"RawPayloadDigest:raw_payload_digest,omitempty", "SignatureStatus:signature_status,omitempty",
				"ReplayOfEventID:replay_of_event_id,omitempty", "Payload:payload,omitempty", "SubjectAttrs:subject_attrs,omitempty",
			},
		},
		{
			name: "delivery", typeOf: reflect.TypeOf(Delivery{}),
			want: []string{
				"WorkspaceKey:workspace_key", "DeliveryID:delivery_id", "TriggerEventID:trigger_event_id",
				"TriggerBindingID:trigger_binding_id", "Status:status", "SubjectKey:subject_key,omitempty",
				"RejectionReason:rejection_reason,omitempty", "DriverRunID:driver_run_id,omitempty",
				"DriverID:driver_id,omitempty", "DriverVersionID:driver_version_id,omitempty",
				"TargetEntrypoint:target_entrypoint,omitempty", "TargetAgentServiceID:target_agent_service_id,omitempty",
				"SourceKind:source_kind,omitempty",
				"ConcurrencyPolicy:concurrency_policy,omitempty", "RetryMaxAttempts:retry_max_attempts,omitempty",
				"RetryBackoffSeconds:retry_backoff_seconds,omitempty", "Attempt:attempt",
				"NextRetryAt:next_retry_at,omitempty", "ErrorClass:error_class,omitempty", "CreatedAt:created_at", "UpdatedAt:updated_at",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := make([]string, 0, test.typeOf.NumField())
			for index := 0; index < test.typeOf.NumField(); index++ {
				field := test.typeOf.Field(index)
				got = append(got, field.Name+":"+field.Tag.Get("json"))
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("wire fields:\n got %v\nwant %v", got, test.want)
			}
		})
	}
}

func TestModelCompatibilityMethodsAndConstants(t *testing.T) {
	if DefaultTriggerRetryMaxAttempts != 5 || DefaultTriggerRetryBackoffSeconds != 30 ||
		TriggerDeliveryErrorRetriesExhausted != "retries_exhausted" {
		t.Fatal("domain compatibility constants changed")
	}
	var nilFilter *ActorFilter
	if !nilFilter.IsZero() || nilFilter.Clone() != nil {
		t.Fatal("nil ActorFilter compatibility failed")
	}
	filter := &ActorFilter{ExcludeActorKinds: []string{"workflow"}, AllowActors: []string{"agent"}}
	clone := filter.Clone()
	clone.ExcludeActorKinds[0] = "system"
	if filter.ExcludeActorKinds[0] != "workflow" || filter.IsZero() {
		t.Fatal("ActorFilter clone was not defensive")
	}
	event := &Event{}
	event.NormalizeProvenance()
	if event.Origin != EventOriginExternal {
		t.Fatalf("normalized origin = %q", event.Origin)
	}
	for _, status := range []DeliveryStatus{
		DeliveryAccepted, DeliveryRejected, DeliveryDuplicate, DeliveryQueued, DeliveryDispatched,
		DeliveryFailed, DeliveryReplayed, DeliverySuperseded, DeliveryHeld,
	} {
		if !status.IsValid() {
			t.Fatalf("status %q is invalid", status)
		}
	}
	if DeliveryStatus("unknown").IsValid() {
		t.Fatal("unknown delivery status accepted")
	}
}

func TestEventCanonicalEventID(t *testing.T) {
	tests := []struct {
		name  string
		event *Event
		want  string
		ok    bool
	}{
		{name: "source identity", event: &Event{EventID: "stored-1", SourceEventID: "source-1"}, want: "source-1", ok: true},
		{name: "durable fallback", event: &Event{EventID: "stored-1"}, want: "stored-1", ok: true},
		{name: "padded durable", event: &Event{EventID: " stored-1 "}},
		{name: "padded source", event: &Event{EventID: "stored-1", SourceEventID: " source-1 "}},
		{name: "whitespace source", event: &Event{EventID: "stored-1", SourceEventID: "   "}},
		{name: "missing durable", event: &Event{SourceEventID: "source-1"}},
		{name: "nil"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := test.event.CanonicalEventID()
			if got != test.want || ok != test.ok {
				t.Fatalf("CanonicalEventID = %q, %v; want %q, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestPatternSubjectAndInternalEventCompatibility(t *testing.T) {
	if err := validatePattern("github.{issue,pull_request}.*"); err != nil {
		t.Fatalf("valid pattern: %v", err)
	}
	if !matchAny([]string{"bad{", "github.{issue,pull_request}.*"}, "github.issue.opened") {
		t.Fatal("valid sibling pattern did not match after malformed pattern")
	}
	for _, invalid := range []string{"", "github..opened", "github.issue*", "github.{issue,}.opened"} {
		if validatePattern(invalid) == nil {
			t.Fatalf("invalid pattern %q accepted", invalid)
		}
	}
	key, err := renderSubjectKey("{{event_type}}|{{attrs.repo}}|{{subject_ref}}", subjectInputs{
		bindingID: "binding", eventType: "issue.opened", subjectRef: "7", attrs: map[string]string{"repo": "loom"},
	})
	if err != nil || key != "issue.opened|loom|7" {
		t.Fatalf("subject key = %q, %v", key, err)
	}
	if got, err := renderSubjectKey("", subjectInputs{bindingID: "binding", subjectRef: "7"}); err != nil || got != "binding|7" {
		t.Fatalf("default subject = %q, %v", got, err)
	}
	if _, err := renderSubjectKey("{{attrs.missing}}", subjectInputs{}); err == nil {
		t.Fatal("missing subject attribute accepted")
	}
	if got, err := normalizeInternalEventType(" Issue.Create "); err != nil || got != "issue.created" {
		t.Fatalf("normalized type = %q, %v", got, err)
	}
}

func TestFingerprintStableForOmittedOccurrenceTimeButConflictsForChangedExplicitTime(t *testing.T) {
	base := EventReservation{
		Event: &Event{
			WorkspaceKey: "ws", SourceKind: "github", SourceEventID: "delivery", EventType: "issue.opened",
			RouteKey: "github.issue.opened", Origin: EventOriginExternal, IdempotencyKey: "github:delivery",
			RawPayloadDigest: "sha256:x",
		},
		Payload: []byte(`{"x":1}`), SubjectAttrs: map[string]string{"b": "2", "a": "1"},
	}
	second := base
	second.Event = cloneEvent(base.Event)
	second.Payload = cloneRawMessage(base.Payload)
	second.SubjectAttrs = map[string]string{"a": "1", "b": "2"}
	firstFingerprint, err := eventFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := eventFingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatalf("fingerprints differ: %s != %s", firstFingerprint, secondFingerprint)
	}
	explicitFirst := base
	explicitFirst.Event = cloneEvent(base.Event)
	explicitFirst.Event.OccurredAt = time.Unix(1, 0)
	explicitSecond := explicitFirst
	explicitSecond.Event = cloneEvent(explicitFirst.Event)
	explicitSecond.Event.OccurredAt = time.Unix(2, 0)
	explicitFingerprint, err := eventFingerprint(explicitFirst)
	if err != nil {
		t.Fatal(err)
	}
	changedFingerprint, err := eventFingerprint(explicitSecond)
	if err != nil {
		t.Fatal(err)
	}
	if explicitFingerprint == changedFingerprint {
		t.Fatal("changed explicit occurrence time did not change fingerprint")
	}
	second.Payload[0] = 'X'
	if string(base.Payload) != `{"x":1}` {
		t.Fatal("test reservation payload unexpectedly aliased")
	}
	original := &Event{Payload: json.RawMessage(`{"x":1}`), SubjectAttrs: map[string]string{"a": "1"}}
	cloned := cloneEvent(original)
	cloned.Payload[0] = 'X'
	cloned.SubjectAttrs["a"] = "changed"
	if string(original.Payload) != `{"x":1}` || original.SubjectAttrs["a"] != "1" {
		t.Fatal("cloneEvent did not deep-copy durable payload fields")
	}
}

func TestEventFingerprintExcludesExecutionOwnerPrecondition(t *testing.T) {
	base := EventReservation{
		Event: &Event{
			WorkspaceKey: "ws", SourceKind: SourceKindInternal, SourceEventID: "emission-1",
			EventType: "issue.created", RouteKey: "internal.issue.created", Origin: EventOriginWorkflow,
			EmittingRunID: "run-1", IdempotencyKey: InternalEventIdempotencyKey("ws", "emission-1"),
		},
		ExecutionNodeID: "node-a", ExecutionLeaseID: "lease-a", ExecutionFence: 7,
		Payload: []byte(`{"issue":"LOOM-1"}`),
	}
	want, err := eventFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	handoff := base
	handoff.ExecutionNodeID = "node-b"
	handoff.ExecutionLeaseID = "lease-b"
	handoff.ExecutionFence = 8
	got, err := eventFingerprint(handoff)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("owner handoff altered immutable fingerprint: got %s want %s", got, want)
	}
	changedRun := base
	changedRun.Event = cloneEvent(base.Event)
	changedRun.Event.EmittingRunID = "run-2"
	got, err = eventFingerprint(changedRun)
	if err != nil {
		t.Fatal(err)
	}
	if got == want {
		t.Fatal("emitting run change did not alter immutable fingerprint")
	}
	changedContent := base
	changedContent.Payload = []byte(`{"issue":"LOOM-2"}`)
	got, err = eventFingerprint(changedContent)
	if err != nil {
		t.Fatal(err)
	}
	if got == want {
		t.Fatal("semantic payload change did not alter immutable fingerprint")
	}
}
