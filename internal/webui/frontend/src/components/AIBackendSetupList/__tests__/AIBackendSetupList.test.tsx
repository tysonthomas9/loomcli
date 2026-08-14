/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, within } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { BackendInfo } from "@/utils/workspace";

import { AIBackendSetupList } from "../AIBackendSetupList";

function createBackend(overrides: Partial<BackendInfo> = {}): BackendInfo {
  return {
    name: "codex",
    displayName: "Codex",
    provider: "OpenAI",
    brandColor: "#10a37f",
    available: true,
    installed: true,
    apiKeySet: true,
    ...overrides,
  };
}

describe("AIBackendSetupList", () => {
  it("omits provider company names in matrix variant", () => {
    render(
      <AIBackendSetupList
        variant="matrix"
        backends={[
          createBackend(),
          createBackend({
            name: "local-dogfood",
            displayName: "Local Dogfood",
            provider: "Unknown",
          }),
        ]}
      />,
    );

    const list = screen.getByTestId("ai-backend-setup-list");
    expect(list).toHaveTextContent("Codex");
    expect(list).toHaveTextContent("Local Dogfood");
    expect(list.textContent).not.toMatch(/OpenAI|Unknown/);
  });

  it("omits provider company names in list variant", () => {
    render(
      <AIBackendSetupList
        variant="list"
        backends={[
          createBackend(),
          createBackend({
            name: "claude",
            displayName: "Claude",
            provider: "Anthropic",
          }),
        ]}
      />,
    );

    const list = screen.getByTestId("ai-backend-setup-list");
    expect(list).toHaveTextContent("Codex");
    expect(list).toHaveTextContent("Claude");
    expect(list.textContent).not.toMatch(/OpenAI|Anthropic/);
  });

  it("offers testing instead of set-as-default for a non-registrable backend", () => {
    const onAction = vi.fn();
    const localDogfood = createBackend({
      name: "localdogfood",
      displayName: "Local Dogfood",
      provider: "Test harness",
    });
    render(
      <AIBackendSetupList
        backends={[createBackend(), localDogfood]}
        defaultBackend="codex"
        registrableBackends={["codex"]}
        onAction={onAction}
      />,
    );

    const dogfoodRow = screen.getByRole("group", {
      name: "Local Dogfood CLI",
    });
    const action = within(dogfoodRow).getByRole("button", {
      name: "Test",
    });
    fireEvent.click(action);

    expect(onAction).toHaveBeenCalledWith(localDogfood, "test");
  });

  it("omits Set Default for a non-registrable backend in the matrix", () => {
    render(
      <AIBackendSetupList
        variant="matrix"
        backends={[
          createBackend(),
          createBackend({
            name: "localdogfood",
            displayName: "Local Dogfood",
          }),
        ]}
        defaultBackend="codex"
        registrableBackends={["codex"]}
      />,
    );

    const dogfoodRow = screen.getByRole("row", { name: /Local Dogfood/ });
    expect(
      within(dogfoodRow).getByRole("button", { name: "Test" }),
    ).toBeInTheDocument();
    expect(
      within(dogfoodRow).queryByRole("button", { name: "Set Default" }),
    ).not.toBeInTheDocument();
  });
});
