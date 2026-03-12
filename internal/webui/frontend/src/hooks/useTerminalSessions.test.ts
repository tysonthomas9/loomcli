/**
 * @vitest-environment jsdom
 */
import { renderHook, waitFor, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useTerminalSessions } from "./useTerminalSessions";
import * as terminalApi from "../api/terminal";
import type { TerminalSessionInfo } from "../api/terminal";

// Mock the terminal API
vi.mock("../api/terminal", () => ({
  listTerminalSessions: vi.fn(),
}));

const DEFAULT_SESSIONS: TerminalSessionInfo[] = [
  { name: "talk-to-lead", label: "talk-to-lead", created: 0 },
];

describe("useTerminalSessions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns DEFAULT_SESSIONS as initial value before API resolves", () => {
    vi.mocked(terminalApi.listTerminalSessions).mockReturnValue(
      new Promise(() => {}), // never resolves
    );

    const { result } = renderHook(() => useTerminalSessions());

    expect(result.current.sessions).toEqual(DEFAULT_SESSIONS);
    expect(result.current.isLoading).toBe(true);
    expect(result.current.error).toBeNull();
  });

  it("loads sessions from API on mount", async () => {
    const apiSessions: TerminalSessionInfo[] = [
      { name: "session-1", label: "Session 1", created: 1000 },
      { name: "session-2", label: "Session 2", created: 2000 },
    ];
    vi.mocked(terminalApi.listTerminalSessions).mockResolvedValue(apiSessions);

    const { result } = renderHook(() => useTerminalSessions());

    // Starts loading
    expect(result.current.isLoading).toBe(true);

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.sessions).toEqual(apiSessions);
    expect(result.current.error).toBeNull();
    expect(terminalApi.listTerminalSessions).toHaveBeenCalledTimes(1);
  });

  it("falls back to DEFAULT_SESSIONS on API error", async () => {
    vi.mocked(terminalApi.listTerminalSessions).mockRejectedValue(
      new Error("network failure"),
    );

    const { result } = renderHook(() => useTerminalSessions());

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    // Sessions remain as default (not replaced on error)
    expect(result.current.sessions).toEqual(DEFAULT_SESSIONS);
    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("network failure");
  });

  it("falls back to DEFAULT_SESSIONS when API returns empty array", async () => {
    vi.mocked(terminalApi.listTerminalSessions).mockResolvedValue([]);

    const { result } = renderHook(() => useTerminalSessions());

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.sessions).toEqual(DEFAULT_SESSIONS);
    expect(result.current.error).toBeNull();
  });

  it("sets isLoading to true during fetch and false when complete", async () => {
    let resolvePromise!: (value: TerminalSessionInfo[]) => void;
    vi.mocked(terminalApi.listTerminalSessions).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolvePromise = resolve;
        }),
    );

    const { result } = renderHook(() => useTerminalSessions());

    expect(result.current.isLoading).toBe(true);

    resolvePromise([{ name: "s1", label: "S1", created: 100 }]);

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.sessions).toEqual([
      { name: "s1", label: "S1", created: 100 },
    ]);
  });

  it("sets error state on non-Error thrown value", async () => {
    vi.mocked(terminalApi.listTerminalSessions).mockRejectedValue(
      "string error",
    );

    const { result } = renderHook(() => useTerminalSessions());

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("string error");
  });

  it("refetch reloads sessions from API", async () => {
    const initialSessions: TerminalSessionInfo[] = [
      { name: "session-1", label: "Session 1", created: 1000 },
    ];
    const updatedSessions: TerminalSessionInfo[] = [
      { name: "session-1", label: "Session 1", created: 1000 },
      { name: "session-2", label: "Session 2", created: 2000 },
    ];

    vi.mocked(terminalApi.listTerminalSessions)
      .mockResolvedValueOnce(initialSessions)
      .mockResolvedValueOnce(updatedSessions);

    const { result } = renderHook(() => useTerminalSessions());

    // Wait for initial load
    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.sessions).toEqual(initialSessions);

    // Trigger refetch
    await act(async () => {
      await result.current.refetch();
    });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.sessions).toEqual(updatedSessions);
    expect(terminalApi.listTerminalSessions).toHaveBeenCalledTimes(2);
  });

  it("refetch recovers from previous error", async () => {
    vi.mocked(terminalApi.listTerminalSessions)
      .mockRejectedValueOnce(new Error("first call fails"))
      .mockResolvedValueOnce([
        { name: "recovered", label: "Recovered", created: 500 },
      ]);

    const { result } = renderHook(() => useTerminalSessions());

    // Wait for initial (failed) load
    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.error).not.toBeNull();

    // Refetch successfully
    await act(async () => {
      await result.current.refetch();
    });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.sessions).toEqual([
      { name: "recovered", label: "Recovered", created: 500 },
    ]);
    expect(result.current.error).toBeNull();
  });
});
