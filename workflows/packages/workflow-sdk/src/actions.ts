/**
 * Effectively-once side effects via fleet-db's ActionLedger.
 *
 * recordAction(client, key, …, effect) is the only sanctioned way for
 * workflow code to mutate the world: it opens an idempotency-keyed
 * ledger entry, runs the effect only when no prior wake applied it,
 * and completes the entry. Replayed wakes see an `applied` entry and
 * skip the effect.
 *
 * Failure semantics: when the effect throws, the entry stays `pending`
 * so the next wake retries it (the effect itself must be idempotent —
 * at-least-once with idempotent effects is the system contract).
 */

import { FleetClient, FleetError, type LedgerEntry } from './fleet.js';

export type ActionType =
	| 'close_task'
	| 'comment'
	| 'create_pr'
	| 'merge_pr'
	| 'start_task_run'
	| 'update_status';

export interface ActionResult {
	entry: LedgerEntry;
	/** False when a prior wake already applied this action. */
	applied: boolean;
}

export async function recordAction(
	client: FleetClient,
	idempotencyKey: string,
	actionType: ActionType,
	targetRef: string,
	effect: () => Promise<void>,
): Promise<ActionResult> {
	// Create is idempotent: a repeat returns the existing entry.
	const entry = await client.request<LedgerEntry>('POST', 'action-ledger', {
		idempotency_key: idempotencyKey,
		action_type: actionType,
		target_ref: targetRef,
	});
	if (entry.status !== 'pending') {
		return { entry, applied: false };
	}

	await effect(); // throws → entry stays pending, next wake retries

	try {
		const done = await client.request<LedgerEntry>(
			'POST',
			`action-ledger/${encodeURIComponent(entry.action_id)}/complete`,
			{ status: 'applied' },
		);
		return { entry: done, applied: true };
	} catch (err) {
		// A concurrent wake may have completed it first — that is success.
		if (err instanceof FleetError && err.status === 409) {
			return { entry, applied: true };
		}
		throw err;
	}
}
