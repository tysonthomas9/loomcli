/**
 * Idempotent TaskRun starts (the option-(b) seam).
 *
 * A TaskRun is the control-plane record that a workflow asked for a
 * task to be executed. The referenced Issue stays in fleet-db's normal
 * ready queue, and Loom's existing issue-claiming agent supervisor
 * picks it up untouched — the TaskRun audits the request and links it
 * to the DriverRun that made it.
 *
 * Idempotency: fleet-db's TaskRun create has no idempotency_key, but a
 * duplicate task_run_id is rejected with 409. Using the deterministic
 * id `tr-<task_id>` makes one TaskRun per task, no matter how many
 * crashed-and-replayed wakes ask for it.
 */

import { FleetClient, FleetError, type TaskRun } from './fleet.js';
import type { WakeContext } from './context.js';

export interface StartTaskResult {
	taskRun: TaskRun;
	/** False when the TaskRun already existed (idempotent replay). */
	created: boolean;
}

/** Deterministic TaskRun id for a task. */
export function taskRunId(taskId: string): string {
	return `tr-${taskId}`;
}

/** Starts (or confirms) the TaskRun for a ready child task. */
export async function startTask(client: FleetClient, ctx: WakeContext, taskId: string): Promise<StartTaskResult> {
	const id = taskRunId(taskId);
	try {
		const taskRun = await client.request<TaskRun>('POST', 'task-runs', {
			task_run_id: id,
			driver_run_id: ctx.runId,
			task_id: taskId,
		});
		return { taskRun, created: true };
	} catch (err) {
		if (err instanceof FleetError && err.status === 409) {
			const existing = await client.request<TaskRun>('GET', `task-runs/${encodeURIComponent(id)}`);
			return { taskRun: existing, created: false };
		}
		throw err;
	}
}
