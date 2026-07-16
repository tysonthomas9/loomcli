package runtime //nolint:revive // The approved target architecture names this platform mechanism runtime.

import "time"

// HostStatus is the lifecycle state of a Host.
type HostStatus string

const (
	HostCreated  HostStatus = "created"
	HostRunning  HostStatus = "running"
	HostStopping HostStatus = "stopping"
	HostStopped  HostStatus = "stopped"
)

// ComponentStatus is the latest observed lifecycle state of a component.
type ComponentStatus string

const (
	ComponentRegistered ComponentStatus = "registered"
	ComponentStarting   ComponentStatus = "starting"
	ComponentHealthy    ComponentStatus = "healthy"
	ComponentDegraded   ComponentStatus = "degraded"
	ComponentStopped    ComponentStatus = "stopped"
)

// ComponentHealth is a race-safe point-in-time view of one component. A
// degraded component does not implicitly make the hosting process unhealthy.
type ComponentHealth struct {
	ID                  ComponentID
	Status              ComponentStatus
	Policy              Policy
	InFlight            bool
	Runs                uint64
	Successes           uint64
	Failures            uint64
	Timeouts            uint64
	Panics              uint64
	ConsecutiveFailures uint64
	LastStartedAt       time.Time
	LastFinishedAt      time.Time
	LastSuccessAt       time.Time
	LastFailureAt       time.Time
	NextRunAt           time.Time
	LastError           string
}

// Snapshot is a deterministic, ID-sorted view of host and component health.
type Snapshot struct {
	Status     HostStatus
	Components []ComponentHealth
}
