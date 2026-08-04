/**
 * Compose-tool selection.
 *
 * Several support modules need to shell out to `<compose> -f
 * test/fleetdb/docker-compose.regression.yml ...` (reseed, log scraping,
 * exec-redis-cli for routing proofs). The choice of compose runtime
 * varies by host:
 *   - macOS/Windows dev boxes ship with Docker Desktop → `docker compose`
 *   - many Linux setups (and this repo's standard env) use podman →
 *     `podman compose` via podman-compose
 *
 * We probe both at module load and cache the result so callers don't
 * pay the spawn cost per query. FLEETDB_COMPOSE=docker|podman lets the
 * user override.
 */
import { execSync } from "node:child_process";

let cached: string | null = null;

function probe(): string {
  const override = process.env.FLEETDB_COMPOSE?.toLowerCase().trim();
  if (override === "docker" || override === "podman") {
    return `${override} compose`;
  }
  // Try podman first (this repo's default) — falls through to docker on
  // hosts where podman isn't installed.
  for (const tool of ["podman", "docker"]) {
    try {
      execSync(`${tool} compose version`, {
        stdio: "ignore",
        timeout: 3000,
      });
      return `${tool} compose`;
    } catch {
      // try next
    }
  }
  // Last resort: `docker compose` even if probing failed — let the
  // caller surface a clean error rather than swallow an EnoEnt here.
  return "docker compose";
}

export function composeCmd(): string {
  if (cached === null) cached = probe();
  return cached;
}

export type Runtime = "podman" | "docker";

/** Container runtime ("podman" | "docker"), independent of compose plugin. */
export function composeRuntime(): Runtime {
  return composeCmd().split(/\s+/)[0] as Runtime;
}

/** docker-compose `name:` field, derived from `-p` flag at fleetdb-regression-stack boot. */
export const COMPOSE_PROJECT = "loomcli-fleetdb-regression";

/** Project network — generated as `${COMPOSE_PROJECT}_${networks[0]}`. */
export const FLEETDB_NETWORK = `${COMPOSE_PROJECT}_fleetdb-regression`;

/** Pre-built fleetdb-regression-seed image, tagged as `${COMPOSE_PROJECT}_fleetdb-regression-seed`. */
export const FLEETDB_SEED_IMAGE = `${COMPOSE_PROJECT}_fleetdb-regression-seed`;

/** Pre-built fleet-only fleetdb-regression seeder image. */
export const FLEETDB_SEED_FLEET_IMAGE = `${COMPOSE_PROJECT}_fleetdb-regression-seed-fleet`;

/** Container-name prefix all fleetdb-regression services share. */
export const FLEETDB_CONTAINER_PREFIX = `${COMPOSE_PROJECT}-`;

/**
 * Build a full compose subcommand string. Concatenated as a string (not
 * args) because the existing call sites hand the result straight to
 * execSync without shell-quoting concerns; the file path is fixed.
 */
export function composeRun(rest: string): string {
  return `${composeCmd()} -f test/fleetdb/docker-compose.regression.yml ${rest}`;
}
