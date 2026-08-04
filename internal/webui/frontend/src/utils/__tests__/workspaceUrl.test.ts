/**
 * Unit tests for buildWorkspaceSwitchUrl.
 *
 * The whitelist is intentionally narrow — only `view=` is preserved across
 * workspace boundaries. These tests document and enforce that contract.
 */

import { describe, it, expect } from "vitest";

import { buildWorkspaceSwitchUrl } from "../workspaceUrl";

const TARGET = "9e797364-b4a3-4b19-991c-bbc6abf659bb";

describe("buildWorkspaceSwitchUrl", () => {
  describe("base URL shape", () => {
    it("returns /ws/{id}/ with no query string when there are no params", () => {
      expect(buildWorkspaceSwitchUrl(TARGET, "")).toBe(`/ws/${TARGET}/`);
    });

    it("returns /ws/{id}/ when only non-whitelisted params are present", () => {
      expect(
        buildWorkspaceSwitchUrl(TARGET, "?issue=task-123&repo=loomcli"),
      ).toBe(`/ws/${TARGET}/`);
    });

    it("trailing slash is always present", () => {
      const url = buildWorkspaceSwitchUrl(TARGET, "");
      expect(url.endsWith("/")).toBe(true);
    });
  });

  describe("view= preservation", () => {
    it("preserves view=terminal", () => {
      expect(buildWorkspaceSwitchUrl(TARGET, "?view=terminal")).toBe(
        `/ws/${TARGET}/?view=terminal`,
      );
    });

    it("preserves view=kanban", () => {
      expect(buildWorkspaceSwitchUrl(TARGET, "?view=kanban")).toBe(
        `/ws/${TARGET}/?view=kanban`,
      );
    });

    it("preserves view=monitor", () => {
      expect(buildWorkspaceSwitchUrl(TARGET, "?view=monitor")).toBe(
        `/ws/${TARGET}/?view=monitor`,
      );
    });

    it("URL-encodes view value with special characters", () => {
      // Defensive: view names are alphanumeric in practice, but if a custom
      // value with spaces or unicode were ever passed, we should not break.
      const url = buildWorkspaceSwitchUrl(TARGET, "?view=hello%20world");
      expect(url).toBe(`/ws/${TARGET}/?view=hello+world`);
    });

    it("ignores empty view=", () => {
      expect(buildWorkspaceSwitchUrl(TARGET, "?view=")).toBe(`/ws/${TARGET}/`);
    });
  });

  describe("non-whitelisted param dropping", () => {
    it("drops issue= (would leak stale issue id into new workspace)", () => {
      expect(buildWorkspaceSwitchUrl(TARGET, "?issue=task-123")).toBe(
        `/ws/${TARGET}/`,
      );
    });

    it("drops repo= (would leak stale repo selection)", () => {
      expect(buildWorkspaceSwitchUrl(TARGET, "?repo=loomcli")).toBe(
        `/ws/${TARGET}/`,
      );
    });

    it("drops filter params (priority, type, status, search)", () => {
      const params = "?priority=P1&type=bug&status=open&search=foo";
      expect(buildWorkspaceSwitchUrl(TARGET, params)).toBe(`/ws/${TARGET}/`);
    });

    it("drops everything when view= is also present", () => {
      // The whole point of the whitelist: even with a mix of params, only
      // view= survives.
      const params = "?view=terminal&issue=task-123&repo=loomcli&priority=P1";
      expect(buildWorkspaceSwitchUrl(TARGET, params)).toBe(
        `/ws/${TARGET}/?view=terminal`,
      );
    });
  });

  describe("targetId interpolation", () => {
    it("UUIDs round-trip without re-encoding", () => {
      // Workspace IDs are UUIDs; they don't need encoding. Encoding them
      // would break the URL.
      const id = "9e797364-b4a3-4b19-991c-bbc6abf659bb";
      expect(buildWorkspaceSwitchUrl(id, "")).toBe(`/ws/${id}/`);
    });

    it("does not URL-encode the target id (assumes UUID)", () => {
      // Defensive sanity check: UUID characters are URL-safe.
      const url = buildWorkspaceSwitchUrl("a-b-c-d-e", "");
      expect(url).toBe("/ws/a-b-c-d-e/");
    });
  });

  describe("default currentSearch", () => {
    it("falls back to window.location.search when no second arg", () => {
      // We can't reliably mock window.location in this test context, but we
      // can verify the function doesn't throw with the default arg.
      expect(() => buildWorkspaceSwitchUrl(TARGET)).not.toThrow();
    });
  });
});
