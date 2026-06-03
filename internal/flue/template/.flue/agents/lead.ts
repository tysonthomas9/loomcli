/**
 * lead — the durable, multi-turn agent backing `loom lead --backend flue`.
 *
 * loom runs a long-lived flue server (node dist/server.mjs) and connects to
 * this agent over HTTP+SSE at POST /agents/lead/<instanceId>. Each workspace
 * maps to a stable <instanceId> so the conversation persists across prompts
 * and reconnects (browser "Talk to Lead" tab, or re-running `loom lead`).
 *
 * The lead system prompt is dynamic (rendered by loom per workspace), so it is
 * delivered as the first message from loom rather than baked in as static
 * `instructions`. The agent operates on the workspace via the local() sandbox.
 *
 * The agent name is intentionally short (`lead`) to keep the codex
 * prompt_cache_key affinity within the ChatGPT Codex backend's 64-char cap.
 */
import { createAgent, type AgentRouteHandler } from '@flue/runtime';
import { local } from '@flue/runtime/node';

// Enable HTTP exposure: an agent is only served at POST/GET /agents/lead/<id>
// when it exports a `route` (HTTP) or `websocket` middleware. loom drives the
// lead conversation over HTTP+SSE, so a pass-through route is required.
export const route: AgentRouteHandler = async (_c, next) => next();

export default createAgent(({ env }) => ({
	// loom sets LOOM_WORKTREE_PATH when it spawns the server so the lead agent
	// operates on the workspace directory; falls back to the server cwd.
	sandbox: local({ cwd: env.LOOM_WORKTREE_PATH ?? process.cwd() }),
	// loom resolves the model (LOOM_FLUE_MODEL), including the codex bridge
	// (openai-codex/<model>) when only local codex auth is available.
	model: env.LOOM_FLUE_MODEL || 'anthropic/claude-sonnet-4-6',
}));
