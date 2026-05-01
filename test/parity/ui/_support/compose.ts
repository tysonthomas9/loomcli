/**
 * Compose-tool selection.
 *
 * Several support modules need to shell out to `<compose> -f
 * test/parity/docker-compose.parity.yml ...` (reseed, log scraping,
 * exec-redis-cli for routing proofs). The choice of compose runtime
 * varies by host:
 *   - macOS/Windows dev boxes ship with Docker Desktop → `docker compose`
 *   - many Linux setups (and this repo's standard env) use podman →
 *     `podman compose` via podman-compose
 *
 * We probe both at module load and cache the result so callers don't
 * pay the spawn cost per query. PARITY_COMPOSE=docker|podman lets the
 * user override.
 */
import { execSync } from "node:child_process";

let cached: string | null = null;

function probe(): string {
    const override = process.env.PARITY_COMPOSE?.toLowerCase().trim();
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

/** docker-compose `name:` field, derived from `-p` flag at parity-stack boot. */
export const COMPOSE_PROJECT = "loomcli-parity";

/** Project network — generated as `${COMPOSE_PROJECT}_${networks[0]}`. */
export const PARITY_NETWORK = `${COMPOSE_PROJECT}_parity`;

/** Pre-built parity-seed image, tagged as `${COMPOSE_PROJECT}_parity-seed`. */
export const PARITY_SEED_IMAGE = `${COMPOSE_PROJECT}_parity-seed`;

/** Pre-built fleet-only parity seeder image. */
export const PARITY_SEED_FLEET_IMAGE = `${COMPOSE_PROJECT}_parity-seed-fleet`;

/** Container-name prefix all parity services share. */
export const PARITY_CONTAINER_PREFIX = `${COMPOSE_PROJECT}_`;

/**
 * Build a full compose subcommand string. Concatenated as a string (not
 * args) because the existing call sites hand the result straight to
 * execSync without shell-quoting concerns; the file path is fixed.
 */
export function composeRun(rest: string): string {
    return `${composeCmd()} -f test/parity/docker-compose.parity.yml ${rest}`;
}
