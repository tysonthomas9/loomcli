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

  it("generates URL with issue as query param for non-detail views", () => {
    const result = buildShareUrl({ view: "table", issue: "TASK-123" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/table");
    expect(url.searchParams.get("issue")).toBe("TASK-123");
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

  it("uses kanban path segment for default view", () => {
    const result = buildShareUrl({ view: "kanban", issue: "TASK-1" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/kanban");
    expect(url.searchParams.get("issue")).toBe("TASK-1");
  });

  it("omits issue param when null", () => {
    const result = buildShareUrl({ view: "table", issue: null });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
    expect(url.pathname).toBe("/ws/test-ws/table");
  });

  it("omits issue param when empty string", () => {
    const result = buildShareUrl({ view: "table", issue: "" });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("preserves existing filter params from current URL", () => {
    mockLocation(
      "http://localhost:3000/ws/test-ws/kanban?priority=2&type=bug&search=deploy",
    );
    const result = buildShareUrl({
      view: "table",
      issue: "TASK-5",
    });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/table");
    expect(url.searchParams.get("priority")).toBe("2");
    expect(url.searchParams.get("type")).toBe("bug");
    expect(url.searchParams.get("search")).toBe("deploy");
    expect(url.searchParams.get("issue")).toBe("TASK-5");
  });

  it("changes view path segment when switching views", () => {
    mockLocation("http://localhost:3000/ws/test-ws/kanban");
    const result = buildShareUrl({ view: "table" });
    const url = new URL(result);
    expect(url.pathname).toBe("/ws/test-ws/table");
  });

  it("preserves existing query params when switching views", () => {
    mockLocation("http://localhost:3000/ws/test-ws/kanban?filter=mine");
    const result = buildShareUrl({ view: "table" });
    const url = new URL(result);
    expect(url.searchParams.get("filter")).toBe("mine");
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
