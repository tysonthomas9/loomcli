package git

import (
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// verdictEnv reads the environment the actor is resolved from; tests replace it.
var verdictEnv = os.Getenv

// taskAgentEnvMarkers are set only in a task agent's run: by the task driver
// (LOOM_TASK_RUN_ID, LOOM_TASK_ID, LOOM_DRIVER_RUN_ID) or by the supervisor for
// a worker assigned a task (LOOM_ASSIGNED_TASK_ID).
var taskAgentEnvMarkers = []string{"LOOM_TASK_RUN_ID", "LOOM_TASK_ID", "LOOM_DRIVER_RUN_ID", "LOOM_ASSIGNED_TASK_ID"}

// commandActor is whoever runs a verdict or delivery command (D42): a task
// agent, the lead, or a human at a normal shell. marker names the environment
// variable that made it an agent ("" for a human).
type commandActor struct {
	review.Actor
	Marker string
	// Task is the task a task agent is running ("" when unknown).
	Task string
}

// resolveCommandActor names who runs the command. A task agent records as an
// agent under the identity its revisions are authored by, so review refuses
// it approving its own work; any other agent session is the lead. lead names
// the lead working area when the session carries no agent name.
//
// Local mode trusts the environment (D28): a process inside an agent session
// can unset these variables and look human.
func resolveCommandActor(lead string) commandActor {
	for _, marker := range taskAgentEnvMarkers {
		if verdictEnv(marker) == "" {
			continue
		}
		id := firstSet("LOOM_TASK_RUN_WORKER_PROFILE_ID", "LOOM_TASK_RUNNER", "LOOM_AGENT_NAME", "LOOM_TASK_RUN_ID", "LOOM_ASSIGNED_TASK_ID", "LOOM_TASK_ID")
		return commandActor{Actor: review.Actor{Kind: "agent", ID: id}, Marker: marker,
			Task: firstSet("LOOM_TASK_ID", "LOOM_ASSIGNED_TASK_ID")}
	}
	for _, marker := range agentEnvMarkers {
		if verdictEnv(marker) == "" {
			continue
		}
		id := strings.TrimSpace(verdictEnv("LOOM_AGENT_NAME"))
		if id == "" {
			id = lead
		}
		return commandActor{Actor: review.Actor{Kind: "lead", ID: id}, Marker: marker}
	}
	user := strings.TrimSpace(verdictEnv("USER"))
	if user == "" {
		user = "local-user"
	}
	return commandActor{Actor: review.Actor{Kind: "human", ID: user}}
}

func firstSet(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(verdictEnv(name)); value != "" {
			return value
		}
	}
	return ""
}
