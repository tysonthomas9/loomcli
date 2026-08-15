// Package scriptedroles is the compile-time catalog that binds role names to
// scripted workflow machinery. It is deliberately pure: catalog data depends
// only on domain vocabulary, never stores, workflows, drivers, or WebUI code.
package scriptedroles

import (
	_ "embed"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	ScoutRoleName      = "scout"
	EpicRunnerRoleName = "epic-runner"

	ScoutWorkflowName      = "scout"
	EpicRunnerWorkflowName = "epic-runner"

	ScoutTaskRunnerEntrypoint   = "scout-task-runner"
	LocalTaskRunnerEntrypoint   = "local-task-runner"
	DaytonaTaskRunnerEntrypoint = "daytona-task-runner"
)

// PreflightPolicy says when starting a scripted role requires the local CLI
// backend readiness check.
type PreflightPolicy string

const (
	Always        PreflightPolicy = "always"
	PayloadRunner PreflightPolicy = "payload_runner"
	None          PreflightPolicy = "none"
)

// RoleSeed is the create-only durable role definition. Prompt is published as
// a workspace PromptFile by agentprovision before the role record is created.
type RoleSeed struct {
	Kind        domain.RoleKind
	Description string
	Prompt      string
}

// TriggerBindingTemplate is the compiled default binding for a standing
// scripted-role instance.
type TriggerBindingTemplate struct {
	BindingID         string
	Name              string
	SourceKind        string
	RouteKey          string
	TargetEntrypoint  string
	ConcurrencyPolicy domain.TriggerBindingConcurrencyPolicy
	ExcludedActors    []string
	Schedule          string
	ScheduleTimezone  string
	Enabled           bool
}

// InstanceTemplate is the compiled default standing instance for a scripted
// role. A nil ScriptedRole.DefaultInstance means instances are not seeded.
type InstanceTemplate struct {
	ServiceID    string
	Name         string
	TriggerKind  domain.AgentServiceTriggerKind
	DesiredState domain.AgentServiceDesiredState
	CreatedBy    string
	Binding      TriggerBindingTemplate
}

// ScriptedRole binds a durable role name to the workflow machinery that role
// runs. Role content remains ordinary editable data; this binding does not.
type ScriptedRole struct {
	RoleName            string
	DisplayName         string
	WorkflowName        string
	LeafRunners         []string
	TrustedLocalCLI     bool
	Preflight           PreflightPolicy
	JournalFilename     string
	AllowedBindingKinds []string
	DefaultRole         RoleSeed
	DefaultInstance     *InstanceTemplate
}

//go:embed prompts/scout.md
var scoutPrompt string

var catalog = map[string]ScriptedRole{
	ScoutRoleName: {
		RoleName:            ScoutRoleName,
		DisplayName:         "Scout",
		WorkflowName:        ScoutWorkflowName,
		LeafRunners:         []string{ScoutTaskRunnerEntrypoint},
		TrustedLocalCLI:     true,
		Preflight:           Always,
		JournalFilename:     "history.md",
		AllowedBindingKinds: []string{"cron"},
		DefaultRole: RoleSeed{
			Kind:   domain.RoleKindWorker,
			Prompt: scoutPrompt,
		},
		DefaultInstance: &InstanceTemplate{
			ServiceID:    "scout",
			Name:         "Scout",
			TriggerKind:  domain.AgentServiceTriggerKindCron,
			DesiredState: domain.AgentServiceDesiredRunning,
			CreatedBy:    "system",
			Binding: TriggerBindingTemplate{
				BindingID:         "binding-cron-scout-weekly",
				Name:              "Scout weekly",
				SourceKind:        "cron",
				RouteKey:          "cron.scout.weekly",
				TargetEntrypoint:  "run",
				ConcurrencyPolicy: domain.TriggerBindingConcurrencyForbid,
				ExcludedActors:    []string{"driver-run", "task-run"},
				Schedule:          "@weekly",
				Enabled:           true,
			},
		},
	},
	EpicRunnerRoleName: {
		RoleName:            EpicRunnerRoleName,
		DisplayName:         "Epic runner",
		WorkflowName:        EpicRunnerWorkflowName,
		LeafRunners:         []string{LocalTaskRunnerEntrypoint, DaytonaTaskRunnerEntrypoint},
		TrustedLocalCLI:     true,
		Preflight:           PayloadRunner,
		AllowedBindingKinds: []string{"event"},
		DefaultRole: RoleSeed{
			Kind: domain.RoleKindWorker,
		},
	},
}

// ForRole resolves compiled machinery by the durable role discriminator.
func ForRole(name string) (ScriptedRole, bool) {
	role, ok := catalog[strings.TrimSpace(name)]
	if !ok {
		return ScriptedRole{}, false
	}
	return clone(role), true
}

// All returns a stable, detached snapshot of the compiled catalog.
func All() []ScriptedRole {
	roles := make([]ScriptedRole, 0, len(catalog))
	for _, role := range catalog {
		roles = append(roles, clone(role))
	}
	sort.Slice(roles, func(i, j int) bool {
		return roles[i].RoleName < roles[j].RoleName
	})
	return roles
}

// ForWorkflow is the reverse lookup used when a workflow run is submitted by
// workflow name before an AgentService instance has been resolved.
func ForWorkflow(name string) (ScriptedRole, bool) {
	name = strings.TrimSpace(name)
	for _, role := range catalog {
		if role.WorkflowName == name {
			return clone(role), true
		}
	}
	return ScriptedRole{}, false
}

// NeedsPreflight applies a catalog role's preflight policy. PayloadRunner uses
// the first LeafRunners entry as the payload's absent/default runner; later
// entries are explicit non-local alternatives.
func NeedsPreflight(role ScriptedRole, payloadRunner string) bool {
	switch role.Preflight {
	case Always:
		return true
	case PayloadRunner:
		if len(role.LeafRunners) == 0 {
			return false
		}
		return strings.TrimSpace(payloadRunner) == "" || strings.TrimSpace(payloadRunner) == role.LeafRunners[0]
	default:
		return false
	}
}

// IsTrustedLocalCLIRunner reports whether entrypoint is the host-local CLI
// leaf of a catalog row that opts into trusted-local credentials. For a
// PayloadRunner role, only its default/first leaf is local; explicit alternate
// leaves such as Daytona retain the strict remote-runner environment.
func IsTrustedLocalCLIRunner(entrypoint string) bool {
	entrypoint = strings.TrimSpace(entrypoint)
	for _, role := range catalog {
		if !role.TrustedLocalCLI {
			continue
		}
		leaves := role.LeafRunners
		if role.Preflight == PayloadRunner && len(leaves) > 1 {
			leaves = leaves[:1]
		}
		for _, leaf := range leaves {
			if leaf == entrypoint {
				return true
			}
		}
	}
	return false
}

func clone(role ScriptedRole) ScriptedRole {
	role.LeafRunners = append([]string(nil), role.LeafRunners...)
	role.AllowedBindingKinds = append([]string(nil), role.AllowedBindingKinds...)
	if role.DefaultInstance != nil {
		instance := *role.DefaultInstance
		instance.Binding.ExcludedActors = append([]string(nil), role.DefaultInstance.Binding.ExcludedActors...)
		role.DefaultInstance = &instance
	}
	return role
}
