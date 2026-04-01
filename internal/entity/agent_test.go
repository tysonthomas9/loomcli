package entity

import (
	"strings"
	"testing"
	"time"
)

func TestRoleType_IsValid(t *testing.T) {
	tests := []struct {
		role RoleType
		want bool
	}{
		{RolePolecat, true},
		{RoleCrew, true},
		{RoleWitness, true},
		{RoleRefinery, true},
		{RoleMayor, true},
		{RoleDeacon, true},
		{"", true},
		{"unknown_role", false},
		{"POLECAT", false},
		{"admin", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.IsValid(); got != tt.want {
				t.Errorf("RoleType(%q).IsValid() = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestRoleType_IsWellKnown(t *testing.T) {
	tests := []struct {
		role RoleType
		want bool
	}{
		{RolePolecat, true},
		{RoleCrew, true},
		{RoleWitness, true},
		{RoleRefinery, true},
		{RoleMayor, true},
		{RoleDeacon, true},
		{"", false},
		{"unknown_role", false},
		{"POLECAT", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.IsWellKnown(); got != tt.want {
				t.Errorf("RoleType(%q).IsWellKnown() = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestAgent_Validate(t *testing.T) {
	now := time.Now()
	validAgent := func() *Agent {
		return &Agent{
			ID:        "agent-001",
			Title:     "Test Agent",
			Status:    StatusOpen,
			State:     StateIdle,
			RoleType:  RolePolecat,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	t.Run("valid agent with all fields passes", func(t *testing.T) {
		a := validAgent()
		a.Description = "A test agent"
		a.Rig = "rig-1"
		a.HookBead = "hook-1"
		a.RoleBead = "role-1"
		a.LastActivity = &now
		a.Labels = []string{"test", "dev"}
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid agent with minimal fields passes", func(t *testing.T) {
		a := &Agent{
			ID:    "agent-002",
			Title: "Minimal Agent",
		}
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty ID fails", func(t *testing.T) {
		a := validAgent()
		a.ID = ""
		err := a.Validate()
		if err == nil {
			t.Error("expected error for empty ID")
		} else if !strings.Contains(err.Error(), "id is required") {
			t.Errorf("error %q should contain %q", err.Error(), "id is required")
		}
	})

	t.Run("empty Title fails", func(t *testing.T) {
		a := validAgent()
		a.Title = ""
		err := a.Validate()
		if err == nil {
			t.Error("expected error for empty title")
		} else if !strings.Contains(err.Error(), "title is required") {
			t.Errorf("error %q should contain %q", err.Error(), "title is required")
		}
	})

	t.Run("title exactly 500 chars passes", func(t *testing.T) {
		a := validAgent()
		a.Title = strings.Repeat("a", 500)
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error for 500-char title: %v", err)
		}
	})

	t.Run("title 501 chars fails", func(t *testing.T) {
		a := validAgent()
		a.Title = strings.Repeat("a", 501)
		err := a.Validate()
		if err == nil {
			t.Error("expected error for title > 500 chars")
		} else if !strings.Contains(err.Error(), "500 characters") {
			t.Errorf("error %q should contain %q", err.Error(), "500 characters")
		}
	})

	t.Run("invalid status fails", func(t *testing.T) {
		a := validAgent()
		a.Status = "bogus"
		err := a.Validate()
		if err == nil {
			t.Error("expected error for invalid status")
		} else if !strings.Contains(err.Error(), "invalid status") {
			t.Errorf("error %q should contain %q", err.Error(), "invalid status")
		}
	})

	t.Run("valid status passes", func(t *testing.T) {
		a := validAgent()
		a.Status = StatusOpen
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty status passes", func(t *testing.T) {
		a := validAgent()
		a.Status = ""
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error for empty status: %v", err)
		}
	})

	t.Run("invalid state fails", func(t *testing.T) {
		a := validAgent()
		a.State = "bogus"
		err := a.Validate()
		if err == nil {
			t.Error("expected error for invalid state")
		} else if !strings.Contains(err.Error(), "invalid agent state") {
			t.Errorf("error %q should contain %q", err.Error(), "invalid agent state")
		}
	})

	t.Run("valid state passes", func(t *testing.T) {
		a := validAgent()
		a.State = StateRunning
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty state passes", func(t *testing.T) {
		a := validAgent()
		a.State = ""
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error for empty state: %v", err)
		}
	})

	t.Run("invalid role type fails", func(t *testing.T) {
		a := validAgent()
		a.RoleType = "bogus"
		err := a.Validate()
		if err == nil {
			t.Error("expected error for invalid role type")
		} else if !strings.Contains(err.Error(), "invalid role type") {
			t.Errorf("error %q should contain %q", err.Error(), "invalid role type")
		}
	})

	t.Run("valid role type passes", func(t *testing.T) {
		a := validAgent()
		a.RoleType = RolePolecat
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty role type passes", func(t *testing.T) {
		a := validAgent()
		a.RoleType = ""
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error for empty role type: %v", err)
		}
	})

	t.Run("nil LastActivity passes", func(t *testing.T) {
		a := validAgent()
		a.LastActivity = nil
		if err := a.Validate(); err != nil {
			t.Errorf("unexpected error for nil LastActivity: %v", err)
		}
	})
}

func TestAgent_SetDefaults(t *testing.T) {
	t.Run("empty fields get defaults", func(t *testing.T) {
		a := &Agent{}
		a.SetDefaults()
		if a.Status != StatusOpen {
			t.Errorf("Status = %q, want %q", a.Status, StatusOpen)
		}
		if a.State != StateIdle {
			t.Errorf("State = %q, want %q", a.State, StateIdle)
		}
	})

	t.Run("existing Status preserved", func(t *testing.T) {
		a := &Agent{
			Status: StatusClosed,
		}
		a.SetDefaults()
		if a.Status != StatusClosed {
			t.Errorf("Status = %q, want %q", a.Status, StatusClosed)
		}
	})

	t.Run("existing State preserved", func(t *testing.T) {
		a := &Agent{
			State: StateRunning,
		}
		a.SetDefaults()
		if a.State != StateRunning {
			t.Errorf("State = %q, want %q", a.State, StateRunning)
		}
	})
}

func TestAgent_IsAlive(t *testing.T) {
	tests := []struct {
		name  string
		state AgentState
		want  bool
	}{
		{"idle is alive", StateIdle, true},
		{"spawning is alive", StateSpawning, true},
		{"running is alive", StateRunning, true},
		{"working is alive", StateWorking, true},
		{"stuck is not alive", StateStuck, false},
		{"done is not alive", StateDone, false},
		{"stopped is not alive", StateStopped, false},
		{"dead is not alive", StateDead, false},
		{"empty is not alive", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{State: tt.state}
			if got := a.IsAlive(); got != tt.want {
				t.Errorf("Agent{State: %q}.IsAlive() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestAgent_IsDead(t *testing.T) {
	tests := []struct {
		name  string
		state AgentState
		want  bool
	}{
		{"dead is dead", StateDead, true},
		{"running is not dead", StateRunning, false},
		{"empty is not dead", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{State: tt.state}
			if got := a.IsDead(); got != tt.want {
				t.Errorf("Agent{State: %q}.IsDead() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestAgent_NeedsAttention(t *testing.T) {
	tests := []struct {
		name  string
		state AgentState
		want  bool
	}{
		{"stuck needs attention", StateStuck, true},
		{"dead needs attention", StateDead, true},
		{"running does not need attention", StateRunning, false},
		{"idle does not need attention", StateIdle, false},
		{"empty does not need attention", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{State: tt.state}
			if got := a.NeedsAttention(); got != tt.want {
				t.Errorf("Agent{State: %q}.NeedsAttention() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestAgent_IsActive(t *testing.T) {
	tests := []struct {
		name  string
		state AgentState
		want  bool
	}{
		{"running is active", StateRunning, true},
		{"working is active", StateWorking, true},
		{"idle is not active", StateIdle, false},
		{"spawning is not active", StateSpawning, false},
		{"dead is not active", StateDead, false},
		{"empty is not active", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{State: tt.state}
			if got := a.IsActive(); got != tt.want {
				t.Errorf("Agent{State: %q}.IsActive() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}
