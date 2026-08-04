import { describe, it } from "node:test";
import assert from "node:assert/strict";
import crypto from "node:crypto";

// Import from env-schema directly to avoid the module-scope loadEnv()
// in env.ts which calls process.exit(1) when env vars are missing.
import { parseEnv } from "../env-schema.js";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** A valid 64-char hex secret (32 bytes). */
const VALID_HEX_SECRET = crypto.randomBytes(32).toString("hex");

/** A valid base64 secret (>= 32 chars, length divisible by 4). */
const VALID_B64_SECRET = crypto.randomBytes(32).toString("base64");

/** Minimal env that should pass validation. */
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
// Tests
// ---------------------------------------------------------------------------

describe("env validation", () => {
  // 1. Valid config with all required vars
  it("accepts valid config with all required vars and correct typed output", () => {
    const data = expectSuccess(validEnv({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
      GOOGLE_CLIENT_ID: "g-id",
      GOOGLE_CLIENT_SECRET: "g-secret",
      PORT: "4000",
      JWT_AUDIENCE: "my-app",
      TRUSTED_ORIGINS: "http://a.com, http://b.com",
      ALLOWED_EMAIL_DOMAINS: "example.com, test.com",
      ADMIN_USER_IDS: "id1, id2",
      NODE_ENV: "production",
      LOG_LEVEL: "debug",
    }));

    assert.equal(data.BETTER_AUTH_SECRET, VALID_HEX_SECRET);
    assert.equal(data.BETTER_AUTH_URL, "http://localhost:3001");
    assert.equal(data.DATABASE_URL, "file:local.db");
    assert.equal(data.DATABASE_PROVIDER, "sqlite");
    assert.equal(data.GITHUB_CLIENT_ID, "gh-id");
    assert.equal(data.GITHUB_CLIENT_SECRET, "gh-secret");
    assert.equal(data.GOOGLE_CLIENT_ID, "g-id");
    assert.equal(data.GOOGLE_CLIENT_SECRET, "g-secret");
    assert.equal(data.PORT, 4000);
    assert.equal(data.JWT_AUDIENCE, "my-app");
    assert.deepEqual(data.TRUSTED_ORIGINS, ["http://a.com", "http://b.com"]);
    assert.deepEqual(data.ALLOWED_EMAIL_DOMAINS, ["example.com", "test.com"]);
    assert.deepEqual(data.ADMIN_USER_IDS, ["id1", "id2"]);
    assert.equal(data.NODE_ENV, "production");
    assert.equal(data.LOG_LEVEL, "debug");
  });

  // 2. Missing all required vars — all reported in one error
  it("reports all missing required vars in a single error", () => {
    const err = expectFailure({});
    const paths = err.issues.map((i) => i.path.join("."));
    assert.ok(paths.includes("BETTER_AUTH_SECRET"), "should report BETTER_AUTH_SECRET");
    assert.ok(paths.includes("BETTER_AUTH_URL"), "should report BETTER_AUTH_URL");
    assert.ok(paths.includes("DATABASE_URL"), "should report DATABASE_URL");
    assert.ok(paths.includes("DATABASE_PROVIDER"), "should report DATABASE_PROVIDER");
    // All reported at once (at least 4 issues)
    assert.ok(err.issues.length >= 4, `expected >= 4 issues, got ${err.issues.length}`);
  });

  // 3. BETTER_AUTH_SECRET too short
  it("rejects BETTER_AUTH_SECRET shorter than 32 characters", () => {
    const err = expectFailure(validEnv({ BETTER_AUTH_SECRET: "abcdef1234567890" }));
    const msgs = err.issues.map((i) => i.message);
    assert.ok(
      msgs.some((m) => m.includes("at least 32 characters")),
      `expected min-length error, got: ${msgs.join("; ")}`,
    );
  });

  // 4. BETTER_AUTH_SECRET not hex/base64
  it("rejects BETTER_AUTH_SECRET that is neither hex nor base64", () => {
    // 32 chars but contains invalid characters for both hex and base64
    const badSecret = "this-is-not-hex-or-base64!@#$%^&*";
    assert.ok(badSecret.length >= 32);
    const err = expectFailure(validEnv({ BETTER_AUTH_SECRET: badSecret }));
    const msgs = err.issues.map((i) => i.message);
    assert.ok(
      msgs.some((m) => m.includes("hex or base64")),
      `expected format error, got: ${msgs.join("; ")}`,
    );
  });

  // 5. BETTER_AUTH_SECRET all-same-char — low-entropy rejection
  it("rejects BETTER_AUTH_SECRET with all identical characters", () => {
    const sameChar = "a".repeat(64); // valid hex chars, 64 long, but all same
    const err = expectFailure(validEnv({ BETTER_AUTH_SECRET: sameChar }));
    const msgs = err.issues.map((i) => i.message);
    assert.ok(
      msgs.some((m) => m.includes("low-entropy")),
      `expected low-entropy error, got: ${msgs.join("; ")}`,
    );
  });

  // 6. Orphaned OAuth half-pairs
  describe("orphaned OAuth half-pairs", () => {
    it("rejects GITHUB_CLIENT_ID without GITHUB_CLIENT_SECRET", () => {
      const err = expectFailure(validEnv({ GITHUB_CLIENT_ID: "id-only" }));
      const msgs = err.issues.map((i) => i.message);
      assert.ok(
        msgs.some((m) => m.includes("GITHUB_CLIENT_ID requires GITHUB_CLIENT_SECRET")),
        `expected orphan error, got: ${msgs.join("; ")}`,
      );
    });

    it("rejects GITHUB_CLIENT_SECRET without GITHUB_CLIENT_ID", () => {
      const err = expectFailure(validEnv({ GITHUB_CLIENT_SECRET: "secret-only" }));
      const msgs = err.issues.map((i) => i.message);
      assert.ok(
        msgs.some((m) => m.includes("GITHUB_CLIENT_SECRET requires GITHUB_CLIENT_ID")),
        `expected orphan error, got: ${msgs.join("; ")}`,
      );
    });

    it("rejects GOOGLE_CLIENT_ID without GOOGLE_CLIENT_SECRET", () => {
      const err = expectFailure(validEnv({ GOOGLE_CLIENT_ID: "id-only" }));
      const msgs = err.issues.map((i) => i.message);
      assert.ok(
        msgs.some((m) => m.includes("GOOGLE_CLIENT_ID requires GOOGLE_CLIENT_SECRET")),
        `expected orphan error, got: ${msgs.join("; ")}`,
      );
    });

    it("rejects GOOGLE_CLIENT_SECRET without GOOGLE_CLIENT_ID", () => {
      const err = expectFailure(validEnv({ GOOGLE_CLIENT_SECRET: "secret-only" }));
      const msgs = err.issues.map((i) => i.message);
      assert.ok(
        msgs.some((m) => m.includes("GOOGLE_CLIENT_SECRET requires GOOGLE_CLIENT_ID")),
        `expected orphan error, got: ${msgs.join("; ")}`,
      );
    });
  });

  // 7. Invalid DATABASE_PROVIDER
  it("rejects invalid DATABASE_PROVIDER", () => {
    const err = expectFailure(validEnv({ DATABASE_PROVIDER: "mysql" }));
    const msgs = err.issues.map((i) => i.message);
    assert.ok(
      msgs.some((m) => m.includes("'pg' or 'sqlite'")),
      `expected provider error, got: ${msgs.join("; ")}`,
    );
  });

  // 8. PORT out of range
  describe("PORT validation", () => {
    it("rejects PORT = 0", () => {
      const err = expectFailure(validEnv({ PORT: "0" }));
      const paths = err.issues.map((i) => i.path.join("."));
      assert.ok(paths.includes("PORT"), `expected PORT error, got paths: ${paths.join(", ")}`);
    });

    it("rejects PORT = 65536", () => {
      const err = expectFailure(validEnv({ PORT: "65536" }));
      const paths = err.issues.map((i) => i.path.join("."));
      assert.ok(paths.includes("PORT"), `expected PORT error, got paths: ${paths.join(", ")}`);
    });

    it("rejects PORT = -1", () => {
      const err = expectFailure(validEnv({ PORT: "-1" }));
      const paths = err.issues.map((i) => i.path.join("."));
      assert.ok(paths.includes("PORT"), `expected PORT error, got paths: ${paths.join(", ")}`);
    });
  });

  // 9. TRUSTED_ORIGINS comma-separated parsing — trims whitespace, filters empty
  it("parses TRUSTED_ORIGINS: trims whitespace and filters empty segments", () => {
    const data = expectSuccess(validEnv({
      TRUSTED_ORIGINS: "  http://a.com , http://b.com , , http://c.com  ",
    }));
    assert.deepEqual(data.TRUSTED_ORIGINS, ["http://a.com", "http://b.com", "http://c.com"]);
  });

  // 10. TRUSTED_ORIGINS defaults to BETTER_AUTH_URL origin when empty
  it("defaults TRUSTED_ORIGINS to BETTER_AUTH_URL origin when not set", () => {
    const data = expectSuccess(validEnv({
      BETTER_AUTH_URL: "https://auth.example.com:8443/some/path",
    }));
    assert.deepEqual(data.TRUSTED_ORIGINS, ["https://auth.example.com:8443"]);
  });

  it("defaults TRUSTED_ORIGINS to BETTER_AUTH_URL origin when set to empty string", () => {
    const data = expectSuccess(validEnv({
      BETTER_AUTH_URL: "http://localhost:3001",
      TRUSTED_ORIGINS: "",
    }));
    assert.deepEqual(data.TRUSTED_ORIGINS, ["http://localhost:3001"]);
  });

  // 11. JWT_AUDIENCE defaults to 'loom'
  it("defaults JWT_AUDIENCE to 'loom'", () => {
    const data = expectSuccess(validEnv());
    assert.equal(data.JWT_AUDIENCE, "loom");
  });

  // 12. PORT defaults to 3001
  it("defaults PORT to 3001", () => {
    const data = expectSuccess(validEnv());
    assert.equal(data.PORT, 3001);
  });

  // 13. ALLOWED_EMAIL_DOMAINS comma-separated parsing
  it("parses ALLOWED_EMAIL_DOMAINS as comma-separated list", () => {
    const data = expectSuccess(validEnv({
      ALLOWED_EMAIL_DOMAINS: " example.com , test.org ,  foo.io ",
    }));
    assert.deepEqual(data.ALLOWED_EMAIL_DOMAINS, ["example.com", "test.org", "foo.io"]);
  });

  it("defaults ALLOWED_EMAIL_DOMAINS to empty array when not set", () => {
    const data = expectSuccess(validEnv());
    assert.deepEqual(data.ALLOWED_EMAIL_DOMAINS, []);
  });

  // 14. ADMIN_USER_IDS comma-separated parsing
  it("parses ADMIN_USER_IDS as comma-separated list", () => {
    const data = expectSuccess(validEnv({
      ADMIN_USER_IDS: "user-1, user-2 , user-3",
    }));
    assert.deepEqual(data.ADMIN_USER_IDS, ["user-1", "user-2", "user-3"]);
  });

  it("defaults ADMIN_USER_IDS to empty array when not set", () => {
    const data = expectSuccess(validEnv());
    assert.deepEqual(data.ADMIN_USER_IDS, []);
  });

  // 15. Valid base64 secret accepted
  it("accepts a valid base64-encoded secret", () => {
    const data = expectSuccess(validEnv({ BETTER_AUTH_SECRET: VALID_B64_SECRET }));
    assert.equal(data.BETTER_AUTH_SECRET, VALID_B64_SECRET);
  });
});
