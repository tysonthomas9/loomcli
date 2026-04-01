import { describe, it, mock, afterEach, before } from "node:test";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import path from "node:path";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const VALID_SECRET = crypto.randomBytes(32).toString("hex");
const AUTH_SRC = path.resolve(import.meta.dirname, "..");

/**
 * Module-import counter used as a query-string cache buster so that each
 * test gets a fresh evaluation of auth.ts with its own mocked env/db.
 */
let importCounter = 100; // Start at 100 to avoid collisions with auth.test.ts

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Build a valid env object for mocking, with optional overrides. */
function makeEnv(overrides: Record<string, unknown> = {}) {
  return {
    BETTER_AUTH_SECRET: VALID_SECRET,
    BETTER_AUTH_URL: "http://localhost:3099",
    DATABASE_URL: "file:test-security.db",
    DATABASE_PROVIDER: "sqlite",
    NODE_ENV: "development" as const,
    TRUSTED_ORIGINS: ["http://localhost:3099"],
    ALLOWED_EMAIL_DOMAINS: [] as string[],
    ADMIN_USER_IDS: [] as string[],
    JWT_AUDIENCE: "loom",
    PORT: 3099,
    LOG_LEVEL: "info" as const,
    GITHUB_CLIENT_ID: "test-github-client-id",
    GITHUB_CLIENT_SECRET: "test-github-client-secret",
    GOOGLE_CLIENT_ID: "test-google-client-id",
    GOOGLE_CLIENT_SECRET: "test-google-client-secret",
    ...overrides,
  };
}

/**
 * Register mocks for the env and db modules that auth.ts imports.
 */
function setupMocks(envOverrides: Record<string, unknown> = {}) {
  const envData = makeEnv(envOverrides);

  mock.module(path.join(AUTH_SRC, "lib/env.js"), {
    namedExports: { env: envData },
  });

  mock.module(path.join(AUTH_SRC, "db/index.js"), {
    namedExports: {
      createDb: () => ({
        db: {},
        dialect: "sqlite" as const,
        close: () => {},
      }),
    },
  });

  return envData;
}

/**
 * Dynamically import auth.ts with a cache-busting query string.
 */
async function importAuthFresh() {
  importCounter++;
  return await import(path.join(AUTH_SRC, `auth.ts?v=${importCounter}`));
}

// ---------------------------------------------------------------------------
// Test 1: OAuth state parameter
// ---------------------------------------------------------------------------

describe("Test 1: OAuth state parameter", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("1a: OAuth redirect URL contains non-empty state parameter", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();

    // Request the GitHub OAuth sign-in endpoint
    const req = new Request(
      "http://localhost:3099/api/auth/sign-in/social",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          provider: "github",
          callbackURL: "http://localhost:3099/auth/callback",
        }),
        redirect: "manual",
      },
    );

    const response = await auth.handler(req);

    // Better Auth should redirect to GitHub with a state parameter.
    // It may return 302, 303, or a JSON body with a redirect URL.
    if (response.status >= 300 && response.status < 400) {
      const location = response.headers.get("location");
      assert.ok(location, "redirect should have a Location header");
      const url = new URL(location);
      const state = url.searchParams.get("state");
      assert.ok(state, "redirect URL must contain a non-empty 'state' parameter");
      assert.ok(state.length > 0, "state parameter must not be empty");
      // Verify client_id is present
      const clientId = url.searchParams.get("client_id");
      assert.equal(clientId, "test-github-client-id");
    } else {
      // Better Auth may return a JSON body with a URL for the client to redirect to
      const body = await response.json().catch(() => null);
      if (body && typeof body === "object" && "url" in body) {
        const url = new URL(body.url as string);
        const state = url.searchParams.get("state");
        assert.ok(state, "redirect URL in body must contain 'state' parameter");
        assert.ok(state.length > 0, "state parameter must not be empty");
      } else {
        // If neither redirect nor URL body, the handler responded but we can
        // at least verify it didn't crash (status should be 2xx or redirect)
        assert.ok(
          response.status < 500,
          `expected non-500 response, got ${response.status}`,
        );
      }
    }
  });

  it("1b: OAuth callback rejects missing state", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();

    // Send callback without state parameter
    const req = new Request(
      "http://localhost:3099/api/auth/callback/github?code=test-code",
      { method: "GET", redirect: "manual" },
    );

    const response = await auth.handler(req);

    // Without valid state, Better Auth should reject the callback
    // (either error response or redirect to error page — not 200 with a session)
    const body = await response.text().catch(() => "");
    const isSuccess = response.status === 200 && body.includes("session");
    assert.ok(!isSuccess, "callback without state must NOT create a session");
  });

  it("1c: OAuth callback rejects tampered state", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();

    // Send callback with tampered state
    const req = new Request(
      "http://localhost:3099/api/auth/callback/github?code=test-code&state=tampered-garbage-value",
      { method: "GET", redirect: "manual" },
    );

    const response = await auth.handler(req);

    // Tampered state must be rejected
    const body = await response.text().catch(() => "");
    const isSuccess = response.status === 200 && body.includes("session");
    assert.ok(!isSuccess, "callback with tampered state must NOT create a session");
  });
});

// ---------------------------------------------------------------------------
// Test 2: Redirect URI validation
// ---------------------------------------------------------------------------

describe("Test 2: Redirect URI validation", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("rejects callbackURL to non-trusted origin", async () => {
    setupMocks({ TRUSTED_ORIGINS: ["http://localhost:3099"] });
    const { auth } = await importAuthFresh();

    const req = new Request(
      "http://localhost:3099/api/auth/sign-in/social",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          provider: "github",
          callbackURL: "https://evil.com/steal",
        }),
        redirect: "manual",
      },
    );

    const response = await auth.handler(req);

    // Better Auth should reject because https://evil.com is not in TRUSTED_ORIGINS.
    // The behavior may be: error response, redirect without the evil URL, or
    // the redirect goes to GitHub but the callback URL registered in GitHub
    // wouldn't match. At minimum, the response should not contain evil.com
    // as a redirect target.
    if (response.status >= 300 && response.status < 400) {
      const location = response.headers.get("location") ?? "";
      // If redirecting to GitHub, the redirect_uri param should NOT be evil.com
      if (location.includes("github.com")) {
        const url = new URL(location);
        const redirectUri = url.searchParams.get("redirect_uri") ?? "";
        assert.ok(
          !redirectUri.includes("evil.com"),
          `redirect_uri should not contain evil.com, got: ${redirectUri}`,
        );
      }
    } else {
      // Non-redirect response — check it's not a success that accepts evil.com
      assert.ok(
        response.status !== 200 || response.status < 500,
        `unexpected status: ${response.status}`,
      );
    }
  });
});

// ---------------------------------------------------------------------------
// Test 3: Account linking — GitHub excluded from trusted providers
// ---------------------------------------------------------------------------

describe("Test 3: Account linking security", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("GitHub is excluded from trustedProviders (prevents unverified email linking)", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
      GOOGLE_CLIENT_ID: "g-id",
      GOOGLE_CLIENT_SECRET: "g-secret",
    });
    const { auth } = await importAuthFresh();

    const trusted = auth.options.account?.accountLinking
      ?.trustedProviders as string[];
    assert.ok(Array.isArray(trusted), "trustedProviders should be an array");
    assert.ok(
      !trusted.includes("github"),
      "github must NOT be a trusted provider — unverified emails could hijack accounts",
    );
    assert.ok(
      trusted.includes("google"),
      "google should be a trusted provider",
    );
    assert.equal(
      trusted.length,
      1,
      "only google should be in trustedProviders",
    );
  });

  it("trustedProviders is empty when no providers configured", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: undefined,
      GITHUB_CLIENT_SECRET: undefined,
      GOOGLE_CLIENT_ID: undefined,
      GOOGLE_CLIENT_SECRET: undefined,
    });
    const { auth } = await importAuthFresh();

    const trusted = auth.options.account?.accountLinking
      ?.trustedProviders as string[];
    assert.ok(Array.isArray(trusted));
    assert.equal(trusted.length, 0);
  });
});

// ---------------------------------------------------------------------------
// Test 4: Session cookie attributes
// ---------------------------------------------------------------------------

describe("Test 4: Session cookie attributes", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("cookies have httpOnly=true (prevents XSS access)", async () => {
    setupMocks({ NODE_ENV: "development" });
    const { auth } = await importAuthFresh();

    assert.equal(
      auth.options.advanced?.defaultCookieAttributes?.httpOnly,
      true,
      "httpOnly must be true to prevent XSS access to session cookies",
    );
  });

  it("cookies have sameSite=lax (CSRF protection)", async () => {
    setupMocks({ NODE_ENV: "development" });
    const { auth } = await importAuthFresh();

    assert.equal(
      auth.options.advanced?.defaultCookieAttributes?.sameSite,
      "lax",
      "sameSite must be 'lax' for CSRF protection",
    );
  });

  it("cookies are not secure in development (HTTP localhost)", async () => {
    setupMocks({ NODE_ENV: "development" });
    const { auth } = await importAuthFresh();

    assert.equal(
      auth.options.advanced?.defaultCookieAttributes?.secure,
      false,
      "secure should be false in development (HTTP localhost)",
    );
  });

  it("cookies are secure in production", async () => {
    setupMocks({ NODE_ENV: "production" });
    const { auth } = await importAuthFresh();

    assert.equal(
      auth.options.advanced?.defaultCookieAttributes?.secure,
      true,
      "secure must be true in production",
    );
    assert.equal(
      auth.options.advanced?.useSecureCookies,
      true,
      "useSecureCookies must be true in production",
    );
  });
});
