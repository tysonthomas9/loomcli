import { createLoomDriverClient } from '@loom/sdk/driver';

type WorkflowContext = {
  payload?: {
    epicId?: string;
    parentSessionId?: string;
  };
};

type ClaimedTask = {
  id: string;
};

type RequestedTaskRun = {
  status?: string;
  id?: string;
  taskRunId?: string;
  leaseToken?: string;
  logsRef?: string;
  artifactsRef?: string;
  artifactIds?: string[];
};

export async function run(ctx: WorkflowContext) {
  const input = ctx.payload ?? {};
  const epicId = input.epicId ?? '';
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
      runner: 'local-task-runner',
      parentSessionId: input.parentSessionId ?? '',
    })) as RequestedTaskRun;
    if (result.status === 'queued' || result.status === 'running') {
      const awaited = (await loom.taskRuns.await({
        taskRunId: result.taskRunId || result.id || '',
        pollMs: 500,
      })) as RequestedTaskRun;
      Object.assign(result, awaited);
    }

    if (result.status === 'completed') {
      if (result.leaseToken) {
        await loom.tasks.complete({
          taskId: task.id,
          taskRunId: result.taskRunId || result.id || '',
          leaseToken: result.leaseToken,
          logsRef: result.logsRef || '',
          artifactsRef: result.artifactsRef || '',
          artifactIds: result.artifactIds || [],
        });
      }
      completed.push(task.id);
      continue;
    }

    await loom.tasks.release(task.id);
    return loom.needsReview({
      summary: 'Task failed: ' + task.id,
      taskRunId: result.taskRunId || result.id,
      logsRef: result.logsRef ?? '',
      artifactsRef: result.artifactsRef ?? '',
    });
  }
}
