/**
 * The epic runner: a level-triggered reconciler with memory.
 *
 * Loom wakes this agent's per-epic instance (`epic-runner/<epic-id>`)
 * with a JSON message naming the epic and the DriverRun. Each wake
 * re-derives the epic's frontier from FleetDB, starts TaskRuns for
 * ready children (idempotent — `tr-<task-id>`), and closes the epic
 * through the ActionLedger once every child is closed. Because the
 * instance's session persists across wakes, the agent remembers WHY it
 * made earlier decisions; correctness never depends on that memory —
 * state is always re-read from FleetDB.
 */
import { type AgentRouteHandler, createAgent, defineTool } from '@flue/runtime';
import {
	fleetClientForWake,
	parseWakeMessage,
	recordAction,
	startTask,
} from '@loom/workflow-sdk';

// Opt the agent into HTTP exposure. Local mode trusts the loopback;
// cloud mode adds Loom-scoped auth here (Phase 4).
export const route: AgentRouteHandler = async (_c, next) => next();

/** Unpacks the wake message every tool receives into its run context + client. */
function wakeClient(args: Record<string, unknown>) {
	const ctx = parseWakeMessage(String(args.wake_message));
	return { ctx, client: fleetClientForWake(ctx) };
}

const listEpicState = defineTool({
	name: 'list_epic_state',
	description:
		'Fetch the current state of an epic from FleetDB: its ready frontier (unblocked open child tasks) and all children with statuses. Always call this first — state must be re-derived on every wake.',
	parameters: {
		type: 'object',
		properties: {
			wake_message: { type: 'string', description: 'The exact JSON wake message you received.' },
		},
		required: ['wake_message'],
	},
	async execute(args: Record<string, unknown>) {
		const { ctx, client } = wakeClient(args);
		const [ready, children] = await Promise.all([
			client.listReadyTasks(ctx.epicId),
			client.listChildren(ctx.epicId),
		]);
		return JSON.stringify({
			epic_id: ctx.epicId,
			ready: ready.map((t) => ({ id: t.id, title: t.title, status: t.status })),
			children: children.map((t) => ({ id: t.id, title: t.title, status: t.status })),
		});
	},
});

const startTaskRuns = defineTool({
	name: 'start_task_runs',
	description:
		'Start a TaskRun for each listed ready child task. Idempotent: a task that already has a TaskRun is reported as existing, never duplicated. The existing Loom agent supervisor executes the tasks.',
	parameters: {
		type: 'object',
		properties: {
			wake_message: { type: 'string', description: 'The exact JSON wake message you received.' },
			task_ids: { type: 'array', items: { type: 'string' }, description: 'Ready child task IDs to start.' },
		},
		required: ['wake_message', 'task_ids'],
	},
	async execute(args: Record<string, unknown>) {
		const { ctx, client } = wakeClient(args);
		const task_ids = (args.task_ids ?? []) as string[];
		const results: Record<string, string> = {};
		for (const taskId of task_ids) {
			const res = await startTask(client, ctx, taskId);
			results[taskId] = res.created ? 'started' : 'already-started';
		}
		return JSON.stringify(results);
	},
});

const closeEpic = defineTool({
	name: 'close_epic',
	description:
		'Close the epic. Only call when the frontier is empty AND every child task is closed. Effectively-once via the ActionLedger — safe to call on a replayed wake.',
	parameters: {
		type: 'object',
		properties: {
			wake_message: { type: 'string', description: 'The exact JSON wake message you received.' },
		},
		required: ['wake_message'],
	},
	async execute(args: Record<string, unknown>) {
		const { ctx, client } = wakeClient(args);
		const res = await recordAction(client, `close-epic:${ctx.epicId}`, 'update_status', ctx.epicId, async () => {
			await client.closeIssue(ctx.epicId, 'epic runner: all children complete');
		});
		return res.applied ? 'epic closed' : 'epic was already closed by a prior wake';
	},
});

export default createAgent(() => ({
	name: 'epic-runner',
	description: 'Level-triggered epic reconciler: advances one epic per wake.',
	model: 'anthropic/claude-sonnet-4-6',
	instructions: `You are an epic runner — a reconciler that advances one epic per wake.

Each message you receive is one wake: a JSON object with action,
workspace, epic_id, and run_id. Treat it as a signal, not as state.

On every wake, do exactly this:
1. Call list_epic_state with the wake message to re-derive reality.
2. If the ready frontier is non-empty: call start_task_runs for ALL
   ready task IDs, then summarize what you started and STOP.
3. If the frontier is empty and every child is closed: call close_epic,
   then summarize and STOP.
4. If the frontier is empty but children are still open or in progress:
   there is nothing to do — say you are waiting on those tasks and STOP.

Rules:
- Never invent task IDs; only use IDs returned by list_epic_state.
- Pass the wake message JSON through to every tool verbatim.
- All tools are idempotent; calling them on a replayed wake is safe.
- Keep your final summary to one or two sentences.`,
	tools: [listEpicState, startTaskRuns, closeEpic],
	durability: { timeout: 15 },
}));
