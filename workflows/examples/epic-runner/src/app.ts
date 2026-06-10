/**
 * Loom app.ts seam: the bundle's HTTP pipeline.
 *
 * Adds the /healthz endpoint Loom's supervisor polls and mounts Flue's
 * agent routes at the root. Local mode trusts the loopback; cloud mode
 * will add Loom-scoped auth middleware here (Phase 4) without forking
 * Flue.
 */
import { flue } from '@flue/runtime/routing';
import { Hono } from 'hono';

const app = new Hono();

app.get('/healthz', (c) => c.json({ status: 'ok' }));

app.route('/', flue());

export default app;
