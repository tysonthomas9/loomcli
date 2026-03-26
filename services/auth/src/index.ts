import { env } from "./lib/env.js";
import { auth, db, dialect } from "./auth.js";
import { runMigrations } from "./db/migrate.js";

await runMigrations(dialect, db);
console.log(
  "Better Auth configured with providers:",
  Object.keys(auth.options.socialProviders || {}),
);
console.log(`Auth service starting on port ${env.PORT} (${dialect} database)`);
// Hono server will be added by task .10
