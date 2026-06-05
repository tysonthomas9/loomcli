/**
 * lead — the durable, multi-turn agent backing `loom lead --backend flue`.
 *
 * loom runs a long-lived flue server (node dist/server.mjs) and connects to
 * this agent over HTTP+SSE at POST /agents/lead/<instanceId>. Each workspace
 * maps to a stable <instanceId> so the conversation persists across prompts
 * and reconnects (browser "Talk to Lead" tab, or re-running `loom lead`).
 *
 * Sandbox selection:
 *   - DAYTONA_API_KEY set  → the agent's tools run in a remote Daytona sandbox.
 *   - otherwise            → local() on the host worktree (LOOM_WORKTREE_PATH).
 * loom forwards DAYTONA_API_KEY / LOOM_WORKTREE_PATH / LOOM_FLUE_MODEL into the
 * server environment.
 *
 * The agent name is intentionally short (`lead`) to keep the codex
 * prompt_cache_key affinity within the ChatGPT Codex backend's 64-char cap.
 */
import { createAgent, type AgentRouteHandler } from '@flue/runtime';
import { local } from '@flue/runtime/node';
import { Daytona } from '@daytona/sdk';
import { daytona } from '../connectors/daytona';

// Enable HTTP exposure: an agent is only served at POST/GET /agents/lead/<id>
// when it exports a `route` (HTTP) or `websocket` middleware. loom drives the
// lead conversation over HTTP+SSE, so a pass-through route is required.
export const route: AgentRouteHandler = async (_c, next) => next();

export default createAgent(async ({ env }) => {
	// loom resolves the model (LOOM_FLUE_MODEL), including the codex bridge
	// (openai-codex/<model>) when only local codex auth is available.
	const model = env.LOOM_FLUE_MODEL || 'anthropic/claude-sonnet-4-6';

	// Remote Daytona sandbox when a key is present: the agent's bash/file tools
	// execute inside the Daytona container rather than on the host.
	const daytonaKey = env.DAYTONA_API_KEY?.trim();
	if (daytonaKey) {
		const client = new Daytona({ apiKey: daytonaKey });
		const sandbox = await client.create();
		return { sandbox: daytona(sandbox), model };
	}

	// Default: operate on the workspace directory on the host.
	return {
		sandbox: local({ cwd: env.LOOM_WORKTREE_PATH ?? process.cwd() }),
		model,
	};
});
