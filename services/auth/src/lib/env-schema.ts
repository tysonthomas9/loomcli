import { z } from "zod";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function splitCommaList(val: string | undefined): string[] {
  if (!val) return [];
  return val
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

const HEX_RE = /^[0-9a-fA-F]+$/;
const BASE64_RE =
  /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{4})$/;

function isValidHex(s: string): boolean {
  return HEX_RE.test(s);
}

function isValidBase64(s: string): boolean {
  return BASE64_RE.test(s);
}

function isLowEntropy(s: string): boolean {
  return s.length > 0 && new Set(s).size === 1;
}

// ---------------------------------------------------------------------------
// Port schema (reused for PORT and METRICS_PORT)
// ---------------------------------------------------------------------------

function portSchema(defaultVal: number) {
  return z.preprocess(
    (val) => (val === undefined || val === "" ? defaultVal : Number(val)),
    z.number().int().min(1).max(65535),
  );
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const envSchema = z
  .object({
    BETTER_AUTH_SECRET: z
      .string({ required_error: "Required" })
      .min(1, "Required")
      .superRefine((val, ctx) => {
        if (val.length < 32) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: "BETTER_AUTH_SECRET must be at least 32 characters",
          });
          return;
        }
        if (!isValidHex(val) && !isValidBase64(val)) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: "BETTER_AUTH_SECRET must be valid hex or base64",
          });
          return;
        }
        if (isLowEntropy(val)) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: "BETTER_AUTH_SECRET appears low-entropy (all identical characters)",
          });
        }
      }),

    BETTER_AUTH_URL: z.string({ required_error: "Required" }).min(1, "Required"),
    DATABASE_URL: z.string({ required_error: "Required" }).min(1, "Required"),
    DATABASE_PROVIDER: z.enum(["pg", "sqlite"], {
      errorMap: () => ({ message: "DATABASE_PROVIDER must be 'pg' or 'sqlite'" }),
    }),

    GITHUB_CLIENT_ID: z.string().optional(),
    GITHUB_CLIENT_SECRET: z.string().optional(),
    GOOGLE_CLIENT_ID: z.string().optional(),
    GOOGLE_CLIENT_SECRET: z.string().optional(),

    PORT: portSchema(3001),
    METRICS_PORT: portSchema(9090),

    JWT_AUDIENCE: z.string().default("loom"),
    NODE_ENV: z.string().default("development"),
    LOG_LEVEL: z.string().default("info"),

    EMAIL_PASSWORD_ENABLED: z.preprocess(
      (val) => val === "true" || val === "1",
      z.boolean().default(false),
    ),
    TRUSTED_ORIGINS: z.string().optional(),
    ALLOWED_EMAIL_DOMAINS: z.string().optional(),
    ADMIN_USER_IDS: z.string().optional(),
  })
  .superRefine((data, ctx) => {
    // Orphan OAuth pair checks
    if (data.GITHUB_CLIENT_ID && !data.GITHUB_CLIENT_SECRET) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "GITHUB_CLIENT_ID requires GITHUB_CLIENT_SECRET",
        path: ["GITHUB_CLIENT_ID"],
      });
    }
    if (data.GITHUB_CLIENT_SECRET && !data.GITHUB_CLIENT_ID) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "GITHUB_CLIENT_SECRET requires GITHUB_CLIENT_ID",
        path: ["GITHUB_CLIENT_SECRET"],
      });
    }
    if (data.GOOGLE_CLIENT_ID && !data.GOOGLE_CLIENT_SECRET) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "GOOGLE_CLIENT_ID requires GOOGLE_CLIENT_SECRET",
        path: ["GOOGLE_CLIENT_ID"],
      });
    }
    if (data.GOOGLE_CLIENT_SECRET && !data.GOOGLE_CLIENT_ID) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "GOOGLE_CLIENT_SECRET requires GOOGLE_CLIENT_ID",
        path: ["GOOGLE_CLIENT_SECRET"],
      });
    }
  })
  .transform((data) => {
    const trustedRaw = splitCommaList(data.TRUSTED_ORIGINS);
    const trustedOrigins =
      trustedRaw.length > 0
        ? trustedRaw
        : (() => {
            try {
              return [new URL(data.BETTER_AUTH_URL).origin];
            } catch {
              return [];
            }
          })();

    return {
      BETTER_AUTH_SECRET: data.BETTER_AUTH_SECRET,
      BETTER_AUTH_URL: data.BETTER_AUTH_URL,
      DATABASE_URL: data.DATABASE_URL,
      DATABASE_PROVIDER: data.DATABASE_PROVIDER,
      GITHUB_CLIENT_ID: data.GITHUB_CLIENT_ID,
      GITHUB_CLIENT_SECRET: data.GITHUB_CLIENT_SECRET,
      GOOGLE_CLIENT_ID: data.GOOGLE_CLIENT_ID,
      GOOGLE_CLIENT_SECRET: data.GOOGLE_CLIENT_SECRET,
      PORT: data.PORT,
      METRICS_PORT: data.METRICS_PORT,
      JWT_AUDIENCE: data.JWT_AUDIENCE,
      NODE_ENV: data.NODE_ENV,
      LOG_LEVEL: data.LOG_LEVEL,
      EMAIL_PASSWORD_ENABLED: data.EMAIL_PASSWORD_ENABLED,
      TRUSTED_ORIGINS: trustedOrigins,
      ALLOWED_EMAIL_DOMAINS: splitCommaList(data.ALLOWED_EMAIL_DOMAINS),
      ADMIN_USER_IDS: splitCommaList(data.ADMIN_USER_IDS),
    };
  });

// ---------------------------------------------------------------------------
// Exports
// ---------------------------------------------------------------------------

export type EnvConfig = z.infer<typeof envSchema>;

export function parseEnv(
  raw: Record<string, string | undefined>,
): z.SafeParseReturnType<unknown, EnvConfig> {
  return envSchema.safeParse(raw);
}
