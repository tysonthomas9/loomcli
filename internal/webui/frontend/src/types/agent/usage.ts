/**
 * TypeScript types for usage/cost tracking data.
 * Aliased from generated OpenAPI schemas: UsageSessionRecord, UsageAgentSummary,
 * UsageBackendSummary, UsageDailyCost, UsageResponse.
 * Drift: generated UsageSessionRecord uses session_id (optional) vs hand-written UsageSession which had no session_id.
 * UsageParams kept hand-written (query params type, not a schema).
 */

import type { components } from "@/types/generated/openapi";

/** A single agent session's usage record. Aliased from generated UsageSessionRecord schema. */
export type UsageSession = components["schemas"]["UsageSessionRecord"];

/** Per-agent aggregated usage. */
export type UsageAgentSummary = components["schemas"]["UsageAgentSummary"];

/** Per-backend aggregated usage. */
export type UsageBackendSummary = components["schemas"]["UsageBackendSummary"];

/** Daily cost entry. */
export type UsageDailyCost = components["schemas"]["UsageDailyCost"];

/** Full usage API response from GET /api/usage. */
export type UsageResponse = components["schemas"]["UsageResponse"];

/** Query parameters for the usage endpoint. */
export interface UsageParams {
  agent?: string;
  backend?: string;
  epic?: string;
  since?: string;
  until?: string;
}
