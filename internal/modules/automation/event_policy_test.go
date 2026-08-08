package automation

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

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
		{name: "ordinary system reserved actor", eventType: "approval", origin: string(EventOriginSystem), source: SourceKindInternal, actor: "system:cron", want: true},
		{name: "base execution outcome", eventType: execution.RunFinishedEventType, origin: string(EventOriginSystem), source: execution.RunFinishedSourceKind, actor: execution.RunFinishedActorRef, sourceEventID: "run-finished:child:completed", want: true},
		{name: "automation internal outcome", eventType: execution.RunFinishedEventType, origin: string(EventOriginSystem), source: SourceKindInternal, actor: execution.RunFinishedActorRef, sourceEventID: "run-finished:h:abc:failed", want: true},
		{name: "external spoof", eventType: execution.RunFinishedEventType, origin: "external", source: "github", actor: execution.RunFinishedActorRef, sourceEventID: "run-finished:child:completed"},
		{name: "workflow spoof", eventType: execution.RunFinishedEventType, origin: "workflow", source: SourceKindInternal, actor: execution.RunFinishedActorRef, sourceEventID: "run-finished:child:completed"},
		{name: "reserved actor prefix", eventType: execution.RunFinishedEventType, origin: "external", source: "github", actor: "system:cron", sourceEventID: "run-finished:child:completed"},
		{name: "wrong source", eventType: execution.RunFinishedEventType, origin: string(EventOriginSystem), source: "github", actor: execution.RunFinishedActorRef, sourceEventID: "run-finished:child:completed"},
		{name: "noncanonical source id", eventType: execution.RunFinishedEventType, origin: string(EventOriginSystem), source: execution.RunFinishedSourceKind, actor: execution.RunFinishedActorRef, sourceEventID: "delivery-1"},
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
	policy := EventPolicy{}
	if policy.EligibleForAdmission("pull_request", "external", "github", "system:cron", "delivery-1") {
		t.Fatal("external reserved system actor was eligible for admission")
	}
	if policy.EligibleForAdmission("issue.created", "workflow", SourceKindInternal, "system", "event-1") {
		t.Fatal("workflow reserved system actor was eligible for admission")
	}
	if !policy.EligibleForAdmission("pull_request", string(EventOriginSystem), SourceKindInternal, "system:cron", "event-1") {
		t.Fatal("system-owned ordinary event was unexpectedly rejected")
	}
}
