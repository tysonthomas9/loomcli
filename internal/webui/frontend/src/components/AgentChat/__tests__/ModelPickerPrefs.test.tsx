/**
 * @vitest-environment jsdom
 */

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";

import { ProviderModelPicker } from "../ProviderModelPicker";
import type { PickerModel, PickerProvider } from "@/hooks/agents/useAgentModel";

const STORAGE_KEY = "loom.agentChat.modelPicker";
const providers: PickerProvider[] = [{ id: "openai", name: "OpenAI" }];
const models: PickerModel[] = Array.from({ length: 6 }, (_, i) => ({
  id: `openai/model-${i}`,
  name: `Model ${i}`,
  providerId: "openai",
  providerName: "OpenAI",
  context_limit: 1000,
  input: ["text"],
  is_default: i === 0,
  option_descriptors: [],
}));

function Picker() {
  const [modelId, setModelId] = useState(models[0]!.id);
  return (
    <ProviderModelPicker
      harness="opencode"
      models={models}
      providers={providers}
      model={models.find((m) => m.id === modelId)!}
      modelId={modelId}
      onModelChange={setModelId}
    />
  );
}

const prefs = () =>
  JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "{}") as {
    favorites?: string[];
    recent?: string[];
  };

beforeEach(() => window.localStorage.clear());

function choose(index: number) {
  fireEvent.click(screen.getByRole("button", { name: /^Model:/ }));
  fireEvent.click(
    screen.getByRole("option", { name: new RegExp(`Model ${index}`) }),
  );
}

describe("model picker preferences", () => {
  it("persists a selection when a pending favorite update is followed by picker close", () => {
    render(<Picker />);
    fireEvent.click(screen.getByRole("button", { name: /^Model:/ }));
    const row = screen.getByRole("option", { name: /Model 1/ });
    act(() => {
      fireEvent.click(
        within(row).getByRole("button", { name: "Add to favorites" }),
      );
      fireEvent.click(row);
    });

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(prefs()).toEqual({
      favorites: ["opencode:openai/model-1"],
      recent: ["opencode:openai/model-1"],
    });
    fireEvent.click(screen.getByRole("button", { name: /^Model:/ }));
    fireEvent.click(screen.getByRole("button", { name: "Recent" }));
    expect(screen.getByRole("option", { name: /Model 1/ })).toBeInTheDocument();
  });

  it("keeps recent models in MRU order, deduplicates, caps at five, and keeps harness keys apart", () => {
    window.localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ favorites: [], recent: ["codex:openai/model-0"] }),
    );
    render(<Picker />);
    fireEvent.click(screen.getByRole("button", { name: /^Model:/ }));
    fireEvent.click(screen.getByRole("button", { name: "Recent" }));
    expect(screen.getByText("No recent models")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^Model:/ }));

    choose(1);
    expect(prefs().recent).toEqual([
      "opencode:openai/model-1",
      "codex:openai/model-0",
    ]);
    for (const index of [2, 3, 4, 5, 0, 3]) choose(index);
    expect(prefs().recent).toEqual([
      "opencode:openai/model-3",
      "opencode:openai/model-0",
      "opencode:openai/model-5",
      "opencode:openai/model-4",
      "opencode:openai/model-2",
    ]);
    fireEvent.click(screen.getByRole("button", { name: /^Model:/ }));
    fireEvent.click(screen.getByRole("button", { name: "Recent" }));
    expect(screen.getAllByRole("option").map((row) => row.textContent)).toEqual(
      [
        expect.stringContaining("Model 3"),
        expect.stringContaining("Model 0"),
        expect.stringContaining("Model 5"),
        expect.stringContaining("Model 4"),
        expect.stringContaining("Model 2"),
      ],
    );
  });

  it("toggles favorites and still selects when storage writes fail", () => {
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("storage blocked");
      });
    try {
      render(<Picker />);
      fireEvent.click(screen.getByRole("button", { name: /^Model:/ }));
      const row = screen.getByRole("option", { name: /Model 1/ });
      fireEvent.click(
        within(row).getByRole("button", { name: "Add to favorites" }),
      );
      expect(
        within(row).getByRole("button", { name: "Remove from favorites" }),
      ).toBeInTheDocument();
      fireEvent.click(
        within(row).getByRole("button", { name: "Remove from favorites" }),
      );
      expect(
        within(row).getByRole("button", { name: "Add to favorites" }),
      ).toBeInTheDocument();
      fireEvent.click(row);
      expect(
        screen.getByRole("button", { name: "Model: Model 1" }),
      ).toBeInTheDocument();
      expect(prefs()).toEqual({});
    } finally {
      setItem.mockRestore();
    }
  });
});
