/**
 * @vitest-environment jsdom
 */

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type {
  Agent,
  AgentEvent,
  AgentStreamOptions,
  ModelCatalog,
} from "@/api/agentsv1";

const api = vi.hoisted(() => ({
  getAgent: vi.fn(),
  updateAgent: vi.fn(),
  listHarnessModels: vi.fn(),
  getCustomModels: vi.fn(),
  setCustomModels: vi.fn(),
  streams: [] as { opts: AgentStreamOptions; events: AgentEvent[] }[],
  ids: 0,
}));

vi.mock("@/contexts/AuthContext", () => ({
  useAuth: () => ({ user: null }),
}));

vi.mock("@/api/agentsv1", () => ({
  getAgent: api.getAgent,
  updateAgent: api.updateAgent,
  listHarnessModels: api.listHarnessModels,
  getCustomModels: api.getCustomModels,
  setCustomModels: api.setCustomModels,
  sendMessage: vi.fn(),
  withdrawMessage: vi.fn(),
  respondToAsk: vi.fn(),
  newRequestId: () => `req-${++api.ids}`,
  AgentEventStream: class {
    events: AgentEvent[] = [];
    history = { events: () => this.events };
    constructor(
      _ws: string,
      public opts: AgentStreamOptions,
    ) {
      api.streams.push(this);
    }
    connect() {
      return Promise.resolve();
    }
    close() {}
  },
}));

import { AgentChat } from "../AgentChat";
import { scoreModelPickerSearch } from "../modelPickerSearch";
import {
  descriptorsWithSaved,
  resolveAgentModel,
  savedOptions,
  catalogModels,
} from "@/hooks/agents/useAgentModel";

const effort = (def: string, ids: string[]) => ({
  id: "effort",
  label: "Effort",
  type: "select" as const,
  current_value: def,
  options: ids.map((id) => ({
    id,
    label: id[0]!.toUpperCase() + id.slice(1),
    ...(id === def ? { is_default: true } : {}),
  })),
});

const CATALOG: ModelCatalog = {
  harness: "opencode",
  providers: [
    {
      id: "openai",
      name: "OpenAI",
      models: [
        {
          id: "openai/gpt-5.5",
          name: "GPT-5.5",
          context_limit: 400000,
          input: ["text", "image"],
          is_default: true,
          option_descriptors: [effort("medium", ["low", "medium", "high"])],
        },
        {
          id: "openai/gpt-5.5-mini",
          name: "GPT-5.5 Mini",
          context_limit: 400000,
          input: ["text"],
          is_default: false,
          option_descriptors: [effort("low", ["minimal", "low"])],
        },
      ],
    },
    {
      id: "anthropic",
      name: "Anthropic",
      models: [
        {
          id: "anthropic/claude-sonnet-5",
          name: "Claude Sonnet 5",
          context_limit: 200000,
          input: ["text"],
          is_default: false,
          option_descriptors: [],
        },
      ],
    },
  ],
};

function agent(over: Partial<Agent> = {}): Agent {
  return {
    agent_id: "a1",
    name: "lead",
    harness: "opencode",
    mode: "interactive",
    state: "idle",
    model: null,
    spec_json: "{}",
    running_turn_id: null,
    waiting_messages: [],
    open_asks: [],
    ...over,
  } as Agent;
}

async function mount(a: Agent) {
  api.getAgent.mockResolvedValue(a);
  const view = render(
    <MemoryRouter>
      <AgentChat workspaceId="w1" agentId="a1" />
    </MemoryRouter>,
  );
  await screen.findByText(a.name);
  await screen.findByRole("button", { name: /^Model: / });
  // The catalog loads after the agent.
  await act(async () => {});
  return view;
}

const modelButton = () => screen.getByRole("button", { name: /^Model: / });

beforeEach(() => {
  vi.clearAllMocks();
  api.streams = [];
  window.localStorage.clear();
  api.listHarnessModels.mockResolvedValue(CATALOG);
  api.updateAgent.mockResolvedValue({});
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("composer model and effort pickers (UI2)", () => {
  it("shows the catalog default and its effort, from the agent's harness catalog", async () => {
    await mount(agent());
    expect(api.listHarnessModels).toHaveBeenCalledWith("w1", "opencode");
    expect(modelButton()).toHaveAccessibleName("Model: GPT-5.5");
    expect(
      screen.getByRole("button", { name: "Effort: Medium" }),
    ).toBeInTheDocument();
  });

  it("shows the saved model and effort after a reload (Get's model and spec_json)", async () => {
    await mount(
      agent({
        model: "openai/gpt-5.5",
        spec_json: JSON.stringify({
          Options: [{ ID: "effort", Value: "high" }],
        }),
      }),
    );
    expect(modelButton()).toHaveAccessibleName("Model: GPT-5.5");
    expect(
      screen.getByRole("button", { name: "Effort: High" }),
    ).toBeInTheDocument();
  });

  it("picks a model from a provider's list and PATCHes it", async () => {
    await mount(agent());
    fireEvent.click(modelButton());
    const dialog = screen.getByRole("dialog", { name: "Choose a model" });
    // The provider rail: Favorites, Recent, then one button per provider.
    expect(
      within(dialog).getByRole("button", { name: "OpenAI" }),
    ).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(within(dialog).getByRole("button", { name: "Anthropic" }));
    fireEvent.click(
      within(dialog).getByRole("option", { name: /Claude Sonnet 5/ }),
    );
    expect(api.updateAgent).toHaveBeenCalledWith(
      "w1",
      "a1",
      { model: "anthropic/claude-sonnet-5" },
      expect.stringMatching(/^req-/),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // The agent is read again after the PATCH.
    await act(async () => {});
    expect(api.getAgent).toHaveBeenCalledTimes(2);
  });

  it("saves the displayed catalog default when a new agent explicitly picks it", async () => {
    await mount(agent({ model: null }));
    expect(modelButton()).toHaveAccessibleName("Model: GPT-5.5");
    fireEvent.click(modelButton());
    const dialog = screen.getByRole("dialog", { name: "Choose a model" });
    fireEvent.click(
      dialog.querySelector('li[role="option"][title="openai/gpt-5.5"]')!,
    );
    expect(api.updateAgent).toHaveBeenCalledWith(
      "w1",
      "a1",
      { model: "openai/gpt-5.5" },
      expect.stringMatching(/^req-/),
    );
  });

  it("keeps an already saved model selection as a no-op", async () => {
    await mount(agent({ model: "openai/gpt-5.5" }));
    fireEvent.click(modelButton());
    const dialog = screen.getByRole("dialog", { name: "Choose a model" });
    fireEvent.click(
      dialog.querySelector('li[role="option"][title="openai/gpt-5.5"]')!,
    );
    expect(api.updateAgent).not.toHaveBeenCalled();
  });

  it("searches across providers and selects the highlighted model with Enter", async () => {
    await mount(agent());
    fireEvent.click(modelButton());
    const search = screen.getByRole("combobox", { name: "Search models" });
    fireEvent.change(search, { target: { value: "mini" } });
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent("GPT-5.5 Mini");
    fireEvent.keyDown(search, { key: "Enter" });
    expect(api.updateAgent).toHaveBeenCalledWith(
      "w1",
      "a1",
      { model: "openai/gpt-5.5-mini" },
      expect.any(String),
    );
  });

  it("keeps favorites and recent models", async () => {
    await mount(agent());
    fireEvent.click(modelButton());
    const row = screen.getByRole("option", { name: /GPT-5.5 Mini/ });
    fireEvent.click(
      within(row).getByRole("button", { name: "Add to favorites" }),
    );
    expect(api.updateAgent).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Favorites" }));
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      expect.stringContaining("GPT-5.5 Mini"),
    ]);
    fireEvent.click(screen.getByRole("option", { name: /GPT-5.5 Mini/ }));

    fireEvent.click(modelButton());
    // With a favorite, the picker opens on Favorites, as in T3.
    expect(screen.getByRole("button", { name: "Favorites" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    fireEvent.click(screen.getByRole("button", { name: "Recent" }));
    expect(screen.getAllByRole("option")).toHaveLength(1);
    expect(window.localStorage.getItem("loom.agentChat.modelPicker")).toContain(
      "opencode:openai/gpt-5.5-mini",
    );
  });

  it("lists only the effort choices the model declares and PATCHes the pick", async () => {
    await mount(agent({ model: "openai/gpt-5.5-mini" }));
    fireEvent.click(screen.getByRole("button", { name: "Effort: Low" }));
    const items = screen.getAllByRole("menuitemradio");
    expect(items.map((i) => i.textContent)).toEqual(["Minimal", "LowDefault"]);
    expect(items[1]).toHaveAttribute("aria-checked", "true");
    fireEvent.click(items[0]!);
    expect(api.updateAgent).toHaveBeenCalledWith(
      "w1",
      "a1",
      { options: [{ id: "effort", value: "minimal" }] },
      expect.any(String),
    );
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("labels effort Default when the model marks no default and none is saved", async () => {
    const noDefault = structuredClone(CATALOG);
    const d = noDefault.providers[0]!.models[0]!.option_descriptors[0]!;
    delete d.current_value;
    d.options = d.options!.map(({ id, label }) => ({ id, label }));
    api.listHarnessModels.mockResolvedValue(noDefault);
    await mount(agent());
    fireEvent.click(screen.getByRole("button", { name: "Effort: Default" }));
    expect(
      screen
        .getAllByRole("menuitemradio")
        .filter((i) => i.getAttribute("aria-checked") === "true"),
    ).toHaveLength(0);
  });

  it("hides the effort control for a model without options", async () => {
    await mount(agent({ model: "anthropic/claude-sonnet-5" }));
    expect(modelButton()).toHaveAccessibleName("Model: Claude Sonnet 5");
    expect(
      screen.queryByRole("button", { name: /Effort/ }),
    ).not.toBeInTheDocument();
  });

  it("stays usable while a turn runs; the change applies from the next turn", async () => {
    await mount(agent({ state: "running", running_turn_id: "t1" }));
    expect(modelButton()).toBeEnabled();
    expect(modelButton()).toHaveAttribute(
      "title",
      "Applies from the next turn",
    );
  });

  it("is disabled with its reason for an unfinished single task", async () => {
    await mount(agent({ mode: "single_task", state: "running" }));
    expect(modelButton()).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Effort: Medium" }),
    ).toBeDisabled();
    expect(screen.getByRole("note")).toHaveTextContent(
      "A single task's model is fixed until it finishes",
    );
    fireEvent.click(modelButton());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("is disabled with its reason when the catalog cannot be read", async () => {
    api.listHarnessModels.mockRejectedValue(new Error("harness unavailable"));
    await mount(agent({ model: "openai/gpt-5.5" }));
    expect(modelButton()).toBeDisabled();
    expect(modelButton()).toHaveAccessibleName("Model: openai/gpt-5.5");
    expect(screen.getByRole("note")).toHaveTextContent(
      "The model list is unavailable: harness unavailable",
    );
  });

  describe("a catalog read before the harness is up (no providers yet)", () => {
    const EMPTY: ModelCatalog = { harness: "opencode", providers: [] };
    const effortOnly = () =>
      agent({ spec_json: '{"Options":[{"ID":"effort","Value":"high"}]}' });
    const advance = (ms: number) =>
      act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
      });

    beforeEach(() => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it("retries until the providers appear, then shows the model and saved effort", async () => {
      api.listHarnessModels
        .mockResolvedValueOnce(EMPTY)
        .mockResolvedValueOnce(EMPTY)
        .mockResolvedValue(CATALOG);
      await mount(effortOnly());
      expect(modelButton()).toHaveAccessibleName("Model: Default model");
      await advance(30_000);
      expect(api.listHarnessModels).toHaveBeenCalledTimes(3);
      expect(modelButton()).toHaveAccessibleName("Model: GPT-5.5");
      expect(
        screen.getByRole("button", { name: "Effort: High" }),
      ).toBeInTheDocument();
    });

    it("recovers when the first read fails", async () => {
      api.listHarnessModels
        .mockRejectedValueOnce(new Error("harness starting"))
        .mockResolvedValue(CATALOG);
      await mount(effortOnly());
      await advance(0);
      expect(modelButton()).toBeDisabled();
      await advance(30_000);
      expect(modelButton()).toBeEnabled();
      expect(modelButton()).toHaveAccessibleName("Model: GPT-5.5");
      expect(screen.queryByRole("note")).not.toBeInTheDocument();
    });

    it("stops retrying after about 30 seconds", async () => {
      api.listHarnessModels.mockResolvedValue(EMPTY);
      await mount(effortOnly());
      await advance(40_000);
      const calls = api.listHarnessModels.mock.calls.length;
      expect(calls).toBeGreaterThan(1);
      expect(calls).toBeLessThanOrEqual(6);
      await advance(120_000);
      expect(api.listHarnessModels).toHaveBeenCalledTimes(calls);
    });

    it("reads the catalog again when the picker opens", async () => {
      api.listHarnessModels.mockResolvedValue(EMPTY);
      await mount(effortOnly());
      await advance(40_000);
      api.listHarnessModels.mockResolvedValue(CATALOG);
      fireEvent.click(modelButton());
      await advance(0);
      expect(modelButton()).toHaveAccessibleName("Model: GPT-5.5");
      expect(
        within(screen.getByRole("dialog")).getByRole("option", {
          name: /GPT-5.5 Mini/,
        }),
      ).toBeInTheDocument();
    });
  });

  it("moves effort into the More composer controls menu on a narrow composer", async () => {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(private cb: ResizeObserverCallback) {}
        observe() {
          this.cb(
            [{ contentRect: { width: 400 } } as ResizeObserverEntry],
            this as unknown as ResizeObserver,
          );
        }
        disconnect() {}
      },
    );
    await mount(agent());
    expect(
      screen.queryByRole("button", { name: /Effort/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "More composer controls" }),
    );
    fireEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
    expect(api.updateAgent).toHaveBeenCalledWith(
      "w1",
      "a1",
      { options: [{ id: "effort", value: "high" }] },
      expect.any(String),
    );
  });

  it("renders the same controls for opencode, codex and claude", async () => {
    for (const harness of ["opencode", "codex", "claude"]) {
      api.listHarnessModels.mockResolvedValue({ ...CATALOG, harness });
      const view = await mount(agent({ harness }));
      expect(api.listHarnessModels).toHaveBeenLastCalledWith("w1", harness);
      expect(modelButton()).toHaveAccessibleName("Model: GPT-5.5");
      expect(
        screen.getByRole("button", { name: "Effort: Medium" }),
      ).toBeInTheDocument();
      view.unmount();
    }
  });
});

describe("custom models (MCS3)", () => {
  const withCustom = (ids: string[]): ModelCatalog => ({
    ...CATALOG,
    providers: [
      ...CATALOG.providers,
      {
        id: "custom",
        name: "Custom",
        models: ids.map((id) => ({
          id,
          name: id,
          context_limit: 0,
          input: [],
          is_default: false,
          option_descriptors: [effort("medium", ["low", "medium", "high"])],
          source: "custom" as const,
        })),
      },
    ],
  });

  it("adds a custom model id in the Custom section, picks it and removes it", async () => {
    api.getCustomModels.mockResolvedValue([]);
    api.setCustomModels.mockImplementation((_ws, _h, ids: string[]) =>
      Promise.resolve(ids),
    );
    await mount(agent());
    fireEvent.click(modelButton());
    const dialog = screen.getByRole("dialog", { name: "Choose a model" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Custom" }));
    expect(
      within(dialog).getByText("No custom models yet: add a model id above"),
    ).toBeInTheDocument();

    api.listHarnessModels.mockResolvedValue(withCustom(["openai/mine"]));
    const input = within(dialog).getByRole("textbox", {
      name: "Custom model id",
    });
    fireEvent.change(input, { target: { value: " openai/mine " } });
    fireEvent.keyDown(input, { key: "Enter" });
    await act(async () => {});
    expect(api.setCustomModels).toHaveBeenCalledWith("w1", "opencode", [
      "openai/mine",
    ]);
    const row = await within(dialog).findByRole("option", {
      name: /openai\/mine/,
    });

    api.getCustomModels.mockResolvedValue(["openai/mine"]);
    api.listHarnessModels.mockResolvedValue(withCustom([]));
    fireEvent.click(
      within(row).getByRole("button", {
        name: "Remove custom model openai/mine",
      }),
    );
    await act(async () => {});
    expect(api.setCustomModels).toHaveBeenLastCalledWith("w1", "opencode", []);
    expect(api.updateAgent).not.toHaveBeenCalled();
    expect(
      within(dialog).queryByRole("option", { name: /openai\/mine/ }),
    ).not.toBeInTheDocument();
  });

  it("picks a custom model like any other", async () => {
    api.listHarnessModels.mockResolvedValue(withCustom(["openai/mine"]));
    await mount(agent());
    fireEvent.click(modelButton());
    const dialog = screen.getByRole("dialog", { name: "Choose a model" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Custom" }));
    fireEvent.click(
      within(dialog).getByRole("option", { name: /openai\/mine/ }),
    );
    expect(api.updateAgent).toHaveBeenCalledWith(
      "w1",
      "a1",
      { model: "openai/mine" },
      expect.any(String),
    );
  });

  it("shows the server's error when an id is refused", async () => {
    api.getCustomModels.mockResolvedValue([]);
    api.setCustomModels.mockRejectedValue(
      new Error('malformed model id "a//b"'),
    );
    await mount(agent());
    fireEvent.click(modelButton());
    const dialog = screen.getByRole("dialog", { name: "Choose a model" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Custom" }));
    fireEvent.change(
      within(dialog).getByRole("textbox", { name: "Custom model id" }),
      { target: { value: "a//b" } },
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      'malformed model id "a//b"',
    );
  });
});

describe("model picker helpers", () => {
  it("reads saved options from spec_json and applies them to descriptors", () => {
    const saved = savedOptions(
      JSON.stringify({
        Options: [
          { ID: "effort", Value: "high" },
          { ID: "fast", Value: "true" },
        ],
      }),
    );
    expect(saved).toEqual([
      { id: "effort", value: "high" },
      { id: "fast", value: "true" },
    ]);
    expect(savedOptions("not json")).toEqual([]);
    const ds = descriptorsWithSaved(
      [
        effort("medium", ["low", "medium", "high"]),
        { id: "fast", label: "Fast", type: "boolean", current_value: false },
      ],
      saved,
    );
    expect(ds.map((d) => d.current_value)).toEqual(["high", true]);
  });

  it("resolves the model: saved, else default, else none with the first's options", () => {
    const ms = catalogModels(CATALOG);
    expect(resolveAgentModel(ms, "openai/gpt-5.5-mini").model?.name).toBe(
      "GPT-5.5 Mini",
    );
    expect(resolveAgentModel(ms, null).model?.id).toBe("openai/gpt-5.5");
    expect(resolveAgentModel(ms, "gone").model).toBeNull();
    const noDefault = ms.map((m) => ({ ...m, is_default: false }));
    const r = resolveAgentModel(noDefault, null);
    expect(r.model).toBeNull();
    expect(r.traitsModel?.id).toBe("openai/gpt-5.5");
  });

  it("scores model search by name, id and provider; no match is null", () => {
    const m = {
      name: "GPT-5.5 Mini",
      id: "openai/gpt-5.5-mini",
      providerId: "openai",
      providerName: "OpenAI",
    };
    expect(scoreModelPickerSearch(m, "gpt mini")).not.toBeNull();
    expect(scoreModelPickerSearch(m, "openai")).not.toBeNull();
    expect(scoreModelPickerSearch(m, "claude")).toBeNull();
    expect(
      scoreModelPickerSearch({ ...m, isFavorite: true }, "gpt")!,
    ).toBeLessThan(scoreModelPickerSearch(m, "gpt")!);
  });
});
