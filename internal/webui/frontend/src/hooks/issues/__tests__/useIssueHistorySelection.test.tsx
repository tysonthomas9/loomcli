/** @vitest-environment jsdom */
import { Suspense, startTransition, useState, type ReactNode } from "react";
import { act, cleanup, render, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  IssueRecoverySelectionContext,
  IssueRecoverySelectionRegistry,
} from "@/hooks/common/issueRecoverySelection";
import { useIssueHistory } from "../useIssueHistory";
const scope = vi.hoisted(() => ({ workspace: "WS" }));
vi.mock("@/api", () => ({ getIssueEvents: vi.fn().mockResolvedValue([]) }));
vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: scope.workspace }),
}));
vi.mock("@/hooks/common/useEventProvider", () => ({
  useEventContext: () => ({ subscribe: () => () => {}, connectionEpoch: 0 }),
}));
beforeEach(() => {
  scope.workspace = "WS";
});
afterEach(cleanup);
function wrapper(registry: IssueRecoverySelectionRegistry) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <IssueRecoverySelectionContext.Provider value={registry}>
        {children}
      </IssueRecoverySelectionContext.Provider>
    );
  };
}
it("enrolls committed scope and preserves leases across detail revisions", async () => {
  const registry = new IssueRecoverySelectionRegistry();
  const hook = renderHook(
    ({ id, enabled, revision }) => useIssueHistory(id, revision, enabled),
    {
      wrapper: wrapper(registry),
      initialProps: { id: "A", enabled: true, revision: 1 },
    },
  );
  const lease = registry.capture("WS");
  expect(lease.issueId).toBe("A");
  hook.rerender({ id: "A", enabled: true, revision: 2 });
  await act(async () => {});
  expect(lease.isCurrent()).toBe(true);
  hook.rerender({ id: "B", enabled: true, revision: 2 });
  expect(lease.signal.aborted).toBe(true);
  const b = registry.capture("WS");
  expect(b.issueId).toBe("B");
  hook.rerender({ id: "A", enabled: false, revision: 2 });
  expect(b.signal.aborted).toBe(true);
  expect(registry.capture("WS").issueId).toBeUndefined();
  hook.rerender({ id: "A", enabled: true, revision: 2 });
  const again = registry.capture("WS");
  expect(again.issueId).toBe("A");
  expect(lease.isCurrent()).toBe(false);
  scope.workspace = "OTHER";
  hook.rerender({ id: "A", enabled: true, revision: 2 });
  expect(again.signal.aborted).toBe(true);
  expect(registry.capture("WS").issueId).toBeUndefined();
  expect(registry.capture("OTHER").issueId).toBe("A");
  await act(async () => {});
});
it("does not register a suspended speculative selection", async () => {
  const registry = new IssueRecoverySelectionRegistry();
  let change!: (id: string) => void;
  const never = new Promise<void>(() => {});
  function Panel({ id }: { id: string }) {
    useIssueHistory(id);
    if (id === "B") throw never;
    return <span>{id}</span>;
  }
  function App() {
    const [id, setId] = useState("A");
    change = setId;
    return (
      <Suspense fallback="waiting">
        <Panel id={id} />
      </Suspense>
    );
  }
  render(<App />, { wrapper: wrapper(registry) });
  await act(async () => {});
  const lease = registry.capture("WS");
  await act(async () => {
    startTransition(() => change("B"));
  });
  expect(lease.isCurrent()).toBe(true);
  expect(lease.issueId).toBe("A");
});
