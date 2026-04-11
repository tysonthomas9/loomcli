/**
 * @vitest-environment jsdom
 */
import React from "react";
import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import {
  IssueSessionProvider,
  useHasActiveSession,
} from "../IssueSessionContext";
import type { UseIssueSessionMapReturn } from "@/hooks/agents";

function createMockValue(
  overrides: Partial<UseIssueSessionMapReturn> = {},
): UseIssueSessionMapReturn {
  return {
    issueSessionMap: {},
    hasActiveSession: vi.fn(() => false),
    refetch: vi.fn(),
    handleMutation: vi.fn(),
    ...overrides,
  };
}

describe("IssueSessionContext", () => {
  describe("useHasActiveSession", () => {
    it("returns default false when used outside provider", () => {
      const { result } = renderHook(() => useHasActiveSession());

      // Default context value returns false for any input
      expect(result.current("any-issue")).toBe(false);
    });

    it("delegates to the provided hasActiveSession function", () => {
      const mockHasActive = vi.fn((id: string) => id === "PROJ-1");
      const value = createMockValue({ hasActiveSession: mockHasActive });

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <IssueSessionProvider value={value}>{children}</IssueSessionProvider>
      );

      const { result } = renderHook(() => useHasActiveSession(), { wrapper });

      expect(result.current("PROJ-1")).toBe(true);
      expect(result.current("PROJ-2")).toBe(false);
      expect(mockHasActive).toHaveBeenCalledWith("PROJ-1");
      expect(mockHasActive).toHaveBeenCalledWith("PROJ-2");
    });

    it("reflects provider value for different issue IDs", () => {
      const mockHasActive = vi.fn((id: string) => id === "active-issue");
      const value = createMockValue({ hasActiveSession: mockHasActive });

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <IssueSessionProvider value={value}>{children}</IssueSessionProvider>
      );

      const { result } = renderHook(() => useHasActiveSession(), {
        wrapper,
      });

      expect(result.current("active-issue")).toBe(true);
      expect(result.current("inactive-issue")).toBe(false);
      expect(mockHasActive).toHaveBeenCalledTimes(2);
    });
  });

  describe("IssueSessionProvider", () => {
    it("passes hasActiveSession from value prop to context", () => {
      const hasActive = vi.fn().mockReturnValue(true);
      const value = createMockValue({ hasActiveSession: hasActive });

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <IssueSessionProvider value={value}>{children}</IssueSessionProvider>
      );

      const { result } = renderHook(() => useHasActiveSession(), { wrapper });

      const isActive = result.current("test-issue");
      expect(isActive).toBe(true);
      expect(hasActive).toHaveBeenCalledWith("test-issue");
    });
  });
});
