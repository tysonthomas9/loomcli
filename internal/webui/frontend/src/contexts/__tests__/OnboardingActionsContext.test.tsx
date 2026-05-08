/**
 * @vitest-environment jsdom
 */

import { renderHook, render, act } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";

import {
  OnboardingActionsProvider,
  useOnboardingActions,
  useRegisterOnboardingAction,
} from "../OnboardingActionsContext";

function wrapper({ children }: { children: ReactNode }): JSX.Element {
  return <OnboardingActionsProvider>{children}</OnboardingActionsProvider>;
}

describe("OnboardingActionsContext", () => {
  it("dispatches a registered handler", () => {
    const handler = vi.fn();
    const { result } = renderHook(
      () => {
        useRegisterOnboardingAction("open_create_issue", handler);
        return useOnboardingActions();
      },
      { wrapper },
    );

    act(() => result.current.dispatch("open_create_issue"));
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it("falls back when no handler is registered", () => {
    const onUnregistered = vi.fn();
    const Provider = ({ children }: { children: ReactNode }): JSX.Element => (
      <OnboardingActionsProvider onUnregistered={onUnregistered}>
        {children}
      </OnboardingActionsProvider>
    );
    const { result } = renderHook(() => useOnboardingActions(), {
      wrapper: Provider,
    });

    act(() => result.current.dispatch("open_create_agent"));
    expect(onUnregistered).toHaveBeenCalledWith("open_create_agent");
  });

  it("deregisters on unmount", () => {
    const handler = vi.fn();
    const onUnregistered = vi.fn();
    const sharedActions: {
      current: ReturnType<typeof useOnboardingActions> | null;
    } = { current: null };

    function CaptureActions(): null {
      sharedActions.current = useOnboardingActions();
      return null;
    }
    function ChildRegistering(): null {
      useRegisterOnboardingAction("open_create_issue", handler);
      return null;
    }

    const { rerender } = render(
      <OnboardingActionsProvider onUnregistered={onUnregistered}>
        <CaptureActions />
        <ChildRegistering />
      </OnboardingActionsProvider>,
    );

    act(() => sharedActions.current!.dispatch("open_create_issue"));
    expect(handler).toHaveBeenCalledTimes(1);

    rerender(
      <OnboardingActionsProvider onUnregistered={onUnregistered}>
        <CaptureActions />
      </OnboardingActionsProvider>,
    );

    handler.mockClear();
    act(() => sharedActions.current!.dispatch("open_create_issue"));
    expect(handler).not.toHaveBeenCalled();
    expect(onUnregistered).toHaveBeenCalledWith("open_create_issue");
  });

  it("uses the latest handler when re-rendered with a new closure", () => {
    let counter = 0;
    function useTestHooks(): ReturnType<typeof useOnboardingActions> {
      // New closure every render — the hook must keep dispatching to
      // whichever closure is current.
      useRegisterOnboardingAction("start_first_agent", () => {
        counter += 1;
      });
      return useOnboardingActions();
    }

    const { result, rerender } = renderHook(useTestHooks, { wrapper });

    act(() => result.current.dispatch("start_first_agent"));
    expect(counter).toBe(1);

    rerender();
    act(() => result.current.dispatch("start_first_agent"));
    expect(counter).toBe(2);
  });

  it("throws when used outside the provider", () => {
    // Suppress the expected error log during this assertion.
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => renderHook(() => useOnboardingActions())).toThrow(
      /OnboardingActionsProvider/,
    );
    spy.mockRestore();
  });
});
