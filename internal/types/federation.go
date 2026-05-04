// Package types defines core issue-tracking data structures.
package types

import "time"

// WorkHandoffPayload is the payload structure for work-handoff messages.
// Used by the fleet API to communicate task details between workers.
type WorkHandoffPayload struct {
	// Issue is the issue being handed off.
	Issue *Issue `json:"issue"`

	// Labels are the issue's labels.
	Labels []string `json:"labels,omitempty"`

	// Dependencies are the issue's dependencies.
	Dependencies []*Dependency `json:"dependencies,omitempty"`

	// Reason explains why the work is being handed off.
	Reason string `json:"reason,omitempty"`

	// Deadline is when the work should be completed.
	Deadline *time.Time `json:"deadline,omitempty"`

	// Priority override for the receiving town (optional).
	PriorityOverride *int `json:"priority_override,omitempty"`
}
