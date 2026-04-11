/**
 * Hook barrel exports for the beads-web-ui frontend.
 *
 * Re-aggregates all domain sub-barrels so the existing
 * `import { ... } from "@/hooks"` form keeps working unchanged.
 */

export * from "./agents";
export * from "./common";
export * from "./issues";
export * from "./terminal";
export * from "./ui";
export * from "./workspace";

// API re-exports so components can reach data-fetching functions through
// the hooks layer instead of importing from @/api directly (Phase 7 DAG).
export * from "./api";
