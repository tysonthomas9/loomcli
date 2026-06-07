package workflows

const BuiltinEpicRunnerWorkflowName = "epic-runner"

type builtinWorkflowSpec struct {
	entrypoint string
	files      map[string]string
}

var builtinWorkflows = map[string]builtinWorkflowSpec{
	BuiltinEpicRunnerWorkflowName: {
		entrypoint: "workflows/" + BuiltinEpicRunnerWorkflowName + ".ts",
		files: map[string]string{
			"workflows/" + BuiltinEpicRunnerWorkflowName + ".ts": builtinEpicRunnerWorkflowSource,
		},
	},
}

const builtinEpicRunnerWorkflowSource = `import { createLoomDriverClient } from '@loom/sdk/flue';

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  console.log("epic-runner-start " + input.epicId);
  const completed = [];

  while (true) {
    const task = await loom.tasks.claimReady({ epicId: input.epicId });
    if (!task) {
      return loom.completed({ summary: "Epic drained: " + completed.join(",") });
    }

    const result = await loom.taskRuns.request({
      taskId: task.id,
      providerProfile: "flue-local",
      supportedProviders: ["flue-local"],
      sandboxPlacement: { provider: "flue-local" },
    });

    if (result.status === "completed") {
      await loom.tasks.complete({
        taskId: task.id,
        taskRunId: result.taskRunId || result.id,
      });
      completed.push(task.id);
    } else {
      await loom.tasks.release(task.id);
      return loom.needsHuman({
        summary: "Task failed: " + task.id,
        taskRunId: result.id,
        logsRef: result.logsRef || "",
        artifactsRef: result.artifactsRef || "",
      });
    }
  }
}
`
