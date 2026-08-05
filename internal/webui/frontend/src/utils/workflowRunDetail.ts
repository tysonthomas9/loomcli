import type { WorkflowRun, WorkflowRunStep } from "@/api/workflows/workflows";

export interface LinkedRunSession {
  taskRunId: string;
  taskId: string;
  sessionId: string;
}

/**
 * Merge a sparse history/SSE row into an already enriched run detail. Scalar
 * lifecycle fields come from the newest row, while detail-only linkage is
 * retained when that row omits (or sends an empty form of) output/steps.
 */
export function mergeWorkflowRun(
  previous: WorkflowRun,
  incoming: WorkflowRun,
): WorkflowRun {
  if (previous.run_id !== incoming.run_id) return incoming;

  const output = mergeOutput(previous.output, incoming.output);
  const steps = mergeSteps(previous.steps, incoming.steps);
  const merged: WorkflowRun = { ...previous, ...incoming };

  if (output) merged.output = output;
  if (steps) merged.steps = steps;
  if (incoming.payload === undefined && previous.payload !== undefined) {
    merged.payload = previous.payload;
  }
  return merged;
}

/** Merge a canonical history page without discarding enrichment already held. */
export function mergeWorkflowRunPage(
  previous: WorkflowRun[],
  incoming: WorkflowRun[],
): WorkflowRun[] {
  const previousById = new Map(previous.map((run) => [run.run_id, run]));
  return incoming.map((run) => {
    const existing = previousById.get(run.run_id);
    return existing ? mergeWorkflowRun(existing, run) : run;
  });
}

/**
 * Resolve every task-run/session edge embedded in a workflow run. Driver-step
 * order is preserved. A run-level output link enriches its matching step (or
 * becomes an additional link when the workflow emitted no matching step).
 */
export function linkedSessionsForRun(run: WorkflowRun): LinkedRunSession[] {
  const output = run.output ?? {};
  const outputTaskRunId = firstString(output.taskRunId, output.task_run_id);
  const outputSessionId = firstString(output.sessionId, output.session_id);
  const outputTaskId = firstString(
    output.issueId,
    output.issue_id,
    output.taskId,
    output.task_id,
  );
  const taskSteps = (run.steps ?? []).filter(
    (step) => firstString(step.task_run_id) !== "",
  );
  const payloadTaskId = taskIdFromPayload(run.payload);
  const links: LinkedRunSession[] = [];

  for (const step of taskSteps) {
    const taskRunId = firstString(step.task_run_id);
    const matchesOutput = taskRunId === outputTaskRunId;
    addUniqueLink(links, {
      taskRunId,
      taskId: firstString(
        step.task_id,
        matchesOutput ? outputTaskId : "",
        taskSteps.length === 1 ? payloadTaskId : "",
      ),
      sessionId: firstString(
        matchesOutput ? outputSessionId : "",
        `flue-${taskRunId}`,
      ),
    });
  }

  if (outputTaskRunId || outputSessionId) {
    addUniqueLink(links, {
      taskRunId: outputTaskRunId,
      taskId: firstString(
        outputTaskId,
        taskSteps.length === 0 ? payloadTaskId : "",
      ),
      sessionId: firstString(
        outputSessionId,
        outputTaskRunId ? `flue-${outputTaskRunId}` : "",
      ),
    });
  }

  return links;
}

/** Resolve only canonical child TaskRun work-item identities for a run. */
export function workedTaskIdsForRun(run: WorkflowRun): string[] {
  const taskIds: string[] = [];
  for (const step of run.steps ?? []) {
    addUniqueString(taskIds, firstString(step.task_id));
  }
  return taskIds;
}

export function linkedRunSessionKey(link: LinkedRunSession): string {
  return firstString(link.taskRunId, link.sessionId, link.taskId);
}

function mergeOutput(
  previous: WorkflowRun["output"],
  incoming: WorkflowRun["output"],
): WorkflowRun["output"] {
  if (!previous) return incoming;
  if (!incoming) return previous;
  return { ...previous, ...incoming };
}

function mergeSteps(
  previous: WorkflowRunStep[] | undefined,
  incoming: WorkflowRunStep[] | undefined,
): WorkflowRunStep[] | undefined {
  if (!previous?.length) return incoming;
  if (!incoming?.length) return previous;

  const incomingById = new Map(incoming.map((step) => [step.id, step]));
  const merged = previous.map((step) => {
    const fresh = incomingById.get(step.id);
    if (!fresh) return step;
    incomingById.delete(step.id);
    return { ...step, ...fresh };
  });
  return [...merged, ...incomingById.values()];
}

function addUniqueLink(
  links: LinkedRunSession[],
  candidate: LinkedRunSession,
): void {
  if (!candidate.taskRunId && !candidate.sessionId) return;
  const duplicate = links.some(
    (link) =>
      (candidate.taskRunId !== "" && link.taskRunId === candidate.taskRunId) ||
      (candidate.sessionId !== "" && link.sessionId === candidate.sessionId),
  );
  if (!duplicate) links.push(candidate);
}

function addUniqueString(values: string[], candidate: string): void {
  if (candidate && !values.includes(candidate)) values.push(candidate);
}

function taskIdFromPayload(payload: unknown): string {
  const root = asRecord(payload);
  if (!root) return "";
  return firstString(
    root.taskId,
    root.task_id,
    root.issueId,
    root.issue_id,
    asRecord(root.event)?.taskId,
    asRecord(root.event)?.task_id,
    asRecord(root.event)?.issueId,
    asRecord(root.event)?.issue_id,
    asRecord(root.input)?.taskId,
    asRecord(root.input)?.task_id,
    asRecord(root.input)?.issueId,
    asRecord(root.input)?.issue_id,
  );
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value == null || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as Record<string, unknown>;
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (trimmed) return trimmed;
    }
  }
  return "";
}
