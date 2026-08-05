package eventpolicy

import "testing"

func TestRunFinishedAwaitEligibility(t *testing.T) {
	tests := []struct {
		name                      string
		eventType, origin, source string
		actor, sourceEventID      string
		want                      bool
	}{
		{name: "ordinary external event remains eligible", eventType: "approval", origin: "external", source: "approval", actor: "alice", want: true},
		{name: "ordinary external reserved actor", eventType: "approval", origin: "external", source: "approval", actor: "system"},
		{name: "ordinary workflow reserved actor", eventType: "approval", origin: "workflow", source: SourceKindInternal, actor: "system:cron"},
		{name: "ordinary system reserved actor", eventType: "approval", origin: OriginSystem, source: SourceKindInternal, actor: "system:cron", want: true},
		{name: "base execution outcome", eventType: RunFinishedEventType, origin: OriginSystem, source: SourceKindExecution, actor: RunFinishedActorRef, sourceEventID: "run-finished:child:completed", want: true},
		{name: "automation internal outcome", eventType: RunFinishedEventType, origin: OriginSystem, source: SourceKindInternal, actor: RunFinishedActorRef, sourceEventID: "run-finished:h:abc:failed", want: true},
		{name: "external spoof", eventType: RunFinishedEventType, origin: "external", source: "github", actor: RunFinishedActorRef, sourceEventID: "run-finished:child:completed"},
		{name: "workflow spoof", eventType: RunFinishedEventType, origin: "workflow", source: SourceKindInternal, actor: RunFinishedActorRef, sourceEventID: "run-finished:child:completed"},
		{name: "reserved actor prefix", eventType: RunFinishedEventType, origin: "external", source: "github", actor: "system:cron", sourceEventID: "run-finished:child:completed"},
		{name: "wrong source", eventType: RunFinishedEventType, origin: OriginSystem, source: "github", actor: RunFinishedActorRef, sourceEventID: "run-finished:child:completed"},
		{name: "noncanonical source id", eventType: RunFinishedEventType, origin: OriginSystem, source: SourceKindExecution, actor: RunFinishedActorRef, sourceEventID: "delivery-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := EligibleForAwait(test.eventType, test.origin, test.source, test.actor, test.sourceEventID); got != test.want {
				t.Fatalf("EligibleForAwait() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestReservedSystemActorRef(t *testing.T) {
	for actor, want := range map[string]bool{
		"system": true, "system:cron": true, "systematic": false,
		"System": false, "user:system": false,
	} {
		if got := IsReservedSystemActorRef(actor); got != want {
			t.Errorf("IsReservedSystemActorRef(%q) = %t, want %t", actor, got, want)
		}
	}
}

func TestNonSystemReservedActorIsIneligibleForAdmission(t *testing.T) {
	policy := Policy{}
	if policy.EligibleForAdmission("pull_request", "external", "github", "system:cron", "delivery-1") {
		t.Fatal("external reserved system actor was eligible for admission")
	}
	if policy.EligibleForAdmission("issue.created", "workflow", SourceKindInternal, "system", "event-1") {
		t.Fatal("workflow reserved system actor was eligible for admission")
	}
	if !policy.EligibleForAdmission("pull_request", OriginSystem, SourceKindInternal, "system:cron", "event-1") {
		t.Fatal("system-owned ordinary event was unexpectedly rejected")
	}
}
