/**
 * Durable interactive-agent browser identities (FleetDB-backed).
 * Aliased from generated OpenAPI schemas: Browser, BrowserList, BrowserErrorCode.
 */

import type { components } from "@/types/generated/openapi";

/** One durable browser identity. Carries no runtime or page data. */
export type AgentBrowser = components["schemas"]["Browser"];

/** GET /api/workspaces/{ws}/agents/{name}/browsers response. */
export type AgentBrowserList = components["schemas"]["BrowserList"];

/** Error codes the browser routes return in `{error, code}`. */
export type AgentBrowserErrorCode = components["schemas"]["BrowserErrorCode"];
