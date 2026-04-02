import { env } from "./env.js";

type LogLevel = "debug" | "info" | "warn" | "error";

const LEVEL_ORDER: Record<LogLevel, number> = {
  debug: 0,
  info: 1,
  warn: 2,
  error: 3,
};

const minLevel = LEVEL_ORDER[env.LOG_LEVEL as LogLevel] ?? LEVEL_ORDER.info;

function log(
  level: LogLevel,
  message: string,
  data?: Record<string, unknown>,
): void {
  if (LEVEL_ORDER[level] < minLevel) return;
  const entry: Record<string, unknown> = {
    ts: new Date().toISOString(),
    level,
    msg: message,
    ...data,
  };
  const line = JSON.stringify(entry);
  if (level === "error") {
    process.stderr.write(line + "\n");
  } else {
    process.stdout.write(line + "\n");
  }
}

export const logger = {
  debug: (msg: string, data?: Record<string, unknown>) => log("debug", msg, data),
  info: (msg: string, data?: Record<string, unknown>) => log("info", msg, data),
  warn: (msg: string, data?: Record<string, unknown>) => log("warn", msg, data),
  error: (msg: string, data?: Record<string, unknown>) => log("error", msg, data),
};
