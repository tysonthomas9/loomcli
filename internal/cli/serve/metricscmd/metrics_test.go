package metricscmd

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

func TestCollectWorkerStatusCountsUsesDurableMonitorProjection(t *testing.T) {
	data := &monitor.MonitorData{Agents: []monitor.AgentStatus{
		{Name: "coder", RoleKind: "worker", LiveStatus: "working", ActiveTaskID: "TASK-1"},
		{Name: "planner", RoleKind: "worker", Status: "ready"},
		{Name: "reviewer", RoleKind: "worker", LastErrorClass: "backend_unavailable"},
		{Name: "lead", RoleKind: "interactive", LiveStatus: "working"},
	}}

	got := collectWorkerStatusCounts(data)
	if got["active"] != 1 || got["idle"] != 1 || got["blocked"] != 1 {
		t.Fatalf("counts = %v, want active=1 idle=1 blocked=1", got)
	}
}

func TestCollectWorkerStatusCountsNilProjection(t *testing.T) {
	got := collectWorkerStatusCounts(nil)
	if got["active"] != 0 || got["idle"] != 0 || got["blocked"] != 0 {
		t.Fatalf("counts = %v, want zeros", got)
	}
}
