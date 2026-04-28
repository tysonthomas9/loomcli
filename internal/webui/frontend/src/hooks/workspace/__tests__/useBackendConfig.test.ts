/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useBackendConfig hook.
 *
 * These tests verify fetch-on-mount behavior, loading/error states,
 * optimistic updates, rollback on failure, and refetch.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { getBackendConfig, updateBackendConfig } from "@/api/common";
import type { BackendConfigData } from "@/api/common";
import { getWorkspaceBackendConfig } from "@/api/workspace";

import { useBackendConfig } from "../useBackendConfig";

// Mock the config API module
vi.mock("@/api/common", () => ({
  getBackendConfig: vi.fn(),
  updateBackendConfig: vi.fn(),
  getCachedBackendConfig: vi.fn().mockReturnValue(null),
}));

vi.mock("@/api/workspace", () => ({
  getWorkspaceBackendConfig: vi.fn(),
}));

const mockGetBackendConfig = vi.mocked(getBackendConfig);
const mockUpdateBackendConfig = vi.mocked(updateBackendConfig);
const mockGetWorkspaceBackendConfig = vi.mocked(getWorkspaceBackendConfig);

/**
 * Helper to create a mock BackendConfigData.
 */
function createMockConfig(
  overrides?: Partial<BackendConfigData>,
): BackendConfigData {
  return {
    backend: "anthropic",
    source: "project",
    available: ["anthropic", "openai", "local"],
    agents: [],
    ...overrides,
  };
}

/**
 * Helper to flush pending promises.
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useBackendConfig", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial fetch", () => {
    it("fetches config on mount and returns data", async () => {
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(mockGetBackendConfig).toHaveBeenCalledTimes(1);
      expect(result.current.config).toEqual(mockConfig);
    });

    it("returns loading true initially, false after fetch", async () => {
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      // Initially loading
      expect(result.current.isLoading).toBe(true);
      expect(result.current.config).toBeNull();

      await flushPromises();

      // After fetch completes
      expect(result.current.isLoading).toBe(false);
      expect(result.current.config).toEqual(mockConfig);
    });

    it("returns error on fetch failure", async () => {
      mockGetBackendConfig.mockRejectedValueOnce(
        new Error("Server unavailable"),
      );

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe("Server unavailable");
      expect(result.current.config).toBeNull();
    });

    it("returns generic error message for non-Error exceptions", async () => {
      mockGetBackendConfig.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.error).toBe("Failed to load backend config");
    });
  });

  describe("updateBackend", () => {
    it("optimistically updates config.backend", async () => {
      const mockConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config?.backend).toBe("anthropic");

      // Start update but don't resolve yet
      const updatedConfig = createMockConfig({
        backend: "openai",
        source: "project",
      });
      let resolveUpdate!: (value: BackendConfigData) => void;
      mockUpdateBackendConfig.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveUpdate = resolve;
          }),
      );

      let updatePromise: Promise<void>;
      act(() => {
        updatePromise = result.current.updateBackend("openai");
      });

      // Optimistic update should be applied immediately
      expect(result.current.config?.backend).toBe("openai");
      expect(result.current.config?.source).toBe("project");
      expect(result.current.isSaving).toBe(true);

      // Resolve the API call
      await act(async () => {
        resolveUpdate(updatedConfig);
        await updatePromise!;
      });

      expect(result.current.isSaving).toBe(false);
      expect(result.current.config?.backend).toBe("openai");
    });

    it("rolls back on API error", async () => {
      const mockConfig = createMockConfig({
        backend: "anthropic",
        source: "default",
      });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config?.backend).toBe("anthropic");

      // Make the update fail
      mockUpdateBackendConfig.mockRejectedValueOnce(new Error("Save failed"));

      await act(async () => {
        try {
          await result.current.updateBackend("openai");
        } catch {
          // Expected to throw
        }
      });

      // Should have rolled back to the original config
      expect(result.current.config?.backend).toBe("anthropic");
      expect(result.current.config?.source).toBe("default");
      expect(result.current.error).toBe("Save failed");
      expect(result.current.isSaving).toBe(false);
    });

    it("does nothing if config is null", async () => {
      mockGetBackendConfig.mockRejectedValueOnce(new Error("failed"));

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config).toBeNull();

      await act(async () => {
        await result.current.updateBackend("openai");
      });

      expect(mockUpdateBackendConfig).not.toHaveBeenCalled();
    });

    it("calls updateBackendConfig API with correct backend string", async () => {
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);
      const updatedConfig = createMockConfig({ backend: "local" });
      mockUpdateBackendConfig.mockResolvedValueOnce(updatedConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      await act(async () => {
        await result.current.updateBackend("local");
      });

      expect(mockUpdateBackendConfig).toHaveBeenCalledWith("local");
    });
  });

  describe("refetch", () => {
    it("re-fetches config from API", async () => {
      const initialConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(initialConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config?.backend).toBe("anthropic");
      expect(mockGetBackendConfig).toHaveBeenCalledTimes(1);

      // Setup a different config for the refetch
      const updatedConfig = createMockConfig({ backend: "openai" });
      mockGetBackendConfig.mockResolvedValueOnce(updatedConfig);

      await act(async () => {
        result.current.refetch();
      });

      await flushPromises();

      expect(mockGetBackendConfig).toHaveBeenCalledTimes(2);
      expect(result.current.config?.backend).toBe("openai");
    });

    it("clears error on refetch", async () => {
      // First fetch fails
      mockGetBackendConfig.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.error).toBe("Network error");

      // Refetch succeeds
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      await act(async () => {
        result.current.refetch();
      });

      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.config).toEqual(mockConfig);
    });
  });

  describe("error handling during config update", () => {
    it("sets error on non-Error exception during update", async () => {
      const mockConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      // Reject with a string (not an Error)
      mockUpdateBackendConfig.mockRejectedValueOnce("string error");

      await act(async () => {
        await result.current.updateBackend("openai");
      });

      expect(result.current.error).toBe("Failed to save backend config");
      expect(result.current.config?.backend).toBe("anthropic");
    });

    it("clears error from previous update on new successful update", async () => {
      const mockConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      // First update fails
      mockUpdateBackendConfig.mockRejectedValueOnce(new Error("Save failed"));

      await act(async () => {
        await result.current.updateBackend("openai");
      });

      expect(result.current.error).toBe("Save failed");

      // Second update succeeds
      const updatedConfig = createMockConfig({ backend: "local" });
      mockUpdateBackendConfig.mockResolvedValueOnce(updatedConfig);

      await act(async () => {
        await result.current.updateBackend("local");
      });

      expect(result.current.error).toBeNull();
      expect(result.current.config?.backend).toBe("local");
    });

    it("returns false on API failure", async () => {
      const mockConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      mockUpdateBackendConfig.mockRejectedValueOnce(new Error("Save failed"));

      let returnValue: boolean | undefined;
      await act(async () => {
        returnValue = await result.current.updateBackend("openai");
      });

      expect(returnValue).toBe(false);
    });

    it("returns true on API success", async () => {
      const mockConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      const updatedConfig = createMockConfig({ backend: "openai" });
      mockUpdateBackendConfig.mockResolvedValueOnce(updatedConfig);

      let returnValue: boolean | undefined;
      await act(async () => {
        returnValue = await result.current.updateBackend("openai");
      });

      expect(returnValue).toBe(true);
    });
  });

  describe("optimistic update rollback on failure", () => {
    it("preserves all original config fields on rollback", async () => {
      const mockConfig = createMockConfig({
        backend: "anthropic",
        source: "project",
        available: ["anthropic", "openai"],
        agents: [{ name: "agent-1", backend: "anthropic" }],
      });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      // Verify initial state
      expect(result.current.config?.source).toBe("project");
      expect(result.current.config?.available).toEqual(["anthropic", "openai"]);
      expect(result.current.config?.agents).toEqual([
        { name: "agent-1", backend: "anthropic" },
      ]);

      // Trigger failing update
      mockUpdateBackendConfig.mockRejectedValueOnce(new Error("Save failed"));

      await act(async () => {
        await result.current.updateBackend("openai");
      });

      // ALL fields should be restored, not just backend
      expect(result.current.config?.backend).toBe("anthropic");
      expect(result.current.config?.source).toBe("project");
      expect(result.current.config?.available).toEqual(["anthropic", "openai"]);
      expect(result.current.config?.agents).toEqual([
        { name: "agent-1", backend: "anthropic" },
      ]);
    });

    it("isSaving transitions: false → true → false on failure", async () => {
      const mockConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.isSaving).toBe(false);

      // Use deferred promise to control timing
      let rejectUpdate!: (err: Error) => void;
      mockUpdateBackendConfig.mockImplementationOnce(
        () =>
          new Promise((_, reject) => {
            rejectUpdate = reject;
          }),
      );

      let updatePromise: Promise<boolean>;
      act(() => {
        updatePromise = result.current.updateBackend("openai");
      });

      // isSaving should be true during API call
      expect(result.current.isSaving).toBe(true);

      // Reject the API call
      await act(async () => {
        rejectUpdate(new Error("Save failed"));
        await updatePromise!;
      });

      // isSaving should be false after failure
      expect(result.current.isSaving).toBe(false);
    });
  });

  describe("unmount safety", () => {
    it("does not update state after unmount during fetch", async () => {
      // Use deferred promise for fetch
      let resolveFetch!: (config: BackendConfigData) => void;
      mockGetBackendConfig.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { unmount } = renderHook(() => useBackendConfig());

      // Unmount before fetch resolves
      unmount();

      // Resolve the fetch — should not throw (no state update on unmounted component)
      await act(async () => {
        resolveFetch(createMockConfig());
        await Promise.resolve();
      });

      // If we got here without errors, the test passes
    });

    it("does not update state after unmount during updateBackend", async () => {
      const mockConfig = createMockConfig({ backend: "anthropic" });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result, unmount } = renderHook(() => useBackendConfig());

      await flushPromises();

      // Use deferred promise for update
      let resolveUpdate!: (config: BackendConfigData) => void;
      mockUpdateBackendConfig.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveUpdate = resolve;
          }),
      );

      let updatePromise: Promise<boolean>;
      act(() => {
        updatePromise = result.current.updateBackend("openai");
      });

      // Unmount before update resolves
      unmount();

      // Resolve the update — should not throw
      await act(async () => {
        resolveUpdate(createMockConfig({ backend: "openai" }));
        await updatePromise!;
      });

      // If we got here without errors, the test passes
    });
  });

  describe("workspaceId routing", () => {
    it("calls workspace-scoped fetch when workspaceId is provided", async () => {
      const cfg = createMockConfig({ backend: "codex", source: "workspace" });
      mockGetWorkspaceBackendConfig.mockResolvedValueOnce(cfg);

      const { result } = renderHook(() => useBackendConfig("ws-fleet-1"));
      await flushPromises();

      expect(mockGetWorkspaceBackendConfig).toHaveBeenCalledWith("ws-fleet-1");
      expect(mockGetBackendConfig).not.toHaveBeenCalled();
      expect(result.current.config).toEqual(cfg);
      expect(result.current.error).toBeNull();
    });

    it("falls back to unscoped fetch when workspaceId is undefined", async () => {
      const cfg = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(cfg);

      const { result } = renderHook(() => useBackendConfig());
      await flushPromises();

      expect(mockGetBackendConfig).toHaveBeenCalled();
      expect(mockGetWorkspaceBackendConfig).not.toHaveBeenCalled();
      expect(result.current.config).toEqual(cfg);
    });

    it("re-fetches when workspaceId changes", async () => {
      const cfgA = createMockConfig({ backend: "claude" });
      const cfgB = createMockConfig({ backend: "codex" });
      mockGetWorkspaceBackendConfig
        .mockResolvedValueOnce(cfgA)
        .mockResolvedValueOnce(cfgB);

      const { result, rerender } = renderHook(
        ({ ws }: { ws: string }) => useBackendConfig(ws),
        { initialProps: { ws: "ws-A" } },
      );
      await flushPromises();
      expect(result.current.config?.backend).toBe("claude");

      rerender({ ws: "ws-B" });
      await flushPromises();
      expect(mockGetWorkspaceBackendConfig).toHaveBeenNthCalledWith(2, "ws-B");
      expect(result.current.config?.backend).toBe("codex");
    });

    it("propagates workspace-scoped fetch error", async () => {
      mockGetWorkspaceBackendConfig.mockRejectedValueOnce(
        new Error("503 daemon unavailable"),
      );

      const { result } = renderHook(() => useBackendConfig("ws-fleet-1"));
      await flushPromises();

      expect(result.current.error).toBe("503 daemon unavailable");
    });
  });
});
