import { betterAuth } from "better-auth";
import { drizzleAdapter } from "better-auth/adapters/drizzle";
import { jwt } from "better-auth/plugins";
import { APIError } from "better-auth/api";
import { randomUUID } from "node:crypto";
import { createDb } from "./db/index.js";
import { env } from "./lib/env.js";

const { db, dialect, close: closeDb } = createDb();

type SocialProviderConfig = Record<string, unknown>;
const socialProviders: Record<string, SocialProviderConfig> = {};

if (env.GITHUB_CLIENT_ID) {
  socialProviders.github = {
    clientId: env.GITHUB_CLIENT_ID,
    clientSecret: env.GITHUB_CLIENT_SECRET!,
    scope: ["user:email"],
    // SECURITY: Forward GitHub's email verification status to Better Auth.
    // Better Auth's GitHub provider may pass email_verified or emailVerified
    // depending on version. Only override when explicitly present — if the
    // field is absent, let Better Auth's default behavior apply rather than
    // hard-coding false (which would block all GitHub signups).
    mapProfileToUser: (profile: Record<string, unknown>) => {
      const verified = profile.email_verified ?? profile.emailVerified;
      if (typeof verified === "boolean") {
        return { emailVerified: verified };
      }
      return {};
    },
  };
}

if (env.GOOGLE_CLIENT_ID) {
  socialProviders.google = {
    clientId: env.GOOGLE_CLIENT_ID,
    clientSecret: env.GOOGLE_CLIENT_SECRET!,
    accessType: "offline",
    prompt: "select_account consent",
  };
}

// SECURITY: Only Google is trusted for auto-linking.
// GitHub is intentionally excluded — trustedProviders does NOT check
// the email verified field, so an attacker with an unverified GitHub
// email could hijack an existing account.
const trustedProviders: string[] = [];
if (env.GOOGLE_CLIENT_ID) trustedProviders.push("google");

export const auth = betterAuth({
  baseURL: env.BETTER_AUTH_URL,
  secret: env.BETTER_AUTH_SECRET,

  database: drizzleAdapter(db, {
    provider: dialect === "sqlite" ? "sqlite" : "pg",
  }),

  emailAndPassword: {
    enabled: true,
    minPasswordLength: 8,
    maxPasswordLength: 128,
  },

  socialProviders,

  account: {
    accountLinking: {
      enabled: true,
      trustedProviders,
    },
  },

  session: {
    expiresIn: 60 * 60 * 24 * 30, // 30 days
    updateAge: 60 * 60 * 24, // 1 day
    cookieCache: {
      enabled: true,
      maxAge: 60, // 60 seconds
    },
  },

  advanced: {
    defaultCookieAttributes: {
      httpOnly: true,
      secure: env.NODE_ENV === "production",
      sameSite: "lax" as const,
    },
    useSecureCookies: env.NODE_ENV === "production",
  },

  rateLimit: {
    enabled: true,
    window: 60, // 60 seconds
    max: 20, // 20 requests per window per IP
  },

  // parseEnv already defaults TRUSTED_ORIGINS to [BETTER_AUTH_URL origin]
  trustedOrigins: env.TRUSTED_ORIGINS,

  databaseHooks: {
    user: {
      create: {
        before: async (user) => {
          const email = user.email as string;

          // SECURITY: Email domain restriction.
          if (env.ALLOWED_EMAIL_DOMAINS.length > 0) {
            const domain = email.split("@")[1]?.toLowerCase();
            if (
              !domain ||
              !env.ALLOWED_EMAIL_DOMAINS.some(
                (d) => d.toLowerCase() === domain,
              )
            ) {
              throw new APIError("FORBIDDEN", {
                message: `Registration is restricted to the following email domains: ${env.ALLOWED_EMAIL_DOMAINS.join(", ")}`,
              });
            }
          }

          // SECURITY: Defense-in-depth — reject unverified emails from
          // OAuth providers. Email/password signups start unverified by
          // design (no OAuth provider to vouch), so only block when an
          // OAuth provider explicitly reports email_verified=false.
          const isOAuthSignup = "providerId" in user || "accountId" in user;
          if (isOAuthSignup && user.emailVerified === false) {
            throw new APIError("FORBIDDEN", {
              message:
                "Email address must be verified by the OAuth provider before account creation is allowed.",
            });
          }

          return { data: user };
        },
      },
    },
  },

  plugins: [
    jwt({
      jwks: {
        keyPairConfig: {
          alg: "RS256",
          modulusLength: 4096,
        },
        rotationInterval: 60 * 60 * 24 * 7, // 7 days
        gracePeriod: 60 * 60 * 24, // 24 hours
      },
      jwt: {
        expirationTime: "15m",
        issuer: new URL(env.BETTER_AUTH_URL).origin,
        audience: env.JWT_AUDIENCE,
        definePayload: ({ user }) => ({
          email: user.email,
          name: user.name,
          role: (user as Record<string, unknown>).role ?? "user",
          jti: randomUUID(),
        }),
      },
      disableSettingJwtHeader: true,
    }),
  ],
});

export type AuthInstance = typeof auth;
export { db, closeDb, dialect };
