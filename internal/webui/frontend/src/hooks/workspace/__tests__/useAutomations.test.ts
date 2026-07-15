// @vitest-environment jsdom

import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TriggerBinding } from "@/api/workflows";

import { useAutomations } from "../useAutomations";

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

  it("keeps attached schedule edits on the binding while routing the name to the record", async () => {
    const { result } = await renderAutomations([attachedPromptBinding]);

    await act(async () => {
      await result.current.updateBinding("prompt-binding", {
        name: "Daily prompt agent",
        schedule: "0 9 * * *",
        schedule_timezone: "UTC",
      });
    });

    expect(mocks.updateTriggerBinding).toHaveBeenCalledWith(
      "WS",
      "prompt-binding",
      { schedule: "0 9 * * *", schedule_timezone: "UTC" },
    );
    expect(mocks.updateAgentRecord).toHaveBeenCalledWith(
      "WS",
      "agent-prompt-1",
      { name: "Daily prompt agent" },
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
});
