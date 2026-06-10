/**
 * Loom app.ts seam: the bundle's HTTP pipeline.
 *
 * Adds the /healthz endpoint Loom's supervisor polls and mounts Flue's
 * agent routes at the root. Local mode trusts the loopback; cloud mode
 * will add Loom-scoped auth middleware here (Phase 4) without forking
 * Flue.
 *
 * Provider auth (dev): when the agent runs on the `openai-codex`
 * provider (LOOM_WORKFLOW_MODEL=openai-codex/...), the ChatGPT OAuth
 * access token from the Codex CLI's auth file is used. It is re-read
 * per request so the Codex CLI's background token refresh is picked up
 * without restarting the Flue child.
 */
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { configureProvider } from '@flue/runtime';
import { flue } from '@flue/runtime/routing';
import { Hono } from 'hono';

function codexAccessToken(): string | undefined {
	try {
		const authPath = process.env.CODEX_HOME
			? join(process.env.CODEX_HOME, 'auth.json')
			: join(homedir(), '.codex', 'auth.json');
		const auth = JSON.parse(readFileSync(authPath, 'utf8')) as {
			tokens?: { access_token?: string };
			OPENAI_API_KEY?: string;
		};
		return auth.tokens?.access_token ?? auth.OPENAI_API_KEY ?? undefined;
	} catch {
		return undefined;
	}
}

const app = new Hono();

app.get('/healthz', (c) => c.json({ status: 'ok' }));

app.use('*', async (_c, next) => {
	const token = codexAccessToken();
	if (token) {
		configureProvider('openai-codex', { apiKey: token });
	}
	return next();
});

app.route('/', flue());

export default app;
