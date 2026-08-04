export { parseEnv } from "./env-schema.js";
export type { EnvConfig } from "./env-schema.js";

import { parseEnv } from "./env-schema.js";
import type { EnvConfig } from "./env-schema.js";

function loadEnv(): EnvConfig {
  const result = parseEnv(process.env);

  if (!result.success) {
    const errors = result.error.issues
      .map((issue) => `  - ${issue.path.join(".")}: ${issue.message}`)
      .join("\n");
    process.stderr.write(`ENV VALIDATION FAILED:\n${errors}\n`);
    process.exit(1);
  }

  const config = result.data;

  // Startup banner (never log secrets)
  const banner = [
    `Auth service config:`,
    `  Database: ${config.DATABASE_PROVIDER}`,
    `  GitHub OAuth: ${config.GITHUB_CLIENT_ID ? "enabled" : "disabled"}`,
    `  Google OAuth: ${config.GOOGLE_CLIENT_ID ? "enabled" : "disabled"}`,
    `  JWT audience: ${config.JWT_AUDIENCE}`,
    `  Port: ${config.PORT}`,
    `  Trusted origins: ${config.TRUSTED_ORIGINS.join(", ")}`,
  ].join("\n");
  console.log(banner);

  return Object.freeze(config);
}

export const env: Readonly<EnvConfig> = loadEnv();
