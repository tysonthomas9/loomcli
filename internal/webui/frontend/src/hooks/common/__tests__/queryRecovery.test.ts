import { describe, expect, it, vi } from "vitest";
import { QueryRecoveryCoordinator } from "../queryRecovery";
import { InvalidatedQueryRegistry } from "../invalidatedQueryRegistry";

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

describe("QueryRecoveryCoordinator", () => {
  it("requires successful refreshes and propagates failure", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const one = deferred();
    const two = deferred();
    coordinator.register("issues", () => one.promise);
    coordinator.register("agents", () => two.promise);
    const result = coordinator.refresh();
    const failure = expect(result).rejects.toThrow("offline");
    one.resolve();
    two.reject(new Error("offline"));
    await failure;
  });

  it("joins repeated signals and enrolls queries added during recovery", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const one = deferred();
    const two = deferred();
    const refresh = vi.fn(() => one.promise);
    coordinator.register("issues", refresh);
    const result = coordinator.refresh();
    expect(coordinator.refresh()).toBe(result);
    await Promise.resolve();
    coordinator.register("new query", () => two.promise);
    const finished = vi.fn();
    void result.then(finished);
    one.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(finished).not.toHaveBeenCalled();
    two.resolve();
    await result;
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("cancels old scope and ignores its late success", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const old = deferred();
    let signal: AbortSignal | undefined;
    const remove = coordinator.register("old", (value) => {
      signal = value;
      return old.promise;
    });
    const result = coordinator.refresh();
    const rejected = expect(result).rejects.toMatchObject({
      name: "AbortError",
    });
    await Promise.resolve();
    coordinator.cancel();
    expect(signal?.aborted).toBe(true);
    remove();
    const next = deferred();
    coordinator.register("new", () => next.promise);
    const current = coordinator.refresh();
    const finished = vi.fn();
    void current.then(finished);
    old.resolve();
    await rejected;
    expect(finished).not.toHaveBeenCalled();
    next.resolve();
    await current;
  });

  it("withdraws unmounted participants and requires remount identity", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const old = deferred();
    const remove = coordinator.register("query", () => old.promise);
    const result = coordinator.refresh();
    await Promise.resolve();
    remove();
    const next = deferred();
    coordinator.register("query", () => next.promise);
    const finished = vi.fn();
    void result.then(finished);
    old.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(finished).not.toHaveBeenCalled();
    next.resolve();
    await result;
  });
  it("rechecks a completed registry when a new query joins before stores finish", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const registry = new InvalidatedQueryRegistry();
    const agents = deferred();
    coordinator.register(
      "registry",
      (signal) => registry.refreshForRecovery(signal),
      () => registry.getRecoveryRevision(),
    );
    coordinator.register("agents", () => agents.promise);
    const result = coordinator.refresh();
    const rejected = expect(result).rejects.toThrow("new query failed");
    // Let the empty aggregate finish while the agent request is outstanding.
    for (let i = 0; i < 10; i++) await Promise.resolve();
    const fetcher = async () => {
      throw new Error("new query failed");
    };
    const query = registry.register("new", fetcher, { key: "new" }, 0);
    query.commit(fetcher);
    query.setEnabled(true);
    agents.resolve();
    await rejected;
    query.dispose();
  });
});
