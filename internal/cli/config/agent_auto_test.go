package config

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// A nil Auto is "unset", which must stay supervised: every pre-auto config and
// every agent created before `--auto` defaulted to true omits the key.
func TestAgentEntryAutoEnabled(t *testing.T) {
	tests := []struct {
		name  string
		entry AgentEntry
		want  bool
	}{
		{name: "unset is enabled", entry: AgentEntry{Worktree: "worker", Role: "task"}, want: true},
		{name: "explicit true is enabled", entry: AgentEntry{Worktree: "worker", Role: "task", Auto: BoolPtr(true)}, want: true},
		{name: "explicit false is disabled", entry: AgentEntry{Worktree: "worker", Role: "task", Auto: BoolPtr(false)}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.AutoEnabled(); got != tt.want {
				t.Fatalf("AutoEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentEntryShouldSuperviseHonorsAuto(t *testing.T) {
	interactive := map[string]RoleConfig{"operator": {Kind: string(domain.RoleKindInteractive)}}
	worker := map[string]RoleConfig{"operator": {Kind: string(domain.RoleKindWorker)}}
	tests := []struct {
		name  string
		entry AgentEntry
		roles map[string]RoleConfig
		want  bool
	}{
		{name: "unset auto is supervised", entry: AgentEntry{Worktree: "w", Role: "task"}, want: true},
		{name: "auto true is supervised", entry: AgentEntry{Worktree: "w", Role: "task", Auto: BoolPtr(true)}, want: true},
		{name: "auto false is not supervised", entry: AgentEntry{Worktree: "w", Role: "task", Auto: BoolPtr(false)}, want: false},
		{
			name:  "auto false beats a running desired state",
			entry: AgentEntry{Worktree: "w", Role: "task", Auto: BoolPtr(false), DesiredState: domain.AgentDesiredRunning},
			want:  false,
		},
		{
			name:  "auto false with an interactive role stays unsupervised",
			entry: AgentEntry{Worktree: "operator", Role: "operator", Auto: BoolPtr(false)},
			roles: interactive,
			want:  false,
		},
		{
			name:  "auto false with a worker role kind is not supervised",
			entry: AgentEntry{Worktree: "operator", Role: "operator", Auto: BoolPtr(false), DesiredState: domain.AgentDesiredRunning},
			roles: worker,
			want:  false,
		},
		{
			name:  "auto true with a worker role kind is supervised",
			entry: AgentEntry{Worktree: "operator", Role: "operator", Auto: BoolPtr(true), DesiredState: domain.AgentDesiredRunning},
			roles: worker,
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.ShouldSuperviseWithRoles(tt.roles); got != tt.want {
				t.Fatalf("ShouldSuperviseWithRoles() = %v, want %v", got, tt.want)
			}
			if tt.roles == nil {
				if got := tt.entry.ShouldSupervise(); got != tt.want {
					t.Fatalf("ShouldSupervise() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// nil and &true are the same policy, so the reconciler must not see a config
// change when a YAML entry that omits auto is compared with a fleet-db row.
func TestAgentEntryEqualCollapsesUnsetAuto(t *testing.T) {
	unset := AgentEntry{Worktree: "w", Role: "task"}
	enabled := AgentEntry{Worktree: "w", Role: "task", Auto: BoolPtr(true)}
	disabled := AgentEntry{Worktree: "w", Role: "task", Auto: BoolPtr(false)}

	if !unset.Equal(enabled) {
		t.Error("expected unset auto to equal an explicit true")
	}
	if enabled.Equal(disabled) {
		t.Error("expected an explicit true not to equal an explicit false")
	}
	if unset.Equal(disabled) {
		t.Error("expected unset auto not to equal an explicit false")
	}
}

func TestAgentEntryAutoYAMLRoundTrip(t *testing.T) {
	var omitted AgentEntry
	if err := yaml.Unmarshal([]byte("worktree: w\nrole: task\n"), &omitted); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if omitted.Auto != nil {
		t.Fatalf("expected a spec without auto to leave Auto nil, got %v", *omitted.Auto)
	}
	if !omitted.AutoEnabled() {
		t.Error("expected a spec without auto to stay enabled")
	}

	var disabled AgentEntry
	if err := yaml.Unmarshal([]byte("worktree: w\nrole: task\nauto: false\n"), &disabled); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if disabled.Auto == nil || *disabled.Auto {
		t.Fatalf("expected auto: false to decode to an explicit false, got %v", disabled.Auto)
	}
	if disabled.AutoEnabled() {
		t.Error("expected auto: false to disable the agent")
	}

	out, err := yaml.Marshal(disabled)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again AgentEntry
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if again.AutoEnabled() {
		t.Errorf("expected auto: false to survive a round trip, got %q", out)
	}
}

// The fleet-db path must never manufacture a nil: a row is always explicit, and
// a nil here would silently re-enable an agent the owner disabled.
func TestAgentEntryFromDomainMakesAutoExplicit(t *testing.T) {
	for _, auto := range []bool{true, false} {
		entry := agentEntryFromDomain(&domain.Agent{Name: "w", RoleName: "task", Auto: auto})
		if entry.Auto == nil {
			t.Fatalf("auto=%v: expected an explicit Auto, got nil", auto)
		}
		if *entry.Auto != auto {
			t.Errorf("auto=%v: got %v", auto, *entry.Auto)
		}
		if entry.AutoEnabled() != auto {
			t.Errorf("auto=%v: AutoEnabled() = %v", auto, entry.AutoEnabled())
		}
	}
}
