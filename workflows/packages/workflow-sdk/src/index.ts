export { parseWakeMessage, envFleetBaseUrl, envWorkspace, type WakeContext } from './context.js';
export { FleetClient, FleetError, type FleetClientOptions, type Issue, type TaskRun, type LedgerEntry } from './fleet.js';
export { startTask, taskRunId, type StartTaskResult } from './tasks.js';
export { recordAction, type ActionType, type ActionResult } from './actions.js';

import { FleetClient } from './fleet.js';
import { envFleetBaseUrl, type WakeContext } from './context.js';

/**
 * Convenience constructor: a FleetClient scoped to one wake. Prefers
 * the per-wake fleet_base_url from the wake message (always current),
 * falling back to the LOOM_FLEET_BASE_URL the dev server captured at
 * start. The run identity becomes the actor.
 */
export function fleetClientForWake(ctx: WakeContext): FleetClient {
	return new FleetClient({
		baseUrl: ctx.fleetBaseUrl || envFleetBaseUrl(),
		workspace: ctx.workspace,
		actor: `workflow:${ctx.runId || 'adhoc'}`,
	});
}
