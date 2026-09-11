/** @vitest-environment jsdom */
import { describe, expect, it, vi } from "vitest";
import { InvalidatedQueryRegistry } from "../invalidatedQueryRegistry";
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}
function query(registry: InvalidatedQueryRegistry, key: string) {
  const requests: ReturnType<typeof deferred<string>>[] = [];
  const signals: AbortSignal[] = [];
  const fetcher = vi.fn((signal: AbortSignal) => {
    const request = deferred<string>();
    requests.push(request);
    signals.push(signal);
    return request.promise;
  });
  const registration = registry.register(key, fetcher, { key }, 0);
  registration.commit(fetcher);
  registration.setEnabled(true);
  return { registration, requests, signals, fetcher };
}
describe("strict registry recovery", () => {
  it("requires a fresh committed response and ignores the pre-fence request", async () => {
    const registry = new InvalidatedQueryRegistry();
    const q = query(registry, "a");
    const done = vi.fn();
    const result = registry
      .refreshForRecovery(new AbortController().signal)
      .then(done);
    expect(q.requests).toHaveLength(2);
    expect(q.signals[0].aborted).toBe(true);
    q.requests[0].resolve("old");
    await flush();
    expect(done).not.toHaveBeenCalled();
    expect(q.registration.getSnapshot().data).toBeNull();
    q.requests[1].resolve("fresh");
    await result;
    expect(q.registration.getSnapshot().data).toBe("fresh");
    q.registration.dispose();
  });
  it.each([
    new Error("read failed"),
    new DOMException("aborted", "AbortError"),
    null,
  ])("rejects actual failure %s", async (error) => {
    const registry = new InvalidatedQueryRegistry();
    const q = query(registry, "a");
    const result = registry.refreshForRecovery(new AbortController().signal);
    const assertion = expect(result).rejects.toBeDefined();
    q.requests[1].reject(error);
    await assertion;
    q.registration.dispose();
  });
  it("joins new committed enabled entries while another member is pending", async () => {
    const registry = new InvalidatedQueryRegistry();
    const a = query(registry, "a");
    const done = vi.fn();
    const result = registry
      .refreshForRecovery(new AbortController().signal)
      .then(done);
    const b = query(registry, "b");
    expect(b.requests).toHaveLength(2);
    a.requests[1].resolve("a");
    await flush();
    expect(done).not.toHaveBeenCalled();
    b.requests[1].resolve("b");
    await result;
    a.registration.dispose();
    b.registration.dispose();
  });
  it("withdraws inactive entries and requires a new request on re-enable", async () => {
    const registry = new InvalidatedQueryRegistry();
    const a = query(registry, "a");
    const b = query(registry, "b");
    const done = vi.fn();
    const result = registry
      .refreshForRecovery(new AbortController().signal)
      .then(done);
    a.registration.setEnabled(false);
    expect(a.signals[1].aborted).toBe(true);
    a.registration.setEnabled(true);
    expect(a.requests).toHaveLength(4);
    a.requests[1].resolve("withdrawn");
    b.requests[1].resolve("b");
    await flush();
    expect(done).not.toHaveBeenCalled();
    a.requests[3].resolve("active");
    await result;
    expect(a.registration.getSnapshot().data).toBe("active");
    a.registration.dispose();
    b.registration.dispose();
  });
  it("withdraws disposed entries without calling their request successful", async () => {
    const registry = new InvalidatedQueryRegistry();
    const a = query(registry, "a");
    const b = query(registry, "b");
    const done = vi.fn();
    const result = registry
      .refreshForRecovery(new AbortController().signal)
      .then(done);
    a.registration.dispose();
    expect(a.signals[1].aborted).toBe(true);
    await flush();
    expect(done).not.toHaveBeenCalled();
    b.requests[1].resolve("b");
    await result;
    b.registration.dispose();
  });
  it("rejects supersession and explicit abort and ignores late responses", async () => {
    const registry = new InvalidatedQueryRegistry();
    const q = query(registry, "a");
    const first = registry.refreshForRecovery(new AbortController().signal);
    const assertion = expect(first).rejects.toMatchObject({
      name: "AbortError",
    });
    const controller = new AbortController();
    const second = registry.refreshForRecovery(controller.signal);
    const secondAssertion = expect(second).rejects.toMatchObject({
      name: "AbortError",
    });
    expect(q.signals[1].aborted).toBe(true);
    controller.abort();
    await assertion;
    await secondAssertion;
    q.requests[2].resolve("stale");
    await flush();
    expect(q.registration.getSnapshot().data).toBeNull();
    q.registration.dispose();
  });
  it("does not require render-only or disabled registrations", async () => {
    const registry = new InvalidatedQueryRegistry();
    const fetcher = vi.fn();
    const registration = registry.register("a", fetcher, { key: "a" }, 0);
    await registry.refreshForRecovery(new AbortController().signal);
    expect(fetcher).not.toHaveBeenCalled();
    registration.dispose();
  });
  it("requires fresh work after disposal and revival under the same key", async () => {
    const registry = new InvalidatedQueryRegistry();
    const a = query(registry, "a");
    const b = query(registry, "b");
    const done = vi.fn();
    const result = registry
      .refreshForRecovery(new AbortController().signal)
      .then(done);
    a.registration.dispose();
    a.registration.revive(a.fetcher, 0);
    a.registration.setEnabled(true);
    expect(a.requests).toHaveLength(4);
    b.requests[1].resolve("b");
    a.requests[1].resolve("old identity");
    await flush();
    expect(done).not.toHaveBeenCalled();
    a.requests[3].resolve("new identity");
    await result;
    expect(a.registration.getSnapshot().data).toBe("new identity");
    a.registration.dispose();
    b.registration.dispose();
  });

  it("rejects a required entry whose committed fetcher is missing", async () => {
    const registry = new InvalidatedQueryRegistry();
    const q = query(registry, "a");
    q.registration.commit(undefined as unknown as typeof q.fetcher);
    await expect(
      registry.refreshForRecovery(new AbortController().signal),
    ).rejects.toThrow("No committed enabled fetcher");
    q.registration.dispose();
  });

  it("shares one recovery request for equal-key active registrations", async () => {
    const registry = new InvalidatedQueryRegistry();
    const a = query(registry, "same");
    const b = query(registry, "same");
    const result = registry.refreshForRecovery(new AbortController().signal);
    expect(a.requests).toHaveLength(2);
    expect(b.requests).toHaveLength(0);
    a.requests[1].resolve("shared");
    await result;
    expect(b.registration.getSnapshot().data).toBe("shared");
    a.registration.dispose();
    b.registration.dispose();
  });
  it("revises required membership while idle without revising stable commits or completion", async () => {
    const registry = new InvalidatedQueryRegistry();
    expect(registry.getRecoveryRevision()).toBe(0);
    const q = query(registry, "a");
    const enabled = registry.getRecoveryRevision();
    expect(enabled).toBeGreaterThan(0);
    q.registration.commit(q.fetcher);
    q.registration.revive(q.fetcher, 1);
    q.registration.setEnabled(true);
    expect(registry.getRecoveryRevision()).toBe(enabled);
    const recovery = registry.refreshForRecovery(new AbortController().signal);
    q.requests[1].resolve("fresh");
    await recovery;
    expect(registry.getRecoveryRevision()).toBe(enabled);
    q.registration.setEnabled(false);
    expect(registry.getRecoveryRevision()).toBe(enabled + 1);
    q.registration.setEnabled(true);
    expect(registry.getRecoveryRevision()).toBe(enabled + 2);
    q.registration.dispose();
    expect(registry.getRecoveryRevision()).toBe(enabled + 3);
    q.registration.revive(q.fetcher, 1);
    expect(registry.getRecoveryRevision()).toBe(enabled + 3);
    q.registration.setEnabled(true);
    expect(registry.getRecoveryRevision()).toBe(enabled + 4);
    const shared = query(registry, "a");
    expect(registry.getRecoveryRevision()).toBe(enabled + 5);
    shared.registration.dispose();
    expect(registry.getRecoveryRevision()).toBe(enabled + 6);
    q.registration.dispose();
  });
});
