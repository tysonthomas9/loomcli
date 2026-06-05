/**
 * app.ts — flue server entry for loom's embedded project.
 *
 * Two responsibilities:
 *   1. Expose GET /healthz so loom's long-lived lead server can be
 *      health-checked with netutil.WaitForHealthz (Phase 2).
 *   2. Codex auth bridge: if the host has local `codex login` credentials
 *      (~/.codex/auth.json with a ChatGPT OAuth access token), register them
 *      as the `openai-codex` provider so loom users authenticated with the
 *      Codex CLI can use the flue backend with no separate API key. loom
 *      selects the `openai-codex/<model>` model (see backend_flue.go).
 *
 * The codex block is guarded: when codex auth is absent or unreadable, the
 * provider is simply not registered and the agent falls back to whatever model
 * loom passes (e.g. anthropic/* with ANTHROPIC_API_KEY). No secret is embedded;
 * the token is read from the host's own file at runtime.
 *
 * Note: pi-ai's clamp does not reliably bound the prompt_cache_key on the codex
 * paths, and the ChatGPT Codex backend caps it at 64 chars — keep workflow and
 * agent names short (the default workflow is `agent`; the lead agent is `lead`).
 */
import { registerProvider } from '@flue/runtime';
import { flue } from '@flue/runtime/routing';
import { Hono } from 'hono';
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

function codexAuthPath(): string {
	const codexHome = process.env.CODEX_HOME?.trim();
	return codexHome ? join(codexHome, 'auth.json') : join(homedir(), '.codex', 'auth.json');
}

try {
	const auth = JSON.parse(readFileSync(codexAuthPath(), 'utf8'));
	const token: unknown = auth?.tokens?.access_token;
	if (typeof token === 'string' && token.length > 0) {
		registerProvider('openai-codex', {
			api: 'openai-codex-responses',
			baseUrl: 'https://chatgpt.com/backend-api',
			apiKey: token,
		});
	}
} catch {
	// No local codex auth (or unreadable) — provider stays unregistered.
}

const app = new Hono();
// Liveness endpoint for loom's lead-server lifecycle (WaitForHealthz).
app.get('/healthz', (c) => c.json({ ok: true }));

// flue() snapshots the agent/workflow registry at call time, and the generated
// server configures that registry at module load — AFTER this module is
// evaluated. So build the flue router lazily on the first request, by which
// point the runtime (and its discovered agents) is configured.
let flueApp: ReturnType<typeof flue> | undefined;
app.all('*', (c) => {
	if (!flueApp) flueApp = flue();
	return flueApp.fetch(c.req.raw);
});

export default app;
