import { describe, it, mock, afterEach } from "node:test";
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
let importCounter = 0;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Build a valid env object for mocking, with optional overrides. */
function makeEnv(overrides: Record<string, unknown> = {}) {
  return {
    BETTER_AUTH_SECRET: VALID_SECRET,
    BETTER_AUTH_URL: "http://localhost:3001",
    DATABASE_URL: "file:test.db",
    DATABASE_PROVIDER: "sqlite",
    NODE_ENV: "development" as const,
    TRUSTED_ORIGINS: [] as string[],
    ALLOWED_EMAIL_DOMAINS: [] as string[],
    ADMIN_USER_IDS: [] as string[],
    JWT_AUDIENCE: "loom",
    PORT: 3001,
    LOG_LEVEL: "info" as const,
    ...overrides,
  };
}

/**
 * Register mocks for the env and db modules that auth.ts imports, then
 * return the env data object so tests can assert against it if needed.
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
 * Dynamically import auth.ts with a cache-busting query string so that
 * the module is re-evaluated against the current mocks.
 */
async function importAuthFresh() {
  importCounter++;
  return await import(path.join(AUTH_SRC, `auth.ts?v=${importCounter}`));
}

// ---------------------------------------------------------------------------
// Tests: Social provider conditional configuration
// ---------------------------------------------------------------------------

describe("social provider configuration", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("registers no social providers when no OAuth env vars are set", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    const providers = auth.options.socialProviders ?? {};
    assert.deepEqual(Object.keys(providers), []);
  });

  it("registers only github when only GITHUB vars are set", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    const providers = auth.options.socialProviders ?? {};
    assert.ok("github" in providers, "github should be registered");
    assert.ok(!("google" in providers), "google should NOT be registered");
  });

  it("registers only google when only GOOGLE vars are set", async () => {
    setupMocks({
      GOOGLE_CLIENT_ID: "g-id",
      GOOGLE_CLIENT_SECRET: "g-secret",
    });
    const { auth } = await importAuthFresh();
    const providers = auth.options.socialProviders ?? {};
    assert.ok("google" in providers, "google should be registered");
    assert.ok(!("github" in providers), "github should NOT be registered");
  });

  it("registers both providers when both OAuth var pairs are set", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
      GOOGLE_CLIENT_ID: "g-id",
      GOOGLE_CLIENT_SECRET: "g-secret",
    });
    const { auth } = await importAuthFresh();
    const providers = auth.options.socialProviders ?? {};
    assert.ok("github" in providers, "github should be registered");
    assert.ok("google" in providers, "google should be registered");
  });

  it("passes correct clientId/clientSecret/scope for github", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "my-gh-id",
      GITHUB_CLIENT_SECRET: "my-gh-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const gh = auth.options.socialProviders?.github as any;
    assert.ok(gh, "github config should exist");
    assert.equal(gh.clientId, "my-gh-id");
    assert.equal(gh.clientSecret, "my-gh-secret");
    assert.deepEqual(gh.scope, ["user:email"]);
  });

  it("passes correct clientId/clientSecret/accessType/prompt for google", async () => {
    setupMocks({
      GOOGLE_CLIENT_ID: "my-g-id",
      GOOGLE_CLIENT_SECRET: "my-g-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const g = auth.options.socialProviders?.google as any;
    assert.ok(g, "google config should exist");
    assert.equal(g.clientId, "my-g-id");
    assert.equal(g.clientSecret, "my-g-secret");
    assert.equal(g.accessType, "offline");
    assert.equal(g.prompt, "select_account consent");
  });
});

// ---------------------------------------------------------------------------
// Tests: trustedProviders
// ---------------------------------------------------------------------------

describe("trustedProviders for account linking", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("includes google when GOOGLE vars are set", async () => {
    setupMocks({
      GOOGLE_CLIENT_ID: "g-id",
      GOOGLE_CLIENT_SECRET: "g-secret",
    });
    const { auth } = await importAuthFresh();
    const trusted = auth.options.account?.accountLinking?.trustedProviders as string[];
    assert.ok(Array.isArray(trusted), "trustedProviders should be an array");
    assert.ok(trusted.includes("google"), "google should be a trusted provider");
  });

  it("never includes github even when GITHUB vars are set", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    const trusted = auth.options.account?.accountLinking?.trustedProviders as string[];
    assert.ok(Array.isArray(trusted), "trustedProviders should be an array");
    assert.ok(!trusted.includes("github"), "github must NOT be a trusted provider");
  });

  it("never includes github even when both providers are set", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
      GOOGLE_CLIENT_ID: "g-id",
      GOOGLE_CLIENT_SECRET: "g-secret",
    });
    const { auth } = await importAuthFresh();
    const trusted = auth.options.account?.accountLinking?.trustedProviders as string[];
    assert.ok(trusted.includes("google"), "google should be trusted");
    assert.ok(!trusted.includes("github"), "github must NOT be trusted");
    assert.equal(trusted.length, 1, "only google should be in trustedProviders");
  });

  it("is empty when no OAuth providers are configured", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    const trusted = auth.options.account?.accountLinking?.trustedProviders as string[];
    assert.ok(Array.isArray(trusted), "trustedProviders should be an array");
    assert.equal(trusted.length, 0, "trustedProviders should be empty when no providers");
  });
});

// ---------------------------------------------------------------------------
// Tests: Session configuration
// ---------------------------------------------------------------------------

describe("session configuration", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("sets expiresIn to 30 days in seconds", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    const thirtyDays = 60 * 60 * 24 * 30;
    assert.equal(auth.options.session?.expiresIn, thirtyDays);
  });

  it("sets updateAge to 1 day in seconds", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    const oneDay = 60 * 60 * 24;
    assert.equal(auth.options.session?.updateAge, oneDay);
  });

  it("enables cookie cache with 60-second maxAge", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    const cookieCache = auth.options.session?.cookieCache;
    assert.ok(cookieCache, "cookieCache should be defined");
    assert.equal(cookieCache.enabled, true);
    assert.equal(cookieCache.maxAge, 60);
  });
});

// ---------------------------------------------------------------------------
// Tests: Cookie attributes (development vs production)
// ---------------------------------------------------------------------------

describe("cookie attributes", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("httpOnly is always true", async () => {
    setupMocks({ NODE_ENV: "development" });
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.advanced?.defaultCookieAttributes?.httpOnly, true);
  });

  it("sameSite is always lax", async () => {
    setupMocks({ NODE_ENV: "development" });
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.advanced?.defaultCookieAttributes?.sameSite, "lax");
  });

  it("secure is false in development", async () => {
    setupMocks({ NODE_ENV: "development" });
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.advanced?.defaultCookieAttributes?.secure, false);
    assert.equal(auth.options.advanced?.useSecureCookies, false);
  });

  it("secure is true in production", async () => {
    setupMocks({ NODE_ENV: "production" });
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.advanced?.defaultCookieAttributes?.secure, true);
    assert.equal(auth.options.advanced?.useSecureCookies, true);
  });
});

// ---------------------------------------------------------------------------
// Tests: Rate limiting
// ---------------------------------------------------------------------------

describe("rate limit configuration", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("rate limiting is enabled with 20 req / 60s window", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.rateLimit?.enabled, true);
    assert.equal(auth.options.rateLimit?.window, 60);
    assert.equal(auth.options.rateLimit?.max, 20);
  });
});

// ---------------------------------------------------------------------------
// Tests: trustedOrigins
// ---------------------------------------------------------------------------

describe("trustedOrigins", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("uses TRUSTED_ORIGINS from env when set", async () => {
    setupMocks({
      TRUSTED_ORIGINS: ["https://app.example.com", "https://admin.example.com"],
    });
    const { auth } = await importAuthFresh();
    assert.deepEqual(auth.options.trustedOrigins, [
      "https://app.example.com",
      "https://admin.example.com",
    ]);
  });

  it("passes env.TRUSTED_ORIGINS directly to betterAuth (fallback handled by parseEnv)", async () => {
    // When TRUSTED_ORIGINS is empty, auth.ts passes it as-is. The fallback
    // to BETTER_AUTH_URL origin is handled by parseEnv in env-schema.ts,
    // not by auth.ts. Here we verify auth.ts does not apply its own fallback.
    setupMocks({
      BETTER_AUTH_URL: "https://auth.example.com:8443/api/auth",
      TRUSTED_ORIGINS: [],
    });
    const { auth } = await importAuthFresh();
    assert.deepEqual(auth.options.trustedOrigins, []);
  });
});

// ---------------------------------------------------------------------------
// Tests: databaseHooks — email domain restriction
// ---------------------------------------------------------------------------

describe("databaseHooks: email domain restriction", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("rejects users with email domain not in ALLOWED_EMAIL_DOMAINS", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: ["example.com", "corp.io"] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;
    assert.ok(hook, "user.create.before hook should exist");

    await assert.rejects(
      () => hook({ email: "attacker@evil.com", emailVerified: true }),
      (err: Error & { body?: { message?: string } }) => {
        const msg = err.body?.message ?? err.message;
        assert.ok(msg.includes("restricted"), `expected 'restricted' in: ${msg}`);
        assert.ok(msg.includes("example.com"), `expected domain list in: ${msg}`);
        return true;
      },
    );
  });

  it("rejects users with email that has no domain part", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: ["example.com"] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;

    await assert.rejects(
      () => hook({ email: "nodomain", emailVerified: true }),
      (err: Error & { body?: { message?: string } }) => {
        const msg = err.body?.message ?? err.message;
        assert.ok(msg.includes("restricted"), `expected 'restricted' in: ${msg}`);
        return true;
      },
    );
  });

  it("domain check is case-insensitive", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: ["Example.COM"] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;

    // Should succeed — domain comparison lowercases both sides
    const result = await hook({ email: "user@example.com", emailVerified: true });
    assert.ok(result?.data, "should return { data: user }");
    assert.equal(result.data.email, "user@example.com");
  });

  it("allows any domain when ALLOWED_EMAIL_DOMAINS is empty", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: [] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;

    const result = await hook({ email: "user@anything.org", emailVerified: true });
    assert.ok(result?.data, "should return { data: user }");
  });

  it("allows users with email domain in ALLOWED_EMAIL_DOMAINS", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: ["example.com", "corp.io"] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;

    const result = await hook({ email: "alice@corp.io", emailVerified: true });
    assert.ok(result?.data, "should return { data: user }");
    assert.equal(result.data.email, "alice@corp.io");
  });
});

// ---------------------------------------------------------------------------
// Tests: databaseHooks — unverified email rejection
// ---------------------------------------------------------------------------

describe("databaseHooks: unverified email rejection", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("rejects user with emailVerified === false", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: [] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;
    assert.ok(hook, "user.create.before hook should exist");

    await assert.rejects(
      () => hook({ email: "user@example.com", emailVerified: false }),
      (err: Error & { body?: { message?: string } }) => {
        const msg = err.body?.message ?? err.message;
        assert.ok(
          msg.includes("verified"),
          `expected 'verified' in error: ${msg}`,
        );
        return true;
      },
    );
  });

  it("allows user with emailVerified === true", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: [] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;

    const result = await hook({ email: "user@example.com", emailVerified: true });
    assert.ok(result?.data, "should return { data: user }");
  });

  it("allows user when emailVerified is undefined (not explicitly false)", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: [] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;

    // emailVerified undefined !== false, so it should pass
    const result = await hook({ email: "user@example.com" });
    assert.ok(result?.data, "should return { data: user }");
  });

  it("domain check runs before emailVerified check (disallowed domain + unverified)", async () => {
    setupMocks({ ALLOWED_EMAIL_DOMAINS: ["example.com"] });
    const { auth } = await importAuthFresh();
    const hook = auth.options.databaseHooks?.user?.create?.before;

    await assert.rejects(
      () => hook({ email: "user@evil.com", emailVerified: false }),
      (err: Error & { body?: { message?: string } }) => {
        const msg = err.body?.message ?? err.message;
        // The domain check fires first, so the error should be about domain restriction
        assert.ok(
          msg.includes("restricted"),
          `expected domain restriction error first, got: ${msg}`,
        );
        return true;
      },
    );
  });
});

// ---------------------------------------------------------------------------
// Tests: GitHub mapProfileToUser
// ---------------------------------------------------------------------------

describe("github mapProfileToUser", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("sets emailVerified to true when profile.email_verified is true", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const mapFn = (auth.options.socialProviders?.github as any)?.mapProfileToUser;
    assert.ok(mapFn, "mapProfileToUser should exist on github config");

    const result = mapFn({ email_verified: true });
    assert.equal(result.emailVerified, true);
  });

  it("sets emailVerified to false when profile.email_verified is false", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const mapFn = (auth.options.socialProviders?.github as any)?.mapProfileToUser;

    const result = mapFn({ email_verified: false });
    assert.equal(result.emailVerified, false);
  });

  it("returns empty object when email_verified is absent (lets Better Auth defaults apply)", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const mapFn = (auth.options.socialProviders?.github as any)?.mapProfileToUser;

    const result = mapFn({});
    assert.deepEqual(result, {}, "should return empty object when email_verified is absent");
  });

  it("reads emailVerified (camelCase) when email_verified (snake_case) is absent", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const mapFn = (auth.options.socialProviders?.github as any)?.mapProfileToUser;

    const result = mapFn({ emailVerified: true });
    assert.equal(result.emailVerified, true);
  });

  it("prefers email_verified (snake_case) over emailVerified (camelCase)", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const mapFn = (auth.options.socialProviders?.github as any)?.mapProfileToUser;

    // snake_case wins via ?? (nullish coalescing) since it is checked first
    const result = mapFn({ email_verified: false, emailVerified: true });
    assert.equal(result.emailVerified, false);
  });

  it("ignores non-boolean email_verified values", async () => {
    setupMocks({
      GITHUB_CLIENT_ID: "gh-id",
      GITHUB_CLIENT_SECRET: "gh-secret",
    });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const mapFn = (auth.options.socialProviders?.github as any)?.mapProfileToUser;

    // String "true" is not a boolean, should return empty object
    const result = mapFn({ email_verified: "true" });
    assert.deepEqual(result, {}, "should ignore non-boolean email_verified");
  });
});

// ---------------------------------------------------------------------------
// Tests: Exports
// ---------------------------------------------------------------------------

describe("module exports", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("exports auth, db, closeDb, and dialect", async () => {
    setupMocks();
    const mod = await importAuthFresh();
    assert.ok(mod.auth, "auth should be exported");
    assert.ok("db" in mod, "db should be exported");
    assert.ok("closeDb" in mod, "closeDb should be exported");
    assert.ok("dialect" in mod, "dialect should be exported");
  });

  it("dialect is 'sqlite' when DATABASE_PROVIDER is sqlite", async () => {
    setupMocks({ DATABASE_PROVIDER: "sqlite" });
    const mod = await importAuthFresh();
    assert.equal(mod.dialect, "sqlite");
  });

  it("plugins array contains JWT plugin", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    assert.ok(Array.isArray(auth.options.plugins), "plugins should be an array");
    assert.equal(auth.options.plugins.length, 1, "plugins should have JWT plugin");
  });

  it("account linking is enabled", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.account?.accountLinking?.enabled, true);
  });

  it("baseURL matches BETTER_AUTH_URL", async () => {
    setupMocks({ BETTER_AUTH_URL: "https://auth.example.com" });
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.baseURL, "https://auth.example.com");
  });

  it("secret matches BETTER_AUTH_SECRET", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    assert.equal(auth.options.secret, VALID_SECRET);
  });
});

// ---------------------------------------------------------------------------
// Tests: JWT plugin configuration
// ---------------------------------------------------------------------------

describe("JWT plugin configuration", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  it("plugins array contains exactly one plugin (jwt)", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    assert.ok(Array.isArray(auth.options.plugins), "plugins should be an array");
    assert.equal(auth.options.plugins.length, 1, "should have exactly one plugin");
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins[0] as any;
    assert.equal(plugin.id, "jwt", "the single plugin should be jwt");
  });

  it("JWT plugin configures RS256 algorithm with 4096 modulusLength", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    const keyPairConfig = plugin.options.jwks.keyPairConfig;
    assert.equal(keyPairConfig.alg, "RS256");
    assert.equal(keyPairConfig.modulusLength, 4096);
  });

  it("JWT plugin sets 15-minute expiration time", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    assert.equal(plugin.options.jwt.expirationTime, "15m");
  });

  it("JWT plugin sets issuer from BETTER_AUTH_URL origin", async () => {
    setupMocks({ BETTER_AUTH_URL: "https://auth.example.com:8443/api/auth" });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    assert.equal(plugin.options.jwt.issuer, "https://auth.example.com:8443");
  });

  it("JWT plugin sets audience from JWT_AUDIENCE env", async () => {
    setupMocks({ JWT_AUDIENCE: "my-custom-audience" });
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    assert.equal(plugin.options.jwt.audience, "my-custom-audience");
  });

  it("JWT plugin has disableSettingJwtHeader true", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    assert.equal(plugin.options.disableSettingJwtHeader, true);
  });

  it("JWT plugin configures 7-day rotation interval", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    const sevenDays = 60 * 60 * 24 * 7;
    assert.equal(plugin.options.jwks.rotationInterval, sevenDays);
  });

  it("JWT plugin configures 24-hour grace period", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    const twentyFourHours = 60 * 60 * 24;
    assert.equal(plugin.options.jwks.gracePeriod, twentyFourHours);
  });

  it("definePayload includes email, name, role, and jti", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    const definePayload = plugin.options.jwt.definePayload;
    assert.ok(definePayload, "definePayload should be defined");

    const mockUser = {
      email: "alice@example.com",
      name: "Alice",
      role: "admin",
    };
    const payload = definePayload({ user: mockUser });
    assert.equal(payload.email, "alice@example.com");
    assert.equal(payload.name, "Alice");
    assert.equal(payload.role, "admin");
    assert.ok(typeof payload.jti === "string", "jti should be a string");
    assert.ok(payload.jti.length > 0, "jti should be non-empty");
  });

  it("definePayload defaults role to 'user' when absent", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    const definePayload = plugin.options.jwt.definePayload;

    const mockUser = { email: "bob@example.com", name: "Bob" };
    const payload = definePayload({ user: mockUser });
    assert.equal(payload.role, "user", "role should default to 'user'");
  });

  it("definePayload uses existing role when present", async () => {
    setupMocks();
    const { auth } = await importAuthFresh();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const plugin = auth.options.plugins![0] as any;
    const definePayload = plugin.options.jwt.definePayload;

    const mockUser = { email: "carol@example.com", name: "Carol", role: "admin" };
    const payload = definePayload({ user: mockUser });
    assert.equal(payload.role, "admin", "role should be 'admin' from user");
  });
});
