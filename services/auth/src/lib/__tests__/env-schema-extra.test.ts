import { describe, it } from "node:test";
import assert from "node:assert/strict";
import crypto from "node:crypto";

import { parseEnv } from "../env-schema.js";

// ---------------------------------------------------------------------------
// Helpers (same pattern as env.test.ts)
// ---------------------------------------------------------------------------

const VALID_HEX_SECRET = crypto.randomBytes(32).toString("hex");

function validEnv(overrides: Record<string, string | undefined> = {}): Record<string, string | undefined> {
  return {
    BETTER_AUTH_SECRET: VALID_HEX_SECRET,
    BETTER_AUTH_URL: "http://localhost:3001",
    DATABASE_URL: "file:local.db",
    DATABASE_PROVIDER: "sqlite",
    ...overrides,
  };
}

function expectSuccess(raw: Record<string, string | undefined>) {
  const result = parseEnv(raw);
  assert.equal(result.success, true, `Expected success but got errors: ${!result.success ? result.error.issues.map((i) => i.message).join("; ") : ""}`);
  if (!result.success) throw new Error("unreachable");
  return result.data;
}

function expectFailure(raw: Record<string, string | undefined>) {
  const result = parseEnv(raw);
  assert.equal(result.success, false, "Expected validation failure but got success");
  if (result.success) throw new Error("unreachable");
  return result.error;
}

// ---------------------------------------------------------------------------
// Tests — gaps not covered by env.test.ts
// ---------------------------------------------------------------------------

describe("env-schema extra coverage", () => {
  // METRICS_PORT defaults to 9090
  it("defaults METRICS_PORT to 9090", () => {
    const data = expectSuccess(validEnv());
    assert.equal(data.METRICS_PORT, 9090);
  });

  // METRICS_PORT accepts a custom value
  it("accepts a custom METRICS_PORT value", () => {
    const data = expectSuccess(validEnv({ METRICS_PORT: "9100" }));
    assert.equal(data.METRICS_PORT, 9100);
  });

  // METRICS_PORT rejects 0
  it("rejects METRICS_PORT = 0", () => {
    const err = expectFailure(validEnv({ METRICS_PORT: "0" }));
    const paths = err.issues.map((i) => i.path.join("."));
    assert.ok(paths.includes("METRICS_PORT"), `expected METRICS_PORT error, got paths: ${paths.join(", ")}`);
  });

  // METRICS_PORT rejects 65536
  it("rejects METRICS_PORT = 65536", () => {
    const err = expectFailure(validEnv({ METRICS_PORT: "65536" }));
    const paths = err.issues.map((i) => i.path.join("."));
    assert.ok(paths.includes("METRICS_PORT"), `expected METRICS_PORT error, got paths: ${paths.join(", ")}`);
  });

  // NODE_ENV defaults to "development"
  it("defaults NODE_ENV to 'development'", () => {
    const data = expectSuccess(validEnv());
    assert.equal(data.NODE_ENV, "development");
  });

  // LOG_LEVEL defaults to "info"
  it("defaults LOG_LEVEL to 'info'", () => {
    const data = expectSuccess(validEnv());
    assert.equal(data.LOG_LEVEL, "info");
  });

  // PORT boundary: accepts 1
  it("accepts PORT = 1 (lower boundary)", () => {
    const data = expectSuccess(validEnv({ PORT: "1" }));
    assert.equal(data.PORT, 1);
  });

  // PORT boundary: accepts 65535
  it("accepts PORT = 65535 (upper boundary)", () => {
    const data = expectSuccess(validEnv({ PORT: "65535" }));
    assert.equal(data.PORT, 65535);
  });

  // METRICS_PORT boundary: accepts 1
  it("accepts METRICS_PORT = 1 (lower boundary)", () => {
    const data = expectSuccess(validEnv({ METRICS_PORT: "1" }));
    assert.equal(data.METRICS_PORT, 1);
  });

  // METRICS_PORT boundary: accepts 65535
  it("accepts METRICS_PORT = 65535 (upper boundary)", () => {
    const data = expectSuccess(validEnv({ METRICS_PORT: "65535" }));
    assert.equal(data.METRICS_PORT, 65535);
  });

  // Non-numeric PORT string
  it("rejects non-numeric PORT string", () => {
    const err = expectFailure(validEnv({ PORT: "abc" }));
    const paths = err.issues.map((i) => i.path.join("."));
    assert.ok(paths.includes("PORT"), `expected PORT error, got paths: ${paths.join(", ")}`);
  });

  // Non-numeric METRICS_PORT string
  it("rejects non-numeric METRICS_PORT string", () => {
    const err = expectFailure(validEnv({ METRICS_PORT: "not-a-number" }));
    const paths = err.issues.map((i) => i.path.join("."));
    assert.ok(paths.includes("METRICS_PORT"), `expected METRICS_PORT error, got paths: ${paths.join(", ")}`);
  });

  // -------------------------------------------------------------------------
  // EMAIL_PASSWORD_ENABLED
  // -------------------------------------------------------------------------

  it("defaults EMAIL_PASSWORD_ENABLED to false when unset", () => {
    const data = expectSuccess(validEnv());
    assert.equal(data.EMAIL_PASSWORD_ENABLED, false);
  });

  it("parses EMAIL_PASSWORD_ENABLED='true' as true", () => {
    const data = expectSuccess(validEnv({ EMAIL_PASSWORD_ENABLED: "true" }));
    assert.equal(data.EMAIL_PASSWORD_ENABLED, true);
  });

  it("parses EMAIL_PASSWORD_ENABLED='1' as true", () => {
    const data = expectSuccess(validEnv({ EMAIL_PASSWORD_ENABLED: "1" }));
    assert.equal(data.EMAIL_PASSWORD_ENABLED, true);
  });

  it("parses EMAIL_PASSWORD_ENABLED='false' as false", () => {
    const data = expectSuccess(validEnv({ EMAIL_PASSWORD_ENABLED: "false" }));
    assert.equal(data.EMAIL_PASSWORD_ENABLED, false);
  });

  it("parses EMAIL_PASSWORD_ENABLED with arbitrary value as false", () => {
    const data = expectSuccess(validEnv({ EMAIL_PASSWORD_ENABLED: "yes" }));
    assert.equal(data.EMAIL_PASSWORD_ENABLED, false);
  });
});
