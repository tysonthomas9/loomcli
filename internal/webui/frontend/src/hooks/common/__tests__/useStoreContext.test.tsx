/**
 * @vitest-environment jsdom
 */
import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import React from "react";

import type { StoreApi } from "zustand/vanilla";

// ---------------------------------------------------------------------------
// vi.hoisted — variables that vi.mock factories can reference
// ---------------------------------------------------------------------------

const {
  mockWorkspace,
  mockEvent,
  mockShowToast,
  EventProviderSpy,
  mockCreateIssueStore,
  mockCreateAgentStore,
  makeMockStore,
  makeMockIssueStoreMethods,
  makeMockAgentStoreMethods,
  issueStoreRef,
  agentStoreRef,
  issueMethodsRef,
  agentMethodsRef,
} = vi.hoisted(() => {
  const _makeMockIssueStoreMethods = () => ({
    connectToEvents: vi.fn(() => vi.fn()),
    setConnectionState: vi.fn(),
    setReconnectAttempts: vi.fn(),
    fetchIssues: vi.fn(),
    reset: vi.fn(),
    isLoading: false,
  });

  const _makeMockAgentStoreMethods = () => ({
    reset: vi.fn(),
    startPolling: vi.fn(),
    stopPolling: vi.fn(),
    isLoading: false,
  });

  type MockIssueStoreMethods = ReturnType<typeof _makeMockIssueStoreMethods>;
  type MockAgentStoreMethods = ReturnType<typeof _makeMockAgentStoreMethods>;

  const _issueMethodsRef = { current: _makeMockIssueStoreMethods() };
  const _agentMethodsRef = { current: _makeMockAgentStoreMethods() };

  const _makeMockStore = <T,>(methods: T): StoreApi<T> =>
    ({
      getState: () => methods,
      subscribe: vi.fn(),
      setState: vi.fn(),
      destroy: vi.fn(),
    }) as unknown as StoreApi<T>;

  const _issueStoreRef = { current: null as StoreApi<any> | null };
  const _agentStoreRef = { current: null as StoreApi<any> | null };

  const _mockCreateIssueStore = vi.fn((..._args: unknown[]) => {
    _issueStoreRef.current = _makeMockStore(_issueMethodsRef.current);
    return _issueStoreRef.current;
  });

  const _mockCreateAgentStore = vi.fn((..._args: unknown[]) => {
    _agentStoreRef.current = _makeMockStore(_agentMethodsRef.current);
    return _agentStoreRef.current;
  });

  return {
    mockWorkspace: {
      workspaceId: "test-ws-id",
      sourceReposFilter: undefined as string[] | undefined,
    },
    mockEvent: {
      state: "connected" as
        | "disconnected"
        | "connecting"
        | "connected"
        | "reconnecting",
      reconnectAttempts: 0,
      subscribe: vi.fn(() => vi.fn()),
      retryNow: vi.fn(),
    },
    mockShowToast: vi.fn(),
    EventProviderSpy: {
      lastSourceRepos: undefined as string[] | undefined,
      renderCount: 0,
    },
    mockCreateIssueStore: _mockCreateIssueStore,
    mockCreateAgentStore: _mockCreateAgentStore,
    makeMockStore: _makeMockStore,
    makeMockIssueStoreMethods: _makeMockIssueStoreMethods,
    makeMockAgentStoreMethods: _makeMockAgentStoreMethods,
    issueStoreRef: _issueStoreRef,
    agentStoreRef: _agentStoreRef,
    issueMethodsRef: _issueMethodsRef as { current: MockIssueStoreMethods },
    agentMethodsRef: _agentMethodsRef as { current: MockAgentStoreMethods },
  };
});

// ---------------------------------------------------------------------------
// Mock dependencies
// ---------------------------------------------------------------------------

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return { ...actual, useWorkspaceContext: () => mockWorkspace };
});

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return { ...actual, useToast: () => ({ showToast: mockShowToast }) };
});

vi.mock("../useEventProvider", () => ({
  EventProvider: ({
    children,
    sourceRepos,
  }: {
    children: React.ReactNode;
    sourceRepos?: string[];
  }) => {
    EventProviderSpy.lastSourceRepos = sourceRepos;
    EventProviderSpy.renderCount += 1;
    return <div data-testid="event-provider">{children}</div>;
  },
  useEventContext: () => mockEvent,
}));

vi.mock("@/stores/issueStore", () => ({
  createIssueStore: (...args: unknown[]) => mockCreateIssueStore(...args),
}));

vi.mock("@/stores/agentStore", () => ({
  createAgentStore: (...args: unknown[]) => mockCreateAgentStore(...args),
}));

// ---------------------------------------------------------------------------
// Import SUT (after mocks)
// ---------------------------------------------------------------------------

import {
  StoreProvider,
  useIssueStoreInstance,
  useAgentStoreInstance,
  NO_STORE_CONTEXT,
} from "../useStoreContext";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function wrapper({ children }: { children: React.ReactNode }) {
  return <StoreProvider>{children}</StoreProvider>;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("useStoreContext", () => {
  beforeEach(() => {
    mockWorkspace.workspaceId = "test-ws-id";
    mockWorkspace.sourceReposFilter = undefined;
    mockEvent.state = "connected";
    mockEvent.reconnectAttempts = 0;

    issueMethodsRef.current = makeMockIssueStoreMethods();
    agentMethodsRef.current = makeMockAgentStoreMethods();

    mockCreateIssueStore.mockClear();
    mockCreateAgentStore.mockClear();
    mockEvent.subscribe.mockClear();
    mockEvent.retryNow.mockClear();
    mockShowToast.mockClear();

    mockCreateIssueStore.mockImplementation((..._args: unknown[]) => {
      issueStoreRef.current = makeMockStore(issueMethodsRef.current);
      return issueStoreRef.current;
    });
    mockCreateAgentStore.mockImplementation((..._args: unknown[]) => {
      agentStoreRef.current = makeMockStore(agentMethodsRef.current);
      return agentStoreRef.current;
    });

    EventProviderSpy.lastSourceRepos = undefined;
    EventProviderSpy.renderCount = 0;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // -----------------------------------------------------------------------
  // 1. Store creation
  // -----------------------------------------------------------------------

  describe("Store creation", () => {
    it("calls createIssueStore once with onToast and retryConnectionFn config", () => {
      renderHook(() => useIssueStoreInstance(), { wrapper });

      // +2 because NO_STORE_CONTEXT also calls it at module level,
      // but mockClear in beforeEach only clears after module load.
      // The provider call is the last one:
      const providerCallIdx = mockCreateIssueStore.mock.calls.length - 1;
      const config = mockCreateIssueStore.mock.calls[providerCallIdx][0] as any;
      expect(config).toBeDefined();
      expect(typeof config.onToast).toBe("function");
      expect(typeof config.retryConnectionFn).toBe("function");
    });

    it("calls createAgentStore once with onToast config", () => {
      renderHook(() => useAgentStoreInstance(), { wrapper });

      const providerCallIdx = mockCreateAgentStore.mock.calls.length - 1;
      const config = mockCreateAgentStore.mock.calls[providerCallIdx][0] as any;
      expect(config).toBeDefined();
      expect(typeof config.onToast).toBe("function");
    });
  });

  // -----------------------------------------------------------------------
  // 2. Context provision
  // -----------------------------------------------------------------------

  describe("Context provision", () => {
    it("useIssueStoreInstance returns the created issue store", () => {
      const { result } = renderHook(() => useIssueStoreInstance(), { wrapper });
      expect(result.current).toBe(issueStoreRef.current);
    });

    it("useAgentStoreInstance returns the created agent store", () => {
      const { result } = renderHook(() => useAgentStoreInstance(), { wrapper });
      expect(result.current).toBe(agentStoreRef.current);
    });
  });

  // -----------------------------------------------------------------------
  // 3. EventProvider mounting
  // -----------------------------------------------------------------------

  describe("EventProvider mounting", () => {
    it("renders EventProvider with sourceRepos prop", () => {
      mockWorkspace.sourceReposFilter = ["repo-a", "repo-b"];

      renderHook(() => useIssueStoreInstance(), { wrapper });

      expect(EventProviderSpy.lastSourceRepos).toEqual(["repo-a", "repo-b"]);
    });

    it("renders EventProvider with undefined sourceRepos when no filter", () => {
      mockWorkspace.sourceReposFilter = undefined;

      renderHook(() => useIssueStoreInstance(), { wrapper });

      expect(EventProviderSpy.lastSourceRepos).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // 4. connectToEvents wiring
  // -----------------------------------------------------------------------

  describe("connectToEvents wiring", () => {
    it("calls connectToEvents with eventContext.subscribe", () => {
      renderHook(() => useIssueStoreInstance(), { wrapper });

      expect(issueMethodsRef.current.connectToEvents).toHaveBeenCalledWith(
        mockEvent.subscribe,
      );
    });
  });

  // -----------------------------------------------------------------------
  // 5. Connection state mirroring
  // -----------------------------------------------------------------------

  describe("Connection state mirroring", () => {
    it("calls setConnectionState with eventContext.state", () => {
      mockEvent.state = "connecting";

      renderHook(() => useIssueStoreInstance(), { wrapper });

      expect(issueMethodsRef.current.setConnectionState).toHaveBeenCalledWith(
        "connecting",
      );
    });

    it("calls setConnectionState again when state changes", () => {
      mockEvent.state = "connecting";

      const { rerender } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      expect(issueMethodsRef.current.setConnectionState).toHaveBeenCalledWith(
        "connecting",
      );

      mockEvent.state = "connected";
      rerender();

      expect(issueMethodsRef.current.setConnectionState).toHaveBeenCalledWith(
        "connected",
      );
    });
  });

  // -----------------------------------------------------------------------
  // 6. reconnectAttempts mirroring
  // -----------------------------------------------------------------------

  describe("reconnectAttempts mirroring", () => {
    it("calls setReconnectAttempts with eventContext.reconnectAttempts", () => {
      mockEvent.reconnectAttempts = 3;

      renderHook(() => useIssueStoreInstance(), { wrapper });

      expect(issueMethodsRef.current.setReconnectAttempts).toHaveBeenCalledWith(
        3,
      );
    });

    it("calls setReconnectAttempts again when attempts change", () => {
      mockEvent.reconnectAttempts = 0;

      const { rerender } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      expect(issueMethodsRef.current.setReconnectAttempts).toHaveBeenCalledWith(
        0,
      );

      mockEvent.reconnectAttempts = 5;
      rerender();

      expect(issueMethodsRef.current.setReconnectAttempts).toHaveBeenCalledWith(
        5,
      );
    });
  });

  // -----------------------------------------------------------------------
  // 7. Initial mount: no reset, no fetchIssues
  // -----------------------------------------------------------------------

  describe("Initial mount (issue fetching delegated to App.tsx)", () => {
    it("does NOT reset stores on initial mount", () => {
      // Initial mount must not reset the stores. App.tsx fires its own
      // fetchIssues(...) before this parent effect runs (children-first
      // effect ordering); calling reset() here would abort the in-flight
      // fetch via activeController.abort() and leave the store empty
      // until the user switches tabs (historical bug).
      renderHook(() => useIssueStoreInstance(), { wrapper });

      expect(issueMethodsRef.current.reset).not.toHaveBeenCalled();
      expect(agentMethodsRef.current.reset).not.toHaveBeenCalled();
      // fetchIssues is NOT called by StoreWiring — App.tsx drives mode-based fetching
      expect(issueMethodsRef.current.fetchIssues).not.toHaveBeenCalled();
    });

    it("does NOT reset on re-render when workspaceId is unchanged", () => {
      const { rerender } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });
      issueMethodsRef.current.reset.mockClear();
      agentMethodsRef.current.reset.mockClear();

      rerender();

      expect(issueMethodsRef.current.reset).not.toHaveBeenCalled();
      expect(agentMethodsRef.current.reset).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 8. Initial startPolling
  // -----------------------------------------------------------------------

  describe("Initial polling", () => {
    it("calls startPolling with pollInterval 5000", () => {
      renderHook(() => useAgentStoreInstance(), { wrapper });

      expect(agentMethodsRef.current.startPolling).toHaveBeenCalledWith({
        pollInterval: 5000,
        workspaceId: "test-ws-id",
      });
    });
  });

  // -----------------------------------------------------------------------
  // 9. Workspace change
  // -----------------------------------------------------------------------

  describe("Workspace change", () => {
    it("resets both stores and re-starts agent polling on workspace change", () => {
      const { rerender } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      // Clear initial calls
      issueMethodsRef.current.reset.mockClear();
      agentMethodsRef.current.reset.mockClear();
      agentMethodsRef.current.startPolling.mockClear();
      agentMethodsRef.current.stopPolling.mockClear();

      // Change workspace
      mockWorkspace.workspaceId = "new-ws-id";
      rerender();

      expect(agentMethodsRef.current.stopPolling).toHaveBeenCalled();
      expect(issueMethodsRef.current.reset).toHaveBeenCalled();
      expect(agentMethodsRef.current.reset).toHaveBeenCalled();
      // fetchIssues is NOT called by StoreWiring — App.tsx drives mode-based fetching
      expect(issueMethodsRef.current.fetchIssues).not.toHaveBeenCalled();
      expect(agentMethodsRef.current.startPolling).toHaveBeenCalledWith({
        pollInterval: 5000,
        workspaceId: "new-ws-id",
      });
    });
  });

  // -----------------------------------------------------------------------
  // 10. sourceRepos change
  // -----------------------------------------------------------------------

  describe("sourceRepos change", () => {
    it("does NOT re-fetch issues from StoreWiring (App.tsx handles sourceRepos refetch)", () => {
      mockWorkspace.sourceReposFilter = ["repo-a"];

      const { rerender } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      issueMethodsRef.current.fetchIssues.mockClear();

      mockWorkspace.sourceReposFilter = ["repo-b"];
      rerender();

      // fetchIssues is NOT called by StoreWiring — App.tsx drives sourceRepos-based fetching
      expect(issueMethodsRef.current.fetchIssues).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 11. sourceRepos reorder (same sorted key) — no refetch
  // -----------------------------------------------------------------------

  describe("sourceRepos reorder", () => {
    it("does NOT refetch when sourceRepos are reordered but have the same sorted key", () => {
      mockWorkspace.sourceReposFilter = ["repo-b", "repo-a"];

      const { rerender } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      issueMethodsRef.current.fetchIssues.mockClear();

      // Reorder — same repos, different order
      mockWorkspace.sourceReposFilter = ["repo-a", "repo-b"];
      rerender();

      expect(issueMethodsRef.current.fetchIssues).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 12. retryConnection bridge
  // -----------------------------------------------------------------------

  describe("retryConnection bridge", () => {
    it("retryConnectionFn invokes eventContext.retryNow", () => {
      renderHook(() => useIssueStoreInstance(), { wrapper });

      // Get the retryConnectionFn that was passed to createIssueStore
      const lastCall =
        mockCreateIssueStore.mock.calls[
          mockCreateIssueStore.mock.calls.length - 1
        ];
      const config = lastCall[0] as any;
      config.retryConnectionFn();

      expect(mockEvent.retryNow).toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 13. Unmount cleanup
  // -----------------------------------------------------------------------

  describe("Unmount cleanup", () => {
    it("calls reset on both stores when unmounting", () => {
      const { unmount } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      issueMethodsRef.current.reset.mockClear();
      agentMethodsRef.current.reset.mockClear();

      unmount();

      expect(issueMethodsRef.current.reset).toHaveBeenCalled();
      expect(agentMethodsRef.current.reset).toHaveBeenCalled();
    });

    it("calls stopPolling on agent store when unmounting", () => {
      const { unmount } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      agentMethodsRef.current.stopPolling.mockClear();

      unmount();

      expect(agentMethodsRef.current.stopPolling).toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 14 & 15. Consumer hooks outside provider
  // -----------------------------------------------------------------------

  describe("Outside provider", () => {
    it("useIssueStoreInstance returns NO_STORE_CONTEXT.issueStore outside provider", () => {
      const { result } = renderHook(() => useIssueStoreInstance());

      expect(result.current).toBe(NO_STORE_CONTEXT.issueStore);
    });

    it("useAgentStoreInstance returns NO_STORE_CONTEXT.agentStore outside provider", () => {
      const { result } = renderHook(() => useAgentStoreInstance());

      expect(result.current).toBe(NO_STORE_CONTEXT.agentStore);
    });
  });

  // -----------------------------------------------------------------------
  // 16. Context value stability
  // -----------------------------------------------------------------------

  describe("Context value stability", () => {
    it("returns the same context reference across re-renders", () => {
      const { result, rerender } = renderHook(
        () => ({
          issueStore: useIssueStoreInstance(),
          agentStore: useAgentStoreInstance(),
        }),
        { wrapper },
      );

      const firstIssue = result.current.issueStore;
      const firstAgent = result.current.agentStore;

      rerender();

      expect(result.current.issueStore).toBe(firstIssue);
      expect(result.current.agentStore).toBe(firstAgent);
    });
  });

  // -----------------------------------------------------------------------
  // 17. Store instances not recreated on re-render
  // -----------------------------------------------------------------------

  describe("Store instances not recreated on re-render", () => {
    it("does not call createIssueStore or createAgentStore again on re-render", () => {
      const { rerender } = renderHook(() => useIssueStoreInstance(), {
        wrapper,
      });

      mockCreateIssueStore.mockClear();
      mockCreateAgentStore.mockClear();

      rerender();
      rerender();
      rerender();

      expect(mockCreateIssueStore).not.toHaveBeenCalled();
      expect(mockCreateAgentStore).not.toHaveBeenCalled();
    });
  });
});
