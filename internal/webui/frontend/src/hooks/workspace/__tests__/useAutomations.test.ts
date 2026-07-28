// @vitest-environment jsdom

import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentRecordSummary } from "@/api/agents";
import type { TriggerBinding } from "@/api/workflows";

import { dispatchBindingsChanged, useAutomations } from "../useAutomations";

const mocks = vi.hoisted(() => ({
  createTriggerBinding: vi.fn(),
  deleteAgentRecord: vi.fn(),
  deleteTriggerBinding: vi.fn(),
  listAgentRecords: vi.fn(),
  listTriggerBindings: vi.fn(),
  listWorkflows: vi.fn(),
  runTriggerBinding: vi.fn(),
  setAgentRecordEnabled: vi.fn(),
  setTriggerBindingEnabled: vi.fn(),
  startWorkflowRun: vi.fn(),
  updateAgentRecord: vi.fn(),
  updateTriggerBinding: vi.fn(),
}));

vi.mock("@/api/agents", () => ({
  deleteAgentRecord: mocks.deleteAgentRecord,
  listAgentRecords: mocks.listAgentRecords,
  setAgentRecordEnabled: mocks.setAgentRecordEnabled,
  updateAgentRecord: mocks.updateAgentRecord,
}));

vi.mock("@/api/workflows", () => ({
  createTriggerBinding: mocks.createTriggerBinding,
  deleteTriggerBinding: mocks.deleteTriggerBinding,
  listTriggerBindings: mocks.listTriggerBindings,
  listWorkflows: mocks.listWorkflows,
  runTriggerBinding: mocks.runTriggerBinding,
  setTriggerBindingEnabled: mocks.setTriggerBindingEnabled,
  startWorkflowRun: mocks.startWorkflowRun,
  updateTriggerBinding: mocks.updateTriggerBinding,
}));

const attachedPromptBinding: TriggerBinding = {
  workspace_key: "WS",
  binding_id: "prompt-binding",
  name: "Prompt agent",
  source_kind: "cron",
  route_key: "prompt-binding",
  driver_id: "prompt-agent",
  driver_version_id: "prompt-v1",
  target_agent_service_id: "agent-prompt-1",
  schedule: "*/10 * * * *",
  enabled: true,
};

const attachedScriptedBinding: TriggerBinding = {
  workspace_key: "WS",
  binding_id: "scripted-binding",
  name: "Scripted agent",
  source_kind: "internal",
  route_key: "scripted-binding",
  driver_id: "scripted-agent",
  driver_version_id: "scripted-v1",
  target_agent_service_id: "agent-scripted-1",
  enabled: true,
};

const legacyBinding: TriggerBinding = {
  workspace_key: "WS",
  binding_id: "legacy-binding",
  name: "Legacy binding",
  source_kind: "cron",
  route_key: "legacy-binding",
  driver_id: "legacy-driver",
  driver_version_id: "legacy-v1",
  schedule: "0 * * * *",
  enabled: true,
};

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T | PromiseLike<T>) => void;
} {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function durableRecord(id: string, workspaceId = "WS"): AgentRecordSummary {
  return {
    id,
    name: id,
    kind: "prompt",
    enabled: true,
    behavior: { role_name: "reviewer" },
    workspace_key: workspaceId,
  };
}

async function renderAutomations(bindings: TriggerBinding[]) {
  mocks.listTriggerBindings.mockResolvedValue(bindings);
  mocks.listAgentRecords.mockResolvedValue(
    bindings
      .filter((binding) => binding.target_agent_service_id)
      .map((binding) => ({
        id: binding.target_agent_service_id,
        name: binding.name,
        kind: binding.driver_id === "scripted-agent" ? "scripted" : "prompt",
      })),
  );
  const rendered = renderHook(() => useAutomations("WS", true));
  await waitFor(() => expect(rendered.result.current.initialized).toBe(true));
  return rendered;
}

describe("useAutomations durable agent lifecycle dispatch", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.listWorkflows.mockResolvedValue([]);
    mocks.updateAgentRecord.mockImplementation(
      async (_workspaceId: string, id: string, req: { name?: string }) => ({
        id,
        name: req.name ?? id,
        kind: "prompt",
        enabled: true,
        behavior: { role_name: "reviewer" },
        workspace_key: "WS",
      }),
    );
    mocks.updateTriggerBinding.mockImplementation(
      async (
        _workspaceId: string,
        bindingId: string,
        req: Partial<TriggerBinding>,
      ) => {
        const current = [
          attachedPromptBinding,
          attachedScriptedBinding,
          legacyBinding,
        ].find((binding) => binding.binding_id === bindingId);
        return { ...current, ...req };
      },
    );
    mocks.deleteAgentRecord.mockResolvedValue({ archived: true });
    mocks.deleteTriggerBinding.mockResolvedValue({ deleted: true });
  });

  it("renames an attached prompt agent through its durable AgentService record", async () => {
    const { result } = await renderAutomations([attachedPromptBinding]);
    mocks.listAgentRecords.mockResolvedValue([
      {
        id: "agent-prompt-1",
        name: "Renamed prompt agent",
        kind: "prompt",
      },
    ]);

    let updated: TriggerBinding | undefined;
    await act(async () => {
      updated = await result.current.updateBinding("prompt-binding", {
        name: "Renamed prompt agent",
      });
    });

    expect(mocks.updateAgentRecord).toHaveBeenCalledWith(
      "WS",
      "agent-prompt-1",
      { name: "Renamed prompt agent" },
    );
    expect(mocks.updateTriggerBinding).not.toHaveBeenCalled();
    expect(updated?.name).toBe("Renamed prompt agent");
    await waitFor(() =>
      expect(result.current.bindings[0]?.name).toBe("Renamed prompt agent"),
    );
  });

  it("routes attached name and schedule edits as separate agent-owned patches", async () => {
    const { result } = await renderAutomations([attachedPromptBinding]);

    await act(async () => {
      await result.current.updateBinding("prompt-binding", {
        name: "Daily prompt agent",
      });
      await result.current.updateBinding("prompt-binding", {
        schedule: "0 9 * * *",
        schedule_timezone: "UTC",
      });
    });

    expect(mocks.updateTriggerBinding).not.toHaveBeenCalled();
    expect(mocks.updateAgentRecord).toHaveBeenNthCalledWith(
      1,
      "WS",
      "agent-prompt-1",
      { name: "Daily prompt agent" },
    );
    expect(mocks.updateAgentRecord).toHaveBeenNthCalledWith(
      2,
      "WS",
      "agent-prompt-1",
      {
        binding_id: "prompt-binding",
        schedule: "0 9 * * *",
        schedule_timezone: "UTC",
      },
    );
  });

  it("routes an attached schedule-only edit through the owning agent record", async () => {
    const { result } = await renderAutomations([attachedPromptBinding]);

    await act(async () => {
      await result.current.updateBinding("prompt-binding", {
        schedule: "30 8 * * 1-5",
      });
    });

    expect(mocks.updateAgentRecord).toHaveBeenCalledWith(
      "WS",
      "agent-prompt-1",
      {
        binding_id: "prompt-binding",
        schedule: "30 8 * * 1-5",
      },
    );
    expect(mocks.updateTriggerBinding).not.toHaveBeenCalled();
  });

  it("rejects a mixed managed name and schedule patch before any request", async () => {
    const { result } = await renderAutomations([attachedPromptBinding]);

    await expect(
      result.current.updateBinding("prompt-binding", {
        name: "Daily prompt agent",
        schedule: "0 9 * * *",
      }),
    ).rejects.toThrow("Save the agent name and cadence separately.");

    expect(mocks.updateAgentRecord).not.toHaveBeenCalled();
    expect(mocks.updateTriggerBinding).not.toHaveBeenCalled();
  });

  it("returns the authoritative managed binding from the agent response", async () => {
    mocks.updateAgentRecord.mockResolvedValueOnce({
      id: "agent-prompt-1",
      name: "Prompt agent",
      kind: "prompt",
      enabled: true,
      behavior: { role_name: "reviewer" },
      workspace_key: "WS",
      bindings: [
        {
          ...attachedPromptBinding,
          schedule: "0 9 * * *",
          created_at: "2026-07-28T12:00:00Z",
          updated_at: "2026-07-28T12:05:00Z",
          concurrency_policy: "one_active_per_epic",
        },
      ],
    });
    const { result } = await renderAutomations([attachedPromptBinding]);

    let updated: TriggerBinding | undefined;
    await act(async () => {
      updated = await result.current.updateBinding("prompt-binding", {
        schedule: " 0 9 * * * ",
      });
    });

    expect(updated).toEqual(
      expect.objectContaining({
        schedule: "0 9 * * *",
        updated_at: "2026-07-28T12:05:00Z",
      }),
    );
  });

  it("deletes an attached scripted agent through its durable AgentService record", async () => {
    const { result } = await renderAutomations([attachedScriptedBinding]);

    await act(async () => {
      await result.current.deleteBinding("scripted-binding");
    });

    expect(mocks.deleteAgentRecord).toHaveBeenCalledWith(
      "WS",
      "agent-scripted-1",
    );
    expect(mocks.deleteTriggerBinding).not.toHaveBeenCalled();
  });

  it("exposes record-scoped enable and archive actions for orphan recovery", async () => {
    const { result } = await renderAutomations([]);

    await act(async () => {
      await result.current.setRecordEnabled("orphan-agent", true);
      await result.current.deleteRecord("orphan-agent");
    });

    expect(mocks.setAgentRecordEnabled).toHaveBeenCalledWith(
      "WS",
      "orphan-agent",
      true,
    );
    expect(mocks.deleteAgentRecord).toHaveBeenCalledWith("WS", "orphan-agent");
  });

  it("routes an attached binding toggle through its durable agent record", async () => {
    const { result } = await renderAutomations([attachedPromptBinding]);

    await act(async () => {
      await result.current.setEnabled("prompt-binding", false);
    });

    expect(mocks.setAgentRecordEnabled).toHaveBeenCalledWith(
      "WS",
      "agent-prompt-1",
      false,
    );
    expect(mocks.setTriggerBindingEnabled).not.toHaveBeenCalled();
  });

  it("uses record names for attached agents after a fresh reload", async () => {
    mocks.listAgentRecords.mockResolvedValueOnce([
      {
        id: "agent-prompt-1",
        name: "Durable renamed agent",
        kind: "prompt",
      },
    ]);
    mocks.listTriggerBindings.mockResolvedValueOnce([
      attachedPromptBinding,
      legacyBinding,
    ]);

    const { result } = renderHook(() => useAutomations("WS", true));
    await waitFor(() => expect(result.current.initialized).toBe(true));

    expect(result.current.bindings).toEqual([
      expect.objectContaining({
        binding_id: "prompt-binding",
        name: "Durable renamed agent",
      }),
      expect.objectContaining({
        binding_id: "legacy-binding",
        name: "Legacy binding",
      }),
    ]);
    expect(result.current.agentRecords).toEqual([
      {
        id: "agent-prompt-1",
        name: "Durable renamed agent",
        kind: "prompt",
      },
    ]);
  });

  it("preserves direct binding rename and delete for legacy unattached bindings", async () => {
    const { result } = await renderAutomations([legacyBinding]);

    await act(async () => {
      await result.current.updateBinding("legacy-binding", {
        name: "Renamed legacy binding",
      });
      await result.current.deleteBinding("legacy-binding");
    });

    expect(mocks.updateTriggerBinding).toHaveBeenCalledWith(
      "WS",
      "legacy-binding",
      { name: "Renamed legacy binding" },
    );
    expect(mocks.deleteTriggerBinding).toHaveBeenCalledWith(
      "WS",
      "legacy-binding",
    );
    expect(mocks.updateAgentRecord).not.toHaveBeenCalled();
    expect(mocks.deleteAgentRecord).not.toHaveBeenCalled();
  });

  it("does not let a deferred initial snapshot overwrite a newer binding-change refresh", async () => {
    const initialBindings = deferred<TriggerBinding[]>();
    const initialRecords = deferred<AgentRecordSummary[]>();
    const refreshedRecord = durableRecord("agent-refreshed");
    const refreshedBinding: TriggerBinding = {
      ...attachedPromptBinding,
      binding_id: "binding-refreshed",
      route_key: "binding-refreshed",
      target_agent_service_id: refreshedRecord.id,
    };
    mocks.listTriggerBindings
      .mockReturnValueOnce(initialBindings.promise)
      .mockResolvedValueOnce([refreshedBinding]);
    mocks.listAgentRecords
      .mockReturnValueOnce(initialRecords.promise)
      .mockResolvedValueOnce([refreshedRecord]);

    const { result } = renderHook(() => useAutomations("WS", true));
    await waitFor(() =>
      expect(mocks.listTriggerBindings).toHaveBeenCalledTimes(1),
    );

    act(() => dispatchBindingsChanged("WS"));

    await waitFor(() => {
      expect(mocks.listTriggerBindings).toHaveBeenCalledTimes(2);
      expect(result.current.agentRecords).toEqual([refreshedRecord]);
    });

    await act(async () => {
      initialBindings.resolve([]);
      initialRecords.resolve([]);
      await Promise.all([initialBindings.promise, initialRecords.promise]);
    });

    expect(result.current.agentRecords).toEqual([refreshedRecord]);
    expect(result.current.bindings).toEqual([
      expect.objectContaining({
        binding_id: "binding-refreshed",
        target_agent_service_id: "agent-refreshed",
      }),
    ]);
    expect(result.current.initialized).toBe(true);
  });

  it("masks workspace A records while workspace B bare-route data loads", async () => {
    const workspaceABindings = deferred<TriggerBinding[]>();
    const workspaceARecords = deferred<AgentRecordSummary[]>();
    const workspaceBRecord = durableRecord("agent-b", "WS-B");
    const workspaceBBinding: TriggerBinding = {
      ...attachedPromptBinding,
      workspace_key: "WS-B",
      binding_id: "binding-b",
      route_key: "binding-b",
      target_agent_service_id: workspaceBRecord.id,
    };
    mocks.listTriggerBindings
      .mockReturnValueOnce(workspaceABindings.promise)
      .mockResolvedValueOnce([workspaceBBinding]);
    mocks.listAgentRecords
      .mockReturnValueOnce(workspaceARecords.promise)
      .mockResolvedValueOnce([workspaceBRecord]);

    const { result, rerender } = renderHook(
      ({ workspaceId }) => useAutomations(workspaceId, true),
      { initialProps: { workspaceId: "WS-A" } },
    );
    await waitFor(() =>
      expect(mocks.listTriggerBindings).toHaveBeenCalledWith("WS-A"),
    );

    rerender({ workspaceId: "WS-B" });

    expect(result.current.agentRecords).toEqual([]);
    expect(result.current.bindings).toEqual([]);
    expect(result.current.initialized).toBe(false);
    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(mocks.listTriggerBindings).toHaveBeenCalledWith("WS-B");
      expect(result.current.agentRecords).toEqual([workspaceBRecord]);
    });

    await act(async () => {
      workspaceABindings.resolve([]);
      workspaceARecords.resolve([]);
      await Promise.all([
        workspaceABindings.promise,
        workspaceARecords.promise,
      ]);
    });

    expect(result.current.agentRecords).toEqual([workspaceBRecord]);
    expect(result.current.bindings).toEqual([
      expect.objectContaining({
        binding_id: "binding-b",
        workspace_key: "WS-B",
      }),
    ]);
  });
});
