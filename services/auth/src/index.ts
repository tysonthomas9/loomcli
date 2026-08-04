import type { ServerType } from "@hono/node-server";
import { Hono } from "hono";
import { cors } from "hono/cors";
import { serve } from "@hono/node-server";
import { sql } from "drizzle-orm";
import type { BetterSQLite3Database } from "drizzle-orm/better-sqlite3";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import { auth, db, closeDb, dialect } from "./auth.js";
import { runMigrations } from "./db/migrate.js";
import { env } from "./lib/env.js";
import { logger } from "./lib/logger.js";
import { metrics, renderMetrics } from "./lib/metrics.js";

// ============================================================
// Main application server (public-facing, port from env.PORT)
// ============================================================
const app = new Hono();

// --- Middleware ---

// Request logging (skip /health to avoid probe spam)
app.use("*", async (c, next) => {
  const start = Date.now();
  await next();
  if (c.req.path !== "/health") {
    logger.info("request", {
      method: c.req.method,
      path: c.req.path,
      status: c.res.status,
      duration_ms: Date.now() - start,
      ip: c.req.header("x-forwarded-for") ?? c.req.header("x-real-ip") ?? "unknown",
    });
  }
});

// TLS enforcement warning (production only, rate-limited to 1 per 60s)
let lastTlsWarning = 0;
if (env.NODE_ENV === "production") {
  app.use("*", async (c, next) => {
    const proto = c.req.header("x-forwarded-proto");
    if (!proto || proto !== "https") {
      const now = Date.now();
      if (now - lastTlsWarning > 60_000) {
        lastTlsWarning = now;
        logger.warn("request received without TLS termination in production", {
          path: c.req.path,
          x_forwarded_proto: proto ?? "missing",
          hint: "ensure a reverse proxy provides HTTPS and sets X-Forwarded-Proto",
        });
      }
    }
    await next();
  });
}

// CORS
app.use(
  "/api/*",
  cors({
    origin: env.TRUSTED_ORIGINS,
    allowMethods: ["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
    allowHeaders: ["Content-Type", "Authorization", "X-Requested-With"],
    credentials: true,
    maxAge: 86400,
  }),
);

// Metrics counting on auth routes
app.use("/api/auth/*", async (c, next) => {
  await next();
  const path = c.req.path;
  const method = c.req.method;
  const status = c.res.status;

  if (path.includes("/sign-in") && method === "POST") {
    metrics.signInAttempts.inc();
    if (status === 200) metrics.sessionCreated.inc();
  }
  if (path.endsWith("/token") && method === "POST" && status === 200) {
    metrics.tokenIssued.inc();
  }
  if (status === 429) {
    metrics.rateLimitTriggered.inc();
  }
});

// --- Routes ---

// Health check with DB ping
app.get("/health", async (c) => {
  try {
    // Adapters expose different raw-query methods; branch on dialect
    if (dialect === "pg") {
      await (db as unknown as NodePgDatabase).execute(sql`SELECT 1`);
    } else {
      (db as unknown as BetterSQLite3Database).run(sql`SELECT 1`);
    }
    return c.json({ status: "ok" }, 200);
  } catch (err) {
    logger.error("health check failed", {
      error: err instanceof Error ? err.message : String(err),
    });
    return c.json(
      {
        status: "degraded",
        error: err instanceof Error ? err.message : "database unreachable",
      },
      503,
    );
  }
});

// Per-session rate limit on POST /api/auth/token (10 req/min per session)
// Prevents token stockpiling from a compromised session
const tokenRateLimit = new Map<string, { count: number; resetAt: number }>();

app.use("/api/auth/token", async (c, next) => {
  if (c.req.method === "POST") {
    const cookies = c.req.header("cookie") ?? "";
    const sessionMatch = cookies.match(/(?:__Secure-)?better-auth\.session_token=([^;]+)/);
    const sessionKey =
      sessionMatch?.[1] ?? c.req.header("x-forwarded-for") ?? "unknown";

    const now = Date.now();
    const window = 60_000; // 1 minute
    const maxRequests = 10;

    let entry = tokenRateLimit.get(sessionKey);
    if (!entry || now >= entry.resetAt) {
      entry = { count: 0, resetAt: now + window };
      tokenRateLimit.set(sessionKey, entry);
    }

    entry.count++;
    if (entry.count > maxRequests) {
      logger.warn("token endpoint rate limited", {
        sessionKey: sessionKey.slice(0, 8) + "...",
      });
      return c.json({ error: "rate limit exceeded" }, 429);
    }

    // Periodically clean stale entries
    if (tokenRateLimit.size > 1000) {
      for (const [key, val] of tokenRateLimit) {
        if (now >= val.resetAt) tokenRateLimit.delete(key);
      }
    }
  }

  await next();

  // Cache-Control: no-store on token endpoint responses
  // Prevents browser/proxy caching of JWTs
  // NOTE: c.header() doesn't apply to raw Response objects returned by
  // auth.handler(), so we must clone the response with modified headers.
  if (c.req.method === "POST" && c.res) {
    const original = c.res;
    const headers = new Headers(original.headers);
    headers.set("Cache-Control", "no-store");
    headers.set("Pragma", "no-cache");
    c.res = new Response(original.body, {
      status: original.status,
      statusText: original.statusText,
      headers,
    });
  }
});

// Mount Better Auth handler
app.on(["GET", "POST"], "/api/auth/*", (c) => {
  return auth.handler(c.req.raw);
});

// ============================================================
// Internal metrics server (separate port, NOT public-facing)
// ============================================================
const metricsApp = new Hono();

metricsApp.get("/metrics", (c) => {
  c.header("Content-Type", "text/plain; version=0.0.4; charset=utf-8");
  return c.text(renderMetrics());
});

// ============================================================
// Server startup
// ============================================================
async function main(): Promise<void> {
  await runMigrations(dialect, db);
  logger.info("database migrations complete");

  const providers = Object.keys(auth.options.socialProviders ?? {});
  logger.info("auth service starting", {
    port: env.PORT,
    metricsPort: env.METRICS_PORT,
    dialect,
    providers,
    mode: providers.length > 0 ? "oauth" : "minimal",
    jwt: "RS256, 15m TTL, 7d rotation, 24h grace",
    audience: env.JWT_AUDIENCE,
  });

  const server = serve(
    { fetch: app.fetch, port: env.PORT },
    (info: { port: number }) => {
      logger.info("auth service listening", { port: info.port });
    },
  );

  const metricsServer = serve(
    { fetch: metricsApp.fetch, port: env.METRICS_PORT, hostname: "127.0.0.1" },
    (info: { port: number }) => {
      logger.info("metrics server listening (internal only)", {
        port: info.port,
      });
    },
  );

  // Graceful shutdown — drain in-flight requests before closing DB
  const closeServer = (s: ServerType): Promise<void> =>
    new Promise<void>((resolve, reject) =>
      s.close((err?: Error) => (err ? reject(err) : resolve())),
    );

  let shuttingDown = false;
  const shutdown = async (signal: string): Promise<void> => {
    if (shuttingDown) return;
    shuttingDown = true;
    logger.info("shutdown initiated", { signal });
    await Promise.all([closeServer(server), closeServer(metricsServer)]);
    await closeDb();
    logger.info("shutdown complete");
    process.exit(0);
  };

  process.on("SIGINT", () => void shutdown("SIGINT"));
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
}

main().catch((err: unknown) => {
  logger.error("fatal startup error", {
    error: err instanceof Error ? err.message : String(err),
  });
  process.exit(1);
});
