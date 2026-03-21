/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for buildShareUrl utility.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

import { DEFAULT_VIEW } from "@/components/ViewSwitcher";
import { buildShareUrl } from "../buildShareUrl";

/**
 * Mock window.location with given URL parts.
 */
function mockLocation(href: string): void {
  // Use a real URL object so searchParams work correctly
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
    mockLocation("http://localhost:3000/app");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns current URL when no params provided", () => {
    const result = buildShareUrl();
    expect(result).toBe("http://localhost:3000/app");
  });

  it("generates URL with issue param", () => {
    const result = buildShareUrl({ issue: "TASK-123" });
    const url = new URL(result);
    expect(url.searchParams.get("issue")).toBe("TASK-123");
  });

  it("generates URL with view and issue params", () => {
    const result = buildShareUrl({ view: "table", issue: "TASK-123" });
    const url = new URL(result);
    expect(url.searchParams.get("view")).toBe("table");
    expect(url.searchParams.get("issue")).toBe("TASK-123");
  });

  it("generates URL with issue-detail view and issue param", () => {
    const result = buildShareUrl({
      view: "issue-detail",
      issue: "abc-123",
    });
    const url = new URL(result);
    expect(url.searchParams.get("view")).toBe("issue-detail");
    expect(url.searchParams.get("issue")).toBe("abc-123");
  });

  it("omits view param when it matches DEFAULT_VIEW", () => {
    const result = buildShareUrl({ view: DEFAULT_VIEW, issue: "TASK-1" });
    const url = new URL(result);
    expect(url.searchParams.has("view")).toBe(false);
    expect(url.searchParams.get("issue")).toBe("TASK-1");
  });

  it("omits issue param when null", () => {
    const result = buildShareUrl({ view: "table", issue: null });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
    expect(url.searchParams.get("view")).toBe("table");
  });

  it("omits issue param when empty string", () => {
    const result = buildShareUrl({ view: "table", issue: "" });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("preserves existing filter params from current URL", () => {
    mockLocation("http://localhost:3000/app?priority=2&type=bug&search=deploy");
    const result = buildShareUrl({
      view: "issue-detail",
      issue: "TASK-5",
    });
    const url = new URL(result);
    expect(url.searchParams.get("priority")).toBe("2");
    expect(url.searchParams.get("type")).toBe("bug");
    expect(url.searchParams.get("search")).toBe("deploy");
    expect(url.searchParams.get("view")).toBe("issue-detail");
    expect(url.searchParams.get("issue")).toBe("TASK-5");
  });

  it("overwrites existing view param", () => {
    mockLocation("http://localhost:3000/app?view=kanban");
    const result = buildShareUrl({ view: "table" });
    const url = new URL(result);
    expect(url.searchParams.get("view")).toBe("table");
  });

  it("overwrites existing issue param", () => {
    mockLocation("http://localhost:3000/app?issue=old-issue");
    const result = buildShareUrl({ issue: "new-issue" });
    const url = new URL(result);
    expect(url.searchParams.get("issue")).toBe("new-issue");
  });

  it("removes existing issue param when null is passed", () => {
    mockLocation("http://localhost:3000/app?view=issue-detail&issue=TASK-5");
    const result = buildShareUrl({ issue: null });
    const url = new URL(result);
    expect(url.searchParams.has("issue")).toBe(false);
    // view should still be there since we didn't touch it
    expect(url.searchParams.get("view")).toBe("issue-detail");
  });

  it("handles missing params gracefully (only view)", () => {
    const result = buildShareUrl({ view: "graph" });
    const url = new URL(result);
    expect(url.searchParams.get("view")).toBe("graph");
    expect(url.searchParams.has("issue")).toBe(false);
  });

  it("handles missing params gracefully (only issue)", () => {
    const result = buildShareUrl({ issue: "TASK-99" });
    const url = new URL(result);
    expect(url.searchParams.get("issue")).toBe("TASK-99");
    expect(url.searchParams.has("view")).toBe(false);
  });

  it("returns empty string in non-browser environment", () => {
    const originalWindow = globalThis.window;
    // @ts-expect-error - intentionally setting window to undefined for SSR test
    delete globalThis.window;

    const result = buildShareUrl({ view: "table", issue: "TASK-1" });
    expect(result).toBe("");

    globalThis.window = originalWindow;
  });
});
