import Database from "better-sqlite3";
import { drizzle as drizzleSqlite } from "drizzle-orm/better-sqlite3";
import { drizzle as drizzlePg } from "drizzle-orm/node-postgres";
import pg from "pg";
import { env } from "../lib/env.js";

export type Dialect = "sqlite" | "pg";

export interface DbHandle {
  db: ReturnType<typeof drizzleSqlite> | ReturnType<typeof drizzlePg>;
  dialect: Dialect;
  close: () => Promise<void>;
}

export function createDb(): DbHandle {
  const dialect = env.DATABASE_PROVIDER as Dialect;

  if (dialect === "sqlite") {
    const sqlite = new Database(env.DATABASE_URL);
    sqlite.pragma("journal_mode = WAL");
    sqlite.pragma("foreign_keys = ON");
    const db = drizzleSqlite(sqlite);
    return {
      db,
      dialect,
      close: async () => {
        sqlite.close();
      },
    };
  }

  const pool = new pg.Pool({ connectionString: env.DATABASE_URL });
  const db = drizzlePg(pool);
  return {
    db,
    dialect,
    close: async () => {
      await pool.end();
    },
  };
}
