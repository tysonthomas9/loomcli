/**
 * Run context for one workflow wake.
 *
 * Loom's reconciler delivers each wake as a JSON message:
 *   {"action":"advance","workspace":"…","epic_id":"…","run_id":"…"}
 * and injects connection config into the Flue child's environment:
 *   LOOM_FLEET_BASE_URL, LOOM_WORKSPACE.
 */

export interface WakeContext {
	action: string;
	workspace: string;
	epicId: string;
	runId: string;
}

/** Parses the reconciler's wake message. Throws on malformed input. */
export function parseWakeMessage(message: string): WakeContext {
	let raw: Record<string, unknown>;
	try {
		raw = JSON.parse(message) as Record<string, unknown>;
	} catch (err) {
		throw new Error(`wake message is not JSON: ${String(err)}`);
	}
	const str = (key: string): string => (typeof raw[key] === 'string' ? (raw[key] as string) : '');
	const ctx: WakeContext = {
		action: str('action'),
		workspace: str('workspace') || envWorkspace(),
		epicId: str('epic_id'),
		runId: str('run_id'),
	};
	if (!ctx.workspace) {
		throw new Error('wake message missing workspace (and LOOM_WORKSPACE unset)');
	}
	return ctx;
}

/** FleetDB base URL from the environment Loom injected. */
export function envFleetBaseUrl(): string {
	const url = process.env.LOOM_FLEET_BASE_URL ?? '';
	if (!url) {
		throw new Error('LOOM_FLEET_BASE_URL is not set — is this running under `loom workflow dev`?');
	}
	return url.replace(/\/+$/, '');
}

/** Active workspace from the environment Loom injected. */
export function envWorkspace(): string {
	return process.env.LOOM_WORKSPACE ?? '';
}
