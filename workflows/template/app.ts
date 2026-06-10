/**
 * Template app.ts for new Loom workflow projects.
 *
 * Copy into your project's src/ directory. This is the sanctioned
 * extension seam: Loom's supervisor requires GET /healthz, and any
 * auth middleware or provider configuration belongs here — never fork
 * the generated Flue server.
 */
import { flue } from '@flue/runtime/routing';
import { Hono } from 'hono';

const app = new Hono();

// Required: Loom's Flue supervisor polls this during startup and reuse.
app.get('/healthz', (c) => c.json({ status: 'ok' }));

// Optional: per-request provider overrides, e.g. a model gateway:
// import { configureProvider } from '@flue/runtime';
// app.use('*', async (_c, next) => {
//   configureProvider('anthropic', { baseUrl: process.env.ANTHROPIC_GATEWAY_URL });
//   return next();
// });

// Mount Flue's agent/workflow routes at the root (Loom invokes
// POST /agents/{name}/{id} here).
app.route('/', flue());

export default app;
