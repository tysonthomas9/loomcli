package backends

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Planner capability names. Kept small and explicit so admission diagnostics
// name the missing operation rather than a vague "misconfigured".
const (
	CapTaskReadAssigned    = "task.read.assigned"
	CapTaskMonitorRegister = "task.monitor.register"
	CapTaskDesignSubmit    = "task.design.submit"
	CapTaskStatusReview    = "task.status.review"
	CapRepoWrite           = "repo.write"
)

// EnforcementLevel names how read_only is applied on the resolved backend.
// Identical strings are used in daemon logs, doctor, and agent status JSON.
type EnforcementLevel string

const (
	EnforcementNone       EnforcementLevel = ""
	EnforcementHardTools  EnforcementLevel = "hard_tools"
	EnforcementOSSandbox  EnforcementLevel = "os_sandbox"
	EnforcementPromptOnly EnforcementLevel = "prompt_only"
)

// PlannerCapInput is everything the resolver needs to decide how a plan
// worker can read its assignment and submit a design without repo writes.
type PlannerCapInput struct {
	Backend           string
	ReadOnly          bool
	RoleName          string
	TaskFilter        string
	Hooks             *domain.AgentHooks
	AssignedTaskIDSet bool   // supervisor will inject LOOM_ASSIGNED_TASK_ID
	ServerURL         string // LOOM_SERVER_URL (or equivalent HTTP issue backend)
}

// CapSatisfaction records whether a required planner cap is available and how.
type CapSatisfaction struct {
	Cap         string
	Satisfied   bool
	Via         string // "host", "agent_shell", "denied", or ""
	Remediation string
}

// PlannerCapResult is the resolved capability set for one spawn attempt.
type PlannerCapResult struct {
	IsPlanner         bool
	Enforcement       EnforcementLevel
	EnforcementDetail string
	Caps              []CapSatisfaction
	Missing           []string
	AdmitError        error // non-nil ⇒ refuse spawn (fail closed)
}

// IsPlannerWorker reports whether this role/filter pair is a plan worker that
// must satisfy the planner capability set before paid execution.
func IsPlannerWorker(roleName, taskFilter string) bool {
	if roleName == "plan" {
		return true
	}
	return taskFilter == "needs_plan"
}

// EffectiveEnforcement returns how read_only will be applied on backend.
// Empty when read_only is off.
func EffectiveEnforcement(backend string, readOnly bool) EnforcementLevel {
	if !readOnly {
		return EnforcementNone
	}
	switch backend {
	case "claude":
		return EnforcementHardTools
	case "codex":
		return EnforcementOSSandbox
	case "gemini":
		// Gemini's --approval-mode=plan is a hard CLI mode, not an OS sandbox.
		return EnforcementHardTools
	default:
		return EnforcementPromptOnly
	}
}

// EnforcementDetailText is the operator-facing sentence for a level. Same
// wording must appear in daemon log, doctor, and status JSON.
func EnforcementDetailText(backend string, level EnforcementLevel) string {
	switch level {
	case EnforcementHardTools:
		if backend == "claude" {
			return fmt.Sprintf("enforcement=%s: backend %q denies Write/Edit/NotebookEdit/Bash via --disallowedTools", level, backend)
		}
		return fmt.Sprintf("enforcement=%s: backend %q applies a hard CLI read-only mode", level, backend)
	case EnforcementOSSandbox:
		return fmt.Sprintf("enforcement=%s: backend %q applies an OS-level read-only sandbox (--sandbox read-only)", level, backend)
	case EnforcementPromptOnly:
		return fmt.Sprintf("enforcement=%s: backend %q has no hard read-only mechanism — prompt preamble only (SOFT ENFORCEMENT ONLY); the agent CAN still write the repo", level, backend)
	default:
		return ""
	}
}

// HasHostDesignSubmit reports whether hooks include write_design from final_reply.
func HasHostDesignSubmit(hooks *domain.AgentHooks) bool {
	if hooks.IsEmpty() {
		return false
	}
	for _, a := range hooks.OnComplete {
		if a.Type == domain.AgentHookActionWriteDesign &&
			a.Source == domain.AgentHookCommentSourceFinalReply {
			return true
		}
	}
	return false
}

// HasHostStatusReview reports whether hooks include set_status review.
func HasHostStatusReview(hooks *domain.AgentHooks) bool {
	if hooks.IsEmpty() {
		return false
	}
	for _, a := range hooks.OnComplete {
		if a.Type == domain.AgentHookActionSetStatus &&
			strings.EqualFold(strings.TrimSpace(a.Value), "review") {
			return true
		}
	}
	return false
}

// DefaultPlanHostHooks is the host-owned submit pipeline seeded for new plan
// agents: write the final reply as design, then move the task to review.
// Assignee is deliberately omitted from set_status (claim-lock release needs
// current.Assignee intact — LOOM-1).
func DefaultPlanHostHooks() *domain.AgentHooks {
	return &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionSetStatus, Value: "review"},
	}}
}

// PlanHostSubmitRemediation is the exact agentdef command operators should run
// when a legacy plan agent is missing host-submit hooks.
func PlanHostSubmitRemediation(agentName string) string {
	if agentName == "" {
		agentName = "<planner>"
	}
	return fmt.Sprintf(
		"configure host submission: loom agentdef update %s --on-complete-write-design --on-complete-set-status review",
		agentName)
}

// AgentShellCanMutateTaskMeta reports whether the agent process can run
// `loom data` mutations under the effective backend enforcement. Hard Bash
// denial and OS sandboxes that block embedded FleetDB lock writes both fail
// here unless an HTTP serve URL is available (soft backends always can).
func AgentShellCanMutateTaskMeta(backend string, readOnly bool, serverURL string) bool {
	if !readOnly {
		return true
	}
	level := EffectiveEnforcement(backend, true)
	switch level {
	case EnforcementPromptOnly:
		return true
	case EnforcementHardTools:
		// Claude denies Bash under read_only; gemini plan mode has no shell
		// tool path for loom data either.
		return false
	case EnforcementOSSandbox:
		// Codex read-only sandbox blocks embedded.lock writes; HTTP serve
		// avoids the local lock write.
		return strings.TrimSpace(serverURL) != ""
	default:
		return true
	}
}

// AgentShellCanReadTaskMeta is the read half of shell Loom CLI access. Same
// constraints as mutate for hard backends (no Bash / no lock write).
func AgentShellCanReadTaskMeta(backend string, readOnly bool, serverURL string) bool {
	return AgentShellCanMutateTaskMeta(backend, readOnly, serverURL)
}

// ResolvePlannerCaps maps role+backend+hooks into satisfied/missing caps and
// an admit error when a plan worker cannot submit a design without repo write
// escalation. Non-planner roles return a zero result with no admit error.
func ResolvePlannerCaps(in PlannerCapInput) PlannerCapResult {
	out := PlannerCapResult{
		IsPlanner:   IsPlannerWorker(in.RoleName, in.TaskFilter),
		Enforcement: EffectiveEnforcement(in.Backend, in.ReadOnly),
	}
	if out.Enforcement != EnforcementNone {
		out.EnforcementDetail = EnforcementDetailText(in.Backend, out.Enforcement)
	}
	if !out.IsPlanner {
		return out
	}

	hostDesign := HasHostDesignSubmit(in.Hooks)
	hostReview := HasHostStatusReview(in.Hooks)
	shellMutate := AgentShellCanMutateTaskMeta(in.Backend, in.ReadOnly, in.ServerURL)
	shellRead := AgentShellCanReadTaskMeta(in.Backend, in.ReadOnly, in.ServerURL)

	// Host always owns assignment context + monitor registration for
	// supervised plan workers (TaskDetail injection + lock persistence).
	// Agent shell is a secondary path when available.
	out.Caps = []CapSatisfaction{
		satisfyCap(CapTaskReadAssigned, in.AssignedTaskIDSet || shellRead,
			hostOrShell(in.AssignedTaskIDSet, shellRead),
			"host must inject TaskDetail (assign a task) or provide a working loom data show path"),
		satisfyCap(CapTaskMonitorRegister, true, "host",
			"supervisor persists assigned task on the worktree lock"),
		satisfyCap(CapTaskDesignSubmit, hostDesign || shellMutate,
			hostOrShell(hostDesign, shellMutate),
			PlanHostSubmitRemediation("")),
		satisfyCap(CapTaskStatusReview, hostReview || shellMutate,
			hostOrShell(hostReview, shellMutate),
			PlanHostSubmitRemediation("")),
		{
			Cap:       CapRepoWrite,
			Satisfied: false,
			Via:       "denied",
			Remediation: "repo.write stays denied under plan read_only — do not grant Bash, " +
				"disable the sandbox, or treat soft Cursor enforcement as hard isolation",
		},
	}

	for _, c := range out.Caps {
		if c.Cap == CapRepoWrite {
			continue // denied is intentional
		}
		if !c.Satisfied {
			out.Missing = append(out.Missing, c.Cap)
		}
	}
	if len(out.Missing) > 0 {
		out.AdmitError = fmt.Errorf("%s", FormatPlannerAdmitError(in, out))
	}
	return out
}

func hostOrShell(host, shell bool) string {
	switch {
	case host:
		return "host"
	case shell:
		return "agent_shell"
	default:
		return ""
	}
}

func satisfyCap(cap string, ok bool, via, remediation string) CapSatisfaction {
	return CapSatisfaction{Cap: cap, Satisfied: ok, Via: via, Remediation: remediation}
}

// FormatPlannerAdmitError builds the SpawnFailure / doctor / UI message.
func FormatPlannerAdmitError(in PlannerCapInput, result PlannerCapResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "planner capability preflight failed for role %q on backend %q",
		in.RoleName, in.Backend)
	if result.EnforcementDetail != "" {
		fmt.Fprintf(&b, " (%s)", result.EnforcementDetail)
	}
	fmt.Fprintf(&b, ": missing %s", strings.Join(result.Missing, ", "))
	rems := uniqueRemediations(result)
	if len(rems) > 0 {
		fmt.Fprintf(&b, " — %s", strings.Join(rems, "; "))
	}
	b.WriteString("; refusing spawn before paid execution (fail-closed). " +
		"Do not disable the sandbox or enable Bash to unblock this.")
	return b.String()
}

func uniqueRemediations(result PlannerCapResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range result.Caps {
		if c.Satisfied || c.Cap == CapRepoWrite || c.Remediation == "" {
			continue
		}
		if seen[c.Remediation] {
			continue
		}
		seen[c.Remediation] = true
		out = append(out, c.Remediation)
	}
	return out
}
