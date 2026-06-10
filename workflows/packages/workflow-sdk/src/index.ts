export { parseWakeMessage, envFleetBaseUrl, envWorkspace, type WakeContext } from './context.js';
export { FleetClient, FleetError, type FleetClientOptions, type Issue, type TaskRun, type LedgerEntry } from './fleet.js';
export { startTask, taskRunId, type StartTaskResult } from './tasks.js';
export { recordAction, type ActionType, type ActionResult } from './actions.js';

import { FleetClient } from './fleet.js';
import { envFleetBaseUrl, type WakeContext } from './context.js';

/**
 * Convenience constructor: a FleetClient scoped to one wake, using the
 * environment Loom injected and the run identity as actor.
 */
export function fleetClientForWake(ctx: WakeContext): FleetClient {
	return new FleetClient({
		baseUrl: envFleetBaseUrl(),
		workspace: ctx.workspace,
		actor: `workflow:${ctx.runId || 'adhoc'}`,
	});
}
