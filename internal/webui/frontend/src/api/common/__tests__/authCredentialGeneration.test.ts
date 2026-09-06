import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  getAuthCredentialGeneration,
  getAuthState,
  getAuthToken,
  onAuthCredentialChange,
  setAuthToken,
} from "../client";

describe("auth credential generation", () => {
  const cleanups: Array<() => void> = [];
  const listen = (callback: (generation: number) => void) => {
    const stop = onAuthCredentialChange(callback);
    cleanups.push(stop);
    return stop;
  };

  beforeEach(() => setAuthToken(null));
  afterEach(() => {
    for (const cleanup of cleanups.splice(0)) cleanup();
    setAuthToken(null);
    vi.restoreAllMocks();
  });

  it("notifies synchronously only for actual changes, including replacement and clear", () => {
    const start = getAuthCredentialGeneration();
    const listener = vi.fn();
    listen(listener);
    setAuthToken(null);
    expect(listener).not.toHaveBeenCalled();
    setAuthToken("credential-a");
    expect(listener).toHaveBeenLastCalledWith(start + 1);
    setAuthToken("credential-a");
    expect(listener).toHaveBeenCalledTimes(1);
    setAuthToken("credential-b");
    expect(listener).toHaveBeenLastCalledWith(start + 2);
    expect(getAuthState()).toBe("authenticated");
    setAuthToken(null);
    expect(listener.mock.calls).toEqual([
      [start + 1],
      [start + 2],
      [start + 3],
    ]);
    expect(getAuthCredentialGeneration()).toBe(start + 3);
    expect(getAuthState()).toBe("none");
  });

  it("isolates throwing observers and never reports their potentially secret error", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    listen(() => {
      throw new Error("credential-a");
    });
    const listener = vi.fn();
    listen(listener);
    expect(() => setAuthToken("credential-a")).not.toThrow();
    expect(listener).toHaveBeenCalledExactlyOnceWith(
      getAuthCredentialGeneration(),
    );
    expect(error).not.toHaveBeenCalled();
    expect(warn).not.toHaveBeenCalled();
    expect(getAuthState()).toBe("authenticated");
  });

  it("does not restore stale authenticated state or notify stale generations after reentrant clear", () => {
    const start = getAuthCredentialGeneration();
    listen(() => {
      if (getAuthToken() !== null) setAuthToken(null);
    });
    const listener = vi.fn();
    listen(listener);
    setAuthToken("credential-a");
    expect(getAuthToken()).toBeNull();
    expect(getAuthState()).toBe("none");
    expect(listener.mock.calls).toEqual([[start + 2]]);
  });

  it("does not restore stale signed-out state after reentrant replacement", () => {
    setAuthToken("credential-a");
    listen(() => {
      if (getAuthToken() === null) setAuthToken("credential-b");
    });
    setAuthToken(null);
    expect(getAuthToken()).toBe("credential-b");
    expect(getAuthState()).toBe("authenticated");
  });

  it("honors unsubscribe during notification and keeps duplicate registrations independent", () => {
    const skipped = vi.fn();
    let stopSkipped = () => {};
    listen(() => stopSkipped());
    stopSkipped = listen(skipped);
    const duplicate = vi.fn();
    const stopFirst = listen(duplicate);
    listen(duplicate);
    stopFirst();
    stopFirst();
    setAuthToken("credential-a");
    expect(skipped).not.toHaveBeenCalled();
    expect(duplicate).toHaveBeenCalledTimes(1);
  });
});
