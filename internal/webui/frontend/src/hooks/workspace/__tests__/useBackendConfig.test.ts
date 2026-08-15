/**
 * @vitest-environment jsdom
 */

import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { getCachedBackendConfig } from "@/api/common";
import type { BackendConfigData } from "@/api/common";
import {
  getWorkspaceBackendConfig,
  updateWorkspaceBackend,
} from "@/api/workspace";

import { useBackendConfig } from "../useBackendConfig";

vi.mock("@/api/common", () => ({
  getCachedBackendConfig: vi.fn().mockReturnValue(null),
}));

vi.mock("@/api/workspace", () => ({
  getWorkspaceBackendConfig: vi.fn(),
  updateWorkspaceBackend: vi.fn(),
}));

const mockGetCachedBackendConfig = vi.mocked(getCachedBackendConfig);
const mockGetWorkspaceBackendConfig = vi.mocked(getWorkspaceBackendConfig);
const mockUpdateWorkspaceBackend = vi.mocked(updateWorkspaceBackend);

function createMockConfig(
  overrides?: Partial<BackendConfigData>,
): BackendConfigData {
  return {
    backend: "claude",
    source: "default",
    available: ["claude", "codex", "shell"],
    agents: [],
    ...overrides,
  };
}

describe("useBackendConfig", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetCachedBackendConfig.mockReturnValue(null);
  });

  it("fetches workspace-scoped backend config", async () => {
    const config = createMockConfig({ backend: "codex", source: "fleetdb" });
    mockGetWorkspaceBackendConfig.mockResolvedValueOnce(config);

    const { result } = renderHook(() => useBackendConfig("FLEETDB"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(mockGetWorkspaceBackendConfig).toHaveBeenCalledWith("FLEETDB");
    expect(result.current.config).toEqual(config);
    expect(result.current.error).toBeNull();
  });

  it("requires a workspace ID", async () => {
    const { result } = renderHook(() => useBackendConfig());

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(mockGetWorkspaceBackendConfig).not.toHaveBeenCalled();
    expect(result.current.error).toBe("workspace ID is required");
  });

  it("optimistically updates and refetches workspace config", async () => {
    const initial = createMockConfig({ backend: "claude" });
    const updated = createMockConfig({ backend: "codex", source: "fleetdb" });
    mockGetWorkspaceBackendConfig
      .mockResolvedValueOnce(initial)
      .mockResolvedValueOnce(updated);
    mockUpdateWorkspaceBackend.mockResolvedValueOnce({} as never);

    const { result } = renderHook(() => useBackendConfig("FLEETDB"));
    await waitFor(() => expect(result.current.config).toEqual(initial));

    let ok = false;
    await act(async () => {
      ok = await result.current.updateBackend("codex");
    });

    expect(ok).toBe(true);
    expect(mockUpdateWorkspaceBackend).toHaveBeenCalledWith("FLEETDB", "codex");
    expect(result.current.config).toEqual(updated);
  });

  it("rolls back optimistic update on save failure", async () => {
    const initial = createMockConfig({ backend: "claude" });
    mockGetWorkspaceBackendConfig.mockResolvedValueOnce(initial);
    mockUpdateWorkspaceBackend.mockRejectedValueOnce(new Error("save failed"));

    const { result } = renderHook(() => useBackendConfig("FLEETDB"));
    await waitFor(() => expect(result.current.config).toEqual(initial));

    let ok = true;
    await act(async () => {
      ok = await result.current.updateBackend("codex");
    });

    expect(ok).toBe(false);
    expect(result.current.config).toEqual(initial);
    expect(result.current.error).toBe("save failed");
  });

  describe("isLoading readiness", () => {
    it("is loading in the same render in which enabled flips false->true", () => {
      // Mirror of useTerminalMetadata: isLoading is seeded from `enabled`, so
      // without a render-phase reset a consumer reads configLoading===false in
      // the very commit where the view activates.
      mockGetWorkspaceBackendConfig.mockImplementation(
        () => new Promise<BackendConfigData>(() => {}),
      );

      const { result, rerender } = renderHook(
        ({ enabled }: { enabled: boolean }) =>
          useBackendConfig("FLEETDB", { enabled }),
        { initialProps: { enabled: false } },
      );

      expect(result.current.isLoading).toBe(false);

      rerender({ enabled: true });

      expect(result.current.isLoading).toBe(true);
    });

    it("is loading in the same render in which the workspace changes", async () => {
      const config = createMockConfig();
      mockGetWorkspaceBackendConfig.mockResolvedValueOnce(config);

      const { result, rerender } = renderHook(
        ({ ws }: { ws: string }) => useBackendConfig(ws),
        { initialProps: { ws: "WS1" } },
      );
      await waitFor(() => expect(result.current.isLoading).toBe(false));

      mockGetWorkspaceBackendConfig.mockImplementation(
        () => new Promise<BackendConfigData>(() => {}),
      );
      rerender({ ws: "WS2" });

      expect(result.current.isLoading).toBe(true);
    });

    it("keeps the cached config visible across the reset", async () => {
      const cached = createMockConfig({ backend: "codex" });
      mockGetCachedBackendConfig.mockReturnValue(cached);
      mockGetWorkspaceBackendConfig.mockImplementation(
        () => new Promise<BackendConfigData>(() => {}),
      );

      const { result, rerender } = renderHook(
        ({ enabled }: { enabled: boolean }) =>
          useBackendConfig("FLEETDB", { enabled }),
        { initialProps: { enabled: false } },
      );

      rerender({ enabled: true });

      expect(result.current.config).toEqual(cached);
      expect(result.current.isCached).toBe(true);
    });
  });
});
