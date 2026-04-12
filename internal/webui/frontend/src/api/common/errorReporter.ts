/**
 * Frontend error reporter with circuit breaker and dedup.
 * Reports client-side errors to POST /api/client-errors.
 * Uses raw fetch() to avoid recursion through fetchApi.
 */

export type ErrorType =
  | "global-error"
  | "unhandled-rejection"
  | "react-error"
  | "api-error";

interface ErrorPayload {
  type: ErrorType;
  message: string;
  stack?: string;
  url?: string;
  line?: number;
  col?: number;
  userAgent?: string;
  timestamp?: string;
}

export interface ErrorExtra {
  stack?: string | undefined;
  url?: string | undefined;
  line?: number | undefined;
  col?: number | undefined;
  componentStack?: string | undefined;
}

// Constants
const CIRCUIT_BREAKER_THRESHOLD = 3;
const CIRCUIT_BREAKER_COOLDOWN = 60_000;
const DEDUP_WINDOW = 5_000;
const REPORT_TIMEOUT = 5_000;
const REPORT_ENDPOINT = "/api/client-errors";

// Module-level state
let consecutiveFailures = 0;
let circuitOpenUntil = 0;
const recentErrors = new Map<string, number>();

function cleanupDedupMap(): void {
  const now = Date.now();
  for (const [key, timestamp] of recentErrors) {
    if (now - timestamp > DEDUP_WINDOW) {
      recentErrors.delete(key);
    }
  }
}

function normalizeMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "string") return error;
  try {
    return String(error);
  } catch {
    return "Unknown error";
  }
}

async function sendReport(payload: ErrorPayload): Promise<void> {
  try {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), REPORT_TIMEOUT);

    try {
      const response = await fetch(REPORT_ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
        signal: controller.signal,
      });

      if (response.ok) {
        consecutiveFailures = 0;
      } else {
        consecutiveFailures++;
        if (consecutiveFailures >= CIRCUIT_BREAKER_THRESHOLD) {
          circuitOpenUntil = Date.now() + CIRCUIT_BREAKER_COOLDOWN;
        }
      }
    } finally {
      clearTimeout(timeoutId);
    }
  } catch {
    consecutiveFailures++;
    if (consecutiveFailures >= CIRCUIT_BREAKER_THRESHOLD) {
      circuitOpenUntil = Date.now() + CIRCUIT_BREAKER_COOLDOWN;
    }
  }
}

export function reportError(
  type: ErrorType,
  error: unknown,
  extra?: ErrorExtra,
): void {
  try {
    const message = normalizeMessage(error);

    // Dedup check
    cleanupDedupMap();
    const dedupKey = `${type}:${message}`;
    if (recentErrors.has(dedupKey)) return;

    // Circuit breaker check
    if (Date.now() < circuitOpenUntil) return;

    // Record for dedup
    recentErrors.set(dedupKey, Date.now());

    // Build payload
    const payload: ErrorPayload = {
      type,
      message,
      userAgent: navigator.userAgent,
      timestamp: new Date().toISOString(),
    };

    if (error instanceof Error && error.stack) {
      payload.stack = error.stack;
    } else if (extra?.stack) {
      payload.stack = extra.stack;
    }

    if (extra?.componentStack) {
      payload.stack =
        (payload.stack ?? "") + "\n\nComponent Stack:" + extra.componentStack;
    }

    if (extra?.url) payload.url = extra.url;
    if (extra?.line !== undefined) payload.line = extra.line;
    if (extra?.col !== undefined) payload.col = extra.col;

    // Fire-and-forget
    void sendReport(payload);
  } catch {
    // Error reporting must never throw
  }
}

export function initErrorReporter(): void {
  window.addEventListener("error", (event: ErrorEvent) => {
    reportError("global-error", event.error ?? event.message, {
      url: event.filename,
      line: event.lineno,
      col: event.colno,
    });
  });

  window.addEventListener(
    "unhandledrejection",
    (event: PromiseRejectionEvent) => {
      reportError("unhandled-rejection", event.reason);
    },
  );
}
