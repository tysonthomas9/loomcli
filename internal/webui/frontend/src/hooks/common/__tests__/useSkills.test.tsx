/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { createElement, type ReactNode } from "react";
import {
  InvalidatedQueryRegistry,
  InvalidatedQueryRegistryContext,
} from "../invalidatedQueryRegistry";
import { EventContext, NO_EVENT_CONTEXT } from "../useEventProvider";
import type { MutationPayload } from "@/types";
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getSkillCapabilities, listSkills } from "@/api/workspace";
import { skillsStore } from "@/stores/skillsStore";
import {
  useSkillsActions,
  useSkillsCatalog,
  useSkillCapabilities,
} from "../useSkills";

vi.mock("@/api/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/workspace")>();
  return { ...actual, getSkillCapabilities: vi.fn(), listSkills: vi.fn() };
});

const mockCapabilities = vi.mocked(getSkillCapabilities);

function CapabilityProbe({ workspaceId }: { workspaceId: string }) {
  const actions = useSkillsActions(workspaceId);
  return (
    <span>
      {actions.canEdit({ kind: "role", role: "reviewer" })
        ? "editable"
        : "read-only"}
    </span>
  );
}

describe("useSkillsActions", () => {
  it("re-renders when role-edit capabilities arrive after the catalog", async () => {
    mockCapabilities.mockResolvedValue({
      can_edit_role_scope: true,
      workspace_scope: "read_only",
    });
    const workspaceId = "skills-actions-capability-rerender";
    render(<CapabilityProbe workspaceId={workspaceId} />);
    expect(screen.getByText("read-only")).toBeInTheDocument();

    await act(() => skillsStore.loadCapabilities(workspaceId));

    expect(screen.getByText("editable")).toBeInTheDocument();
  });
});

const mockList = vi.mocked(listSkills);
let serial = 0;
describe("shared skills recovery enrollment", () => {
  let registry: InvalidatedQueryRegistry;
  let listeners: Set<(mutation: MutationPayload) => void>;
  let epoch: number;
  let ws: string;
  const subscribe = (listener: (mutation: MutationPayload) => void) => {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  };
  function Wrapper({ children }: { children: ReactNode }) {
    return createElement(
      InvalidatedQueryRegistryContext.Provider,
      { value: registry },
      createElement(
        EventContext.Provider,
        { value: { ...NO_EVENT_CONTEXT, connectionEpoch: epoch, subscribe } },
        children,
      ),
    );
  }
  beforeEach(() => {
    registry = new InvalidatedQueryRegistry();
    listeners = new Set();
    epoch = 0;
    ws = `hook-recovery-${++serial}`;
    mockList.mockReset();
    mockCapabilities.mockReset();
    mockList.mockResolvedValue({ groups: [] });
    mockCapabilities.mockResolvedValue({
      can_edit_role_scope: true,
      workspace_scope: "read_only",
    });
  });
  it("does not load or enroll disabled and action-only consumers", async () => {
    const hook = renderHook(
      () => {
        useSkillsCatalog(ws, false);
        useSkillCapabilities(ws, false);
        useSkillsActions(ws);
      },
      { wrapper: Wrapper },
    );
    await act(async () =>
      registry.refreshForRecovery(new AbortController().signal),
    );
    expect(mockList).not.toHaveBeenCalled();
    expect(mockCapabilities).not.toHaveBeenCalled();
    hook.unmount();
  });
  it("deduplicates enabled consumers and rejects recovery failures", async () => {
    const hook = renderHook(
      () => {
        useSkillsCatalog(ws);
        useSkillsCatalog(ws);
        useSkillCapabilities(ws);
        useSkillCapabilities(ws);
      },
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(skillsStore.catalog(ws).status).toBe("loaded"));
    await waitFor(() =>
      expect(skillsStore.capability(ws).status).toBe("loaded"),
    );
    mockList.mockClear();
    mockCapabilities.mockClear();
    await act(async () =>
      registry.refreshForRecovery(new AbortController().signal),
    );
    expect(mockList).toHaveBeenCalledTimes(1);
    expect(mockCapabilities).toHaveBeenCalledTimes(1);
    mockList.mockRejectedValueOnce(new Error("catalog unavailable"));
    await act(async () => {
      await expect(
        registry.refreshForRecovery(new AbortController().signal),
      ).rejects.toThrow("catalog unavailable");
    });
    hook.unmount();
  });
  it.each(["skill", "role", "skill_pack"])(
    "invalidates catalog after %s changes and reconnect",
    async (entity_type) => {
      const hook = renderHook(() => useSkillsCatalog(ws), { wrapper: Wrapper });
      await waitFor(() =>
        expect(skillsStore.catalog(ws).status).toBe("loaded"),
      );
      mockList.mockClear();
      act(() => {
        for (const listener of listeners)
          listener({
            type: "update",
            entity_type,
            timestamp: "2026-09-05T00:00:00Z",
          });
      });
      await waitFor(() => expect(mockList).toHaveBeenCalledTimes(1));
      epoch++;
      hook.rerender();
      await waitFor(() => expect(mockList).toHaveBeenCalledTimes(2));
      hook.unmount();
    },
  );
  it("fences an old workspace request through A to B to A", async () => {
    const workspaceA = ws;
    let finishOld!: (data: { groups: [] }) => void;
    mockList.mockReturnValueOnce(
      new Promise((resolve) => {
        finishOld = resolve;
      }),
    );
    const hook = renderHook(({ workspace }) => useSkillsCatalog(workspace), {
      wrapper: Wrapper,
      initialProps: { workspace: workspaceA },
    });
    await waitFor(() => expect(mockList).toHaveBeenCalled());
    hook.rerender({ workspace: `${workspaceA}-B` });
    await waitFor(() => expect(hook.result.current.status).toBe("loaded"));
    hook.rerender({ workspace: workspaceA });
    await waitFor(() => expect(hook.result.current.status).toBe("loaded"));
    const revision = hook.result.current.revision;
    await act(async () => finishOld({ groups: [] }));
    expect(hook.result.current.revision).toBe(revision);
    hook.unmount();
  });
  it("withdraws disabled consumers from the recovery registry", async () => {
    const hook = renderHook(({ enabled }) => useSkillsCatalog(ws, enabled), {
      wrapper: Wrapper,
      initialProps: { enabled: true },
    });
    await waitFor(() => expect(skillsStore.catalog(ws).status).toBe("loaded"));
    hook.rerender({ enabled: false });
    mockList.mockClear();
    await act(async () =>
      registry.refreshForRecovery(new AbortController().signal),
    );
    expect(mockList).not.toHaveBeenCalled();
    hook.unmount();
  });
});
