import { createLoomDriverClient } from '@loom/sdk/flue';

type WorkflowContext = {
  payload?: {
    epicId?: string;
    epic_id?: string;
  };
};

type ClaimedTask = {
  id: string;
};

type RequestedTaskRun = {
  status?: string;
  id?: string;
  logsRef?: string;
  logs_ref?: string;
  artifactsRef?: string;
  artifacts_ref?: string;
};

export async function run(ctx: WorkflowContext) {
  const input = ctx.payload ?? {};
  const epicId = input.epicId ?? input.epic_id ?? '';
  const loom = createLoomDriverClient({ input });

  console.log('native-flue-driver-start ' + epicId);

  const completed: string[] = [];
  while (true) {
    const task = (await loom.tasks.claimReady({ epicId })) as ClaimedTask | null;
    if (!task) {
      return loom.completed({ summary: 'Epic drained: ' + completed.join(',') });
    }

    const result = (await loom.taskRuns.request({
      taskId: task.id,
      providerProfile: 'flue-local',
      supportedProviders: ['flue-local'],
      sandboxPlacement: { provider: 'flue-local' },
    })) as RequestedTaskRun;

    if (result.status === 'completed') {
      await loom.tasks.complete(task.id);
      completed.push(task.id);
      continue;
    }

    await loom.tasks.release(task.id);
    return loom.needsHuman({
      summary: 'Task failed: ' + task.id,
      taskRunId: result.id,
      logsRef: result.logsRef ?? result.logs_ref ?? '',
      artifactsRef: result.artifactsRef ?? result.artifacts_ref ?? '',
    });
  }
}
