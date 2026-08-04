/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
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
});
