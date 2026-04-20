/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for buildShareUrl utility.
 * URLs now use route segments for views (e.g. /ws/abc/table)
 * instead of ?view= query params.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

import { buildShareUrl } from "../buildShareUrl";

/**
 * Mock window.location with given URL parts.
 */
function mockLocation(href: string): void {
  const url = new URL(href);
  Object.defineProperty(window, "location", {
    value: {
      href: url.href,
      origin: url.origin,
      pathname: url.pathname,
      search: url.search,
      hash: url.hash,
      protocol: url.protocol,
      host: url.host,
      hostname: url.hostname,
      port: url.port,
    },
    writable: true,
    configurable: true,
  });
}

describe("buildShareUrl", () => {
  beforeEach(() => {
    mockLocation("http://localhost:3000/ws/test-ws/kanban");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns current URL when no params provided", () => {
    const result = buildShareUrl();
    expect(result).toBe("http://localhost:3000/ws/test-ws/kanban");
  });

  it("generates URL with view as path segment", () => {
    const result = buildShareUrl({ view: "table" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/table");
    expect(url.searchParams.has("view")).toBe(false);
  });

  it("generates panel URL with issue as path segment for table view", () => {
    const result = buildShareUrl({ view: "table", issue: "TASK-123" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/table/issues/TASK-123");
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("falls back to default view for issue-detail without an issue", () => {
    const result = buildShareUrl({ view: "issue-detail" });
    const url = new URL(result);
    // No issue id → never emit the invalid /ws/:id/issue-detail path
    expect(url.pathname).toBe("/ws/test-ws/kanban");
  });

  it("generates URL with issue as route segment for issue-detail view", () => {
    const result = buildShareUrl({
      view: "issue-detail",
      issue: "abc-123",
    });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/issues/abc-123");
    expect(url.searchParams.has("issue")).toBe(false);
    expect(url.searchParams.has("view")).toBe(false);
  });

  it("generates panel URL with issue as path segment for kanban view", () => {
    const result = buildShareUrl({ view: "kanban", issue: "TASK-1" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/kanban/issues/TASK-1");
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("generates panel URL for graph view", () => {
    const result = buildShareUrl({ view: "graph", issue: "T-5" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/graph/issues/T-5");
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("generates panel URL for monitor view", () => {
    const result = buildShareUrl({ view: "monitor", issue: "T-9" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/monitor/issues/T-9");
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("falls back to query param for non-panel view with issue", () => {
    // terminal/settings/workspace don't support panel URLs — carry the issue
    // in the query string so callers that pass those combinations don't break.
    const result = buildShareUrl({ view: "terminal", issue: "T-5" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/terminal");
    expect(url.searchParams.get("issue")).toBe("T-5");
  });

  it("omits issue param when null on panel view", () => {
    const result = buildShareUrl({ view: "table", issue: null });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
    // null issue → falls back to base view URL, not a panel URL
    expect(url.pathname).toBe("/ws/test-ws/table");
  });

  it("omits issue param when empty string on panel view", () => {
    const result = buildShareUrl({ view: "table", issue: "" });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
    expect(url.pathname).toBe("/ws/test-ws/table");
  });

  it("preserves existing filter params when building a panel URL", () => {
    mockLocation(
      "http://localhost:3000/ws/test-ws/kanban?priority=2&type=bug&search=deploy",
    );
    const result = buildShareUrl({
      view: "table",
      issue: "TASK-5",
    });
    const url = new URL(result);
    // table is a panel-supporting view → issue moves into the path
    expect(url.pathname).toBe("/ws/test-ws/table/issues/TASK-5");
    expect(url.searchParams.get("priority")).toBe("2");
    expect(url.searchParams.get("type")).toBe("bug");
    expect(url.searchParams.get("search")).toBe("deploy");
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("changes view path segment when switching views", () => {
    mockLocation("http://localhost:3000/ws/test-ws/kanban");
    const result = buildShareUrl({ view: "table" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/table");
  });

  it("removes legacy view query param if present", () => {
    mockLocation("http://localhost:3000/ws/test-ws/kanban?view=kanban");
    const result = buildShareUrl({ view: "table" });
    const url = new URL(result);
    expect(url.searchParams.has("view")).toBe(false);
    expect(url.pathname).toBe("/ws/test-ws/table");
  });

  it("removes existing issue param when null is passed", () => {
    mockLocation("http://localhost:3000/ws/test-ws/kanban?issue=TASK-5");
    const result = buildShareUrl({ issue: null });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("handles missing params gracefully (only view)", () => {
    const result = buildShareUrl({ view: "graph" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/graph");
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("handles missing params gracefully (only issue)", () => {
    const result = buildShareUrl({ issue: "TASK-99" });
    const url = new URL(result);
    expect(url.searchParams.get("issue")).toBe("TASK-99");
  });

  it("returns empty string in non-browser environment", () => {
    const originalWindow = globalThis.window;
    // @ts-expect-error - intentionally setting window to undefined for SSR test
    delete globalThis.window;

    const result = buildShareUrl({ view: "table", issue: "TASK-1" });
    expect(result).toBe("");

    globalThis.window = originalWindow;
  });

  it("extracts workspace base from nested paths", () => {
    mockLocation("http://localhost:3000/ws/my-workspace/issues/T-5");
    const result = buildShareUrl({ view: "kanban" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/my-workspace/kanban");
  });
});
