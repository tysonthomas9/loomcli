package defs

import "fmt"

func Summary(plan *Plan) string {
	if plan == nil {
		return "No definition plan loaded"
	}
	summary := fmt.Sprintf("agents=%d workflows=%d runtimes=%d", len(plan.Agents), len(plan.Workflows), len(plan.Runtimes))
	if len(plan.AgentInstances) > 0 {
		summary += fmt.Sprintf(" agent_instances=%d", len(plan.AgentInstances))
	}
	if len(plan.Nodes) > 0 {
		summary += fmt.Sprintf(" nodes=%d", len(plan.Nodes))
	}
	if len(plan.AgentSessions) > 0 {
		summary += fmt.Sprintf(" agent_sessions=%d", len(plan.AgentSessions))
	}
	if len(plan.AgentSessionOperations) > 0 {
		summary += fmt.Sprintf(" agent_session_operations=%d", len(plan.AgentSessionOperations))
	}
	if len(plan.AgentSessionToolCalls) > 0 {
		summary += fmt.Sprintf(" agent_session_tool_calls=%d", len(plan.AgentSessionToolCalls))
	}
	if len(plan.AgentLeases) > 0 {
		summary += fmt.Sprintf(" agent_leases=%d", len(plan.AgentLeases))
	}
	if len(plan.AgentOwnershipLeases) > 0 {
		summary += fmt.Sprintf(" agent_ownership_leases=%d", len(plan.AgentOwnershipLeases))
	}
	if len(plan.AgentCommands) > 0 {
		summary += fmt.Sprintf(" agent_commands=%d", len(plan.AgentCommands))
	}
	if len(plan.TerminalSessions) > 0 {
		summary += fmt.Sprintf(" terminal_sessions=%d", len(plan.TerminalSessions))
	}
	if len(plan.WorkflowRuns) > 0 {
		summary += fmt.Sprintf(" workflow_runs=%d", len(plan.WorkflowRuns))
	}
	if len(plan.TaskRuns) > 0 {
		summary += fmt.Sprintf(" task_runs=%d", len(plan.TaskRuns))
	}
	if len(plan.RunEvents) > 0 {
		summary += fmt.Sprintf(" run_events=%d", len(plan.RunEvents))
	}
	if len(plan.Artifacts) > 0 {
		summary += fmt.Sprintf(" artifacts=%d", len(plan.Artifacts))
	}
	if len(plan.Skills) > 0 {
		summary += fmt.Sprintf(" skills=%d", len(plan.Skills))
	}
	if len(plan.Tools) > 0 {
		summary += fmt.Sprintf(" tools=%d", len(plan.Tools))
	}
	return summary
}
