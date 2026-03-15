/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { BackendInfo } from "./backendDefaults";
import { BackendSelectorDropdown } from "./BackendSelectorDropdown";

function makeBackend(overrides: Partial<BackendInfo> = {}): BackendInfo {
  return {
    name: "claude",
    displayName: "Claude",
    provider: "Anthropic",
    brandColor: "#d4a574",
    available: true,
    ...overrides,
  };
}

const defaultBackends: BackendInfo[] = [
  makeBackend(),
  makeBackend({
    name: "codex",
    displayName: "Codex",
    provider: "OpenAI",
    brandColor: "#10a37f",
  }),
  makeBackend({
    name: "opencode",
    displayName: "OpenCode",
    provider: "Open Source",
    brandColor: "#6366f1",
  }),
];

describe("BackendSelectorDropdown", () => {
  describe("trigger rendering", () => {
    it("renders trigger with selected backend display name", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      expect(screen.getByTestId("backend-selector-trigger")).toHaveTextContent(
        "Claude",
      );
    });

    it("renders brand-colored dot in trigger", () => {
      const { container } = render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      const dot = container.querySelector("[class*='brandDot']");
      expect(dot).toHaveStyle({ backgroundColor: "#d4a574" });
    });

    it("renders placeholder when selected backend is not in list", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="unknown-backend"
          onSelect={vi.fn()}
          placeholder="Pick one"
        />,
      );
      expect(screen.getByTestId("backend-selector-trigger")).toHaveTextContent(
        "Pick one",
      );
    });
  });

  describe("dropdown open/close", () => {
    it("opens dropdown on click", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(screen.getByTestId("backend-selector-menu")).toBeInTheDocument();
    });

    it("closes dropdown on second click", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      const trigger = screen.getByTestId("backend-selector-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("backend-selector-menu")).toBeInTheDocument();
      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("backend-selector-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes dropdown on click outside", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(screen.getByTestId("backend-selector-menu")).toBeInTheDocument();
      fireEvent.mouseDown(document.body);
      expect(
        screen.queryByTestId("backend-selector-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes dropdown on Escape", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(screen.getByTestId("backend-selector-menu")).toBeInTheDocument();
      const menu = screen.getByTestId("backend-selector-menu");
      fireEvent.keyDown(menu, { key: "Escape" });
      expect(
        screen.queryByTestId("backend-selector-menu"),
      ).not.toBeInTheDocument();
    });

    it("does not open when disabled", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
          disabled
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(
        screen.queryByTestId("backend-selector-menu"),
      ).not.toBeInTheDocument();
    });

    it("does not open when saving", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
          isSaving
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(
        screen.queryByTestId("backend-selector-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("backend options", () => {
    it("renders all backend options", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(screen.getByTestId("backend-option-claude")).toBeInTheDocument();
      expect(screen.getByTestId("backend-option-codex")).toBeInTheDocument();
      expect(screen.getByTestId("backend-option-opencode")).toBeInTheDocument();
    });

    it("marks selected backend with checkmark", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      const claudeOption = screen.getByTestId("backend-option-claude");
      expect(claudeOption).toHaveAttribute("data-selected", "true");
      expect(claudeOption).toHaveTextContent("✓");
    });

    it("shows display name and provider for each option", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      const codexOption = screen.getByTestId("backend-option-codex");
      expect(codexOption).toHaveTextContent("Codex");
      expect(codexOption).toHaveTextContent("OpenAI");
    });
  });

  describe("selection", () => {
    it("calls onSelect with backend name when clicked", async () => {
      const onSelect = vi.fn().mockResolvedValue(undefined);
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={onSelect}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      fireEvent.click(screen.getByTestId("backend-option-codex"));
      expect(onSelect).toHaveBeenCalledWith("codex");
    });

    it("does not call onSelect when clicking already selected backend", () => {
      const onSelect = vi.fn();
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={onSelect}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      fireEvent.click(screen.getByTestId("backend-option-claude"));
      expect(onSelect).not.toHaveBeenCalled();
    });

    it("shows error and rolls back on failed selection", async () => {
      const onSelect = vi.fn().mockRejectedValue(new Error("Network error"));
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={onSelect}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      fireEvent.click(screen.getByTestId("backend-option-codex"));

      await waitFor(() => {
        expect(screen.getByTestId("backend-selector-error")).toHaveTextContent(
          "Network error",
        );
      });
      // Trigger should show original backend after rollback
      expect(screen.getByTestId("backend-selector-trigger")).toHaveTextContent(
        "Claude",
      );
    });
  });

  describe("unavailable backends", () => {
    it("marks unavailable backends as disabled", () => {
      const backends = [
        makeBackend(),
        makeBackend({
          name: "codex",
          displayName: "Codex",
          provider: "OpenAI",
          available: false,
        }),
      ];
      render(
        <BackendSelectorDropdown
          backends={backends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(screen.getByTestId("backend-option-codex")).toHaveAttribute(
        "data-disabled",
        "true",
      );
    });

    it("does not call onSelect for unavailable backends", () => {
      const onSelect = vi.fn();
      const backends = [
        makeBackend(),
        makeBackend({ name: "codex", available: false }),
      ];
      render(
        <BackendSelectorDropdown
          backends={backends}
          selectedBackend="claude"
          onSelect={onSelect}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      fireEvent.click(screen.getByTestId("backend-option-codex"));
      expect(onSelect).not.toHaveBeenCalled();
    });

    it("shows Configure in Settings link for unavailable backends", () => {
      const backends = [
        makeBackend(),
        makeBackend({ name: "codex", available: false }),
      ];
      render(
        <BackendSelectorDropdown
          backends={backends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      const btn = screen.getByTestId("backend-configure-codex");
      expect(btn).toBeInTheDocument();
      expect(btn.tagName).toBe("BUTTON");
    });
  });

  describe("search filtering", () => {
    it("filters backends by display name", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      const search = screen.getByTestId("backend-selector-search");
      fireEvent.change(search, { target: { value: "code" } });
      expect(
        screen.queryByTestId("backend-option-claude"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("backend-option-codex")).toBeInTheDocument();
      expect(screen.getByTestId("backend-option-opencode")).toBeInTheDocument();
    });

    it("filters backends by provider", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      const search = screen.getByTestId("backend-selector-search");
      fireEvent.change(search, { target: { value: "anthropic" } });
      expect(screen.getByTestId("backend-option-claude")).toBeInTheDocument();
      expect(
        screen.queryByTestId("backend-option-codex"),
      ).not.toBeInTheDocument();
    });

    it("shows no matching backends message", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      const search = screen.getByTestId("backend-selector-search");
      fireEvent.change(search, { target: { value: "zzzzz" } });
      expect(screen.getByTestId("backend-selector-empty")).toHaveTextContent(
        "No matching backends",
      );
    });
  });

  describe("empty state", () => {
    it("shows no backends configured when list is empty", () => {
      render(
        <BackendSelectorDropdown
          backends={[]}
          selectedBackend=""
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(screen.getByTestId("backend-selector-empty")).toHaveTextContent(
        "No backends configured",
      );
    });
  });

  describe("saving state", () => {
    it("shows saving indicator when isSaving is true", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
          isSaving
        />,
      );
      expect(screen.getByTestId("backend-selector-saving")).toBeInTheDocument();
    });

    it("sets data-saving on trigger when saving", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
          isSaving
        />,
      );
      expect(screen.getByTestId("backend-selector-trigger")).toHaveAttribute(
        "data-saving",
        "true",
      );
    });
  });

  describe("accessibility", () => {
    it("has correct aria attributes on trigger", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      const trigger = screen.getByTestId("backend-selector-trigger");
      expect(trigger).toHaveAttribute("aria-haspopup", "listbox");
      expect(trigger).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "true");
    });

    it("has correct aria attributes on options", () => {
      render(
        <BackendSelectorDropdown
          backends={defaultBackends}
          selectedBackend="claude"
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      const option = screen.getByTestId("backend-option-claude");
      expect(option).toHaveAttribute("role", "option");
      expect(option).toHaveAttribute("aria-selected", "true");
    });

    it("sets aria-disabled on unavailable backends", () => {
      const backends = [makeBackend({ name: "codex", available: false })];
      render(
        <BackendSelectorDropdown
          backends={backends}
          selectedBackend=""
          onSelect={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByTestId("backend-selector-trigger"));
      expect(screen.getByTestId("backend-option-codex")).toHaveAttribute(
        "aria-disabled",
        "true",
      );
    });
  });
});
