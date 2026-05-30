package defs

import (
	"fmt"
	"strings"
)

func validateAgentToolReferences(agent AgentModule, tools map[string]ToolModule) error {
	for _, name := range compactStrings(agent.Tools) {
		if _, ok := tools[name]; ok {
			continue
		}
		if externalModelToolReference(name) {
			continue
		}
		if toolNamePattern.MatchString(name) {
			return fmt.Errorf("%s: agent model tool %q must reference a declared typed tool definition", agent.SourcePath, name)
		}
	}
	return nil
}

func validateWorkflowToolReferences(workflow WorkflowModule, tools map[string]ToolModule) error {
	for _, name := range compactStrings(workflow.Tools) {
		if _, ok := tools[name]; ok {
			continue
		}
		if workflowContextToolReference(name) {
			continue
		}
		if toolNamePattern.MatchString(name) {
			return fmt.Errorf("%s: workflow tool %q must reference a declared typed tool definition or WorkflowContext tool", workflow.SourcePath, name)
		}
	}
	return nil
}

func externalModelToolReference(name string) bool {
	name = strings.TrimSpace(name)
	return strings.Contains(name, ".")
}

func workflowContextToolReference(name string) bool {
	switch strings.TrimSpace(name) {
	case "workflow.status",
		"workflow.cancelRequested",
		"workflow.waitUntil",
		"workflow.cancel",
		"workItems.get",
		"workItems.readyChildren",
		"workItems.blockedChildren",
		"workItems.listChildren",
		"workItems.comment",
		"taskRuns.ensure",
		"taskRuns.list",
		"taskRuns.wait",
		"taskClaims.get",
		"taskClaims.list",
		"taskClaims.wait",
		"agents.session",
		"agents.dispatch",
		"artifacts.record",
		"artifacts.create",
		"shell.run",
		"setup.shell.run",
		"files.writeText",
		"files.writeJSON",
		"files.readText",
		"files.readJSON",
		"staging.writeText",
		"staging.writeJSON",
		"staging.readText",
		"staging.readJSON":
		return true
	default:
		return false
	}
}
