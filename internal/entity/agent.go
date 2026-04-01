package entity

import (
	"fmt"
	"time"
)

// RoleType categorizes the role of an agent in the system.
type RoleType string

// Role type constants.
const (
	RolePolecat  RoleType = "polecat"
	RoleCrew     RoleType = "crew"
	RoleWitness  RoleType = "witness"
	RoleRefinery RoleType = "refinery"
	RoleMayor    RoleType = "mayor"
	RoleDeacon   RoleType = "deacon"
)

// IsValid checks if the role type value is valid.
// Empty string is valid (unspecified role).
func (r RoleType) IsValid() bool {
	switch r {
	case RolePolecat, RoleCrew, RoleWitness, RoleRefinery, RoleMayor, RoleDeacon, "":
		return true
	}
	return false
}

// IsWellKnown returns true only for known role type constants (excludes empty string).
func (r RoleType) IsWellKnown() bool {
	switch r {
	case RolePolecat, RoleCrew, RoleWitness, RoleRefinery, RoleMayor, RoleDeacon:
		return true
	}
	return false
}

// Agent represents an autonomous agent as a first-class domain entity.
// Fields are organized into logical groups for maintainability.
type Agent struct {
	// ===== Core Identification =====
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`

	// ===== Status & State =====
	Status IssueStatus `json:"status,omitempty"`
	State  AgentState  `json:"agent_state,omitempty"`

	// ===== Agent Identity =====
	RoleType RoleType `json:"role_type,omitempty"`
	Rig      string   `json:"rig,omitempty"`

	// ===== Slot Fields =====
	HookBead string `json:"hook_bead,omitempty"`
	RoleBead string `json:"role_bead,omitempty"`

	// ===== Activity Tracking =====
	LastActivity *time.Time `json:"last_activity,omitempty"`

	// ===== Timestamps =====
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// ===== Relational Data =====
	Labels []string `json:"labels,omitempty"`
}

// Validate checks if the agent has valid field values.
func (a *Agent) Validate() error {
	if a.ID == "" {
		return fmt.Errorf("id is required")
	}
	if a.Title == "" {
		return fmt.Errorf("title is required")
	}
	if len(a.Title) > 500 {
		return fmt.Errorf("title must be 500 characters or less (got %d)", len(a.Title))
	}
	if !a.Status.IsValid() {
		return fmt.Errorf("invalid status: %s", a.Status)
	}
	if !a.State.IsValid() {
		return fmt.Errorf("invalid agent state: %s", a.State)
	}
	if !a.RoleType.IsValid() {
		return fmt.Errorf("invalid role type: %s", a.RoleType)
	}
	return nil
}

// SetDefaults applies default values for unset fields.
func (a *Agent) SetDefaults() {
	if a.Status == "" {
		a.Status = StatusOpen
	}
	if a.State == "" {
		a.State = StateIdle
	}
}

// IsAlive returns true if the agent state is idle, spawning, running, or working.
func (a *Agent) IsAlive() bool {
	switch a.State {
	case StateIdle, StateSpawning, StateRunning, StateWorking:
		return true
	}
	return false
}

// IsDead returns true if the agent state is dead.
func (a *Agent) IsDead() bool {
	return a.State == StateDead
}

// NeedsAttention returns true if the agent state is stuck or dead.
func (a *Agent) NeedsAttention() bool {
	return a.State == StateStuck || a.State == StateDead
}

// IsActive returns true if the agent is actively doing work (running or working).
func (a *Agent) IsActive() bool {
	return a.State == StateRunning || a.State == StateWorking
}
