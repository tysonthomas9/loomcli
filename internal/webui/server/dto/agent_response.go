package dto

import "time"

// AgentStatusResponse is the typed API response for an agent entity.
// Uses string (not entity types) for Status/State/RoleType to decouple the
// wire format from domain types.
type AgentStatusResponse struct {
	// Core Identification
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`

	// Status & State
	Status string `json:"status,omitempty"`      // "open", "in_progress", "blocked", etc.
	State  string `json:"agent_state,omitempty"` // "idle", "spawning", "running", etc.

	// Agent Identity
	RoleType string `json:"role_type,omitempty"` // "polecat", "crew", "witness", etc.
	Rig      string `json:"rig,omitempty"`

	// Activity Tracking
	LastActivity *time.Time `json:"last_activity,omitempty"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Labels — no omitempty: serializes as [] when empty, not omitted.
	// Mapping function must initialize to []string{} to avoid null.
	Labels []string `json:"labels"`
}
