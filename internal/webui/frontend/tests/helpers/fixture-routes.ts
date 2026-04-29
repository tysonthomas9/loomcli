/**
 * Typed URL builder functions for test fixture routes.
 * Avoids hardcoded query strings in spec files.
 */

import type { Page } from "@playwright/test";

export function sessionNamePromptUrl(opts: {
  state: "open" | "closed";
  existingNames?: string[];
}): string {
  const params = new URLSearchParams();
  params.set("state", opts.state);
  if (opts.existingNames?.length) {
    params.set("existingNames", opts.existingNames.join(","));
  }
  return `/test/session-name-prompt?${params}`;
}

export function welcomeBannerUrl(opts: {
  backend: "claude" | "codex" | "opencode" | "unknown";
}): string {
  return `/test/welcome-banner?backend=${opts.backend}`;
}

export function helpPopoverUrl(): string {
  return "/test/help-popover";
}

export function searchBarUrl(opts?: {
  value?: string;
  matchIndex?: number;
  matchCount?: number;
  caseSensitive?: boolean;
  regex?: boolean;
}): string {
  const params = new URLSearchParams();
  if (opts?.value !== undefined) params.set("value", opts.value);
  if (opts?.matchIndex !== undefined)
    params.set("matchIndex", String(opts.matchIndex));
  if (opts?.matchCount !== undefined)
    params.set("matchCount", String(opts.matchCount));
  if (opts?.caseSensitive) params.set("case", "true");
  if (opts?.regex) params.set("regex", "true");
  const qs = params.toString();
  return `/test/search-bar${qs ? `?${qs}` : ""}`;
}

export function workspaceTreeUrl(): string {
  return "/test/workspace-tree";
}

export function pasteConfirmUrl(): string {
  return "/test/paste-confirm";
}

export function splitDetailSummaryUrl(opts: {
  id?: string;
  title?: string;
  priority?: number;
  hasDesign?: boolean;
  description?: string;
  issueType?: string;
  assignee?: string;
}): string {
  const params = new URLSearchParams();
  if (opts.id) params.set("id", opts.id);
  if (opts.title) params.set("title", opts.title);
  if (opts.priority !== undefined) params.set("priority", String(opts.priority));
  if (opts.hasDesign !== undefined)
    params.set("hasDesign", String(opts.hasDesign));
  if (opts.description) params.set("description", opts.description);
  if (opts.issueType) params.set("issueType", opts.issueType);
  if (opts.assignee) params.set("assignee", opts.assignee);
  return `/test/split-detail-summary?${params}`;
}

export function issueDetailPanelUrl(opts: {
  id: string;
  title: string;
  status: string;
  priority: number;
  issueType?: string;
  description?: string;
}): string {
  const params = new URLSearchParams();
  params.set("id", opts.id);
  params.set("title", opts.title);
  params.set("status", opts.status);
  params.set("priority", String(opts.priority));
  if (opts.issueType) params.set("issue_type", opts.issueType);
  if (opts.description) params.set("description", opts.description);
  return `/test/issue-detail-panel?${params}`;
}

/**
 * Seed window.__fixtureData before navigating to a fixture route.
 * Uses addInitScript so the data persists across page.goto() navigations.
 * Must be called BEFORE page.goto().
 */
export async function seedFixtureData(
  page: Page,
  data: Record<string, unknown>,
): Promise<void> {
  await page.addInitScript((d: Record<string, unknown>) => {
    (window as unknown as { __fixtureData: Record<string, unknown> }).__fixtureData = d;
  }, data);
}
