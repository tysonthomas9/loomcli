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

/**
 * Build a full compose subcommand string. Concatenated as a string (not
 * args) because the existing call sites hand the result straight to
 * execSync without shell-quoting concerns; the file path is fixed.
 */
export function composeRun(rest: string): string {
    return `${composeCmd()} -f test/parity/docker-compose.parity.yml ${rest}`;
}
