import { migrate as migrateSqlite } from "drizzle-orm/better-sqlite3/migrator";
import { migrate as migratePg } from "drizzle-orm/node-postgres/migrator";
import type { BetterSQLite3Database } from "drizzle-orm/better-sqlite3";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import type { Dialect, DbHandle } from "./index.js";

export async function runMigrations(
  dialect: Dialect,
  db: DbHandle["db"],
): Promise<void> {
  if (dialect === "sqlite") {
    migrateSqlite(db as unknown as BetterSQLite3Database, {
      migrationsFolder: "drizzle-sqlite",
    });
  } else {
    await migratePg(db as unknown as NodePgDatabase, {
      migrationsFolder: "drizzle",
    });
  }
}
