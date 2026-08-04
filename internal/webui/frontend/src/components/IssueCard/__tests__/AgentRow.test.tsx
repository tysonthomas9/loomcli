/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentRow component.
 *
 * AgentRow is now a total function of a CardAgentView: the kind decides
 * everything. Several states the old boolean-prop API could express are now
 * unrepresentable by construction — most importantly the bug where a present
 * agent's stale error rendered red over live activity (there is no kind that
 * carries an error while also being live). Those cases are intentionally gone.
 */

import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { AgentRow } from "../AgentRow";
import type { CardAgentView } from "../cardAgentView";

type ClaimedView = Extract<CardAgentView, { kind: "claimed" }>;
type MissingView = Extract<CardAgentView, { kind: "missing" }>;

function claimed(overrides: Partial<ClaimedView> = {}): CardAgentView {
  return {
    kind: "claimed",
    displayName: "nova",
    status: { type: "working", taskId: "loom-123", duration: "5m" },
    lastActivityAt: null,
    ...overrides,
  };
}

function missing(overrides: Partial<MissingView> = {}): CardAgentView {
  return {
    kind: "missing",
    displayName: "nova",
    errorClass: undefined,
    ...overrides,
  };
}

const review = (displayName = "nova"): CardAgentView => ({
  kind: "review",
  displayName,
});

describe("AgentRow", () => {
  describe("rendering", () => {
    it("renders agent name", () => {
      render(<AgentRow view={claimed({ displayName: "falcon" })} />);
      expect(screen.getByText("falcon")).toBeInTheDocument();
    });

    it("renders avatar with first letter uppercased", () => {
      render(<AgentRow view={claimed({ displayName: "nova" })} />);
      expect(screen.getByText("N")).toBeInTheDocument();
    });

    it("renders ? for empty agent name", () => {
      render(<AgentRow view={missing({ displayName: "" })} />);
      expect(screen.getByText("?")).toBeInTheDocument();
    });

    it("derives an avatar background color from the name", () => {
      const { container } = render(
        <AgentRow view={missing({ displayName: "nova" })} />,
      );
      // The avatar div is the styled element (missing has no status dot).
      const avatar = container.querySelector("[style]") as HTMLElement;
      expect(avatar).toBeInTheDocument();
      expect(avatar.style.backgroundColor).toBeTruthy();
    });

    it("returns nothing for kind: none", () => {
      const { container } = render(<AgentRow view={{ kind: "none" }} />);
      expect(container).toBeEmptyDOMElement();
    });
  });

  describe("claimed", () => {
    it("renders a status dot (derived from the claimant's status)", () => {
      const { container } = render(<AgentRow view={claimed()} />);
      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).toBeInTheDocument();
      expect(dot).toHaveAttribute("aria-hidden", "true");
      expect((dot as HTMLElement).style.backgroundColor).toBeTruthy();
    });

    it("renders the status label", () => {
      render(<AgentRow view={claimed({ status: { type: "working" } })} />);
      expect(screen.getByText("Working")).toBeInTheDocument();
    });

    it("composes the status label with a relative-time label", () => {
      const NOW_MS = Date.UTC(2026, 4, 21, 12, 0, 0);
      const at = new Date(NOW_MS - 5_000).toISOString();
      render(
        <AgentRow view={claimed({ lastActivityAt: at })} now={() => NOW_MS} />,
      );
      expect(screen.getByText("Working · active 5s ago")).toBeInTheDocument();
    });

    it("switches to 'last seen' past the 5-minute mark", () => {
      const NOW_MS = Date.UTC(2026, 4, 21, 12, 0, 0);
      const at = new Date(NOW_MS - 30 * 60_000).toISOString();
      render(
        <AgentRow view={claimed({ lastActivityAt: at })} now={() => NOW_MS} />,
      );
      expect(
        screen.getByText("Working · last seen 30m ago"),
      ).toBeInTheDocument();
    });

    describe("self-refresh ticker (mirrors ConnectionBanner pattern)", () => {
      beforeEach(() => vi.useFakeTimers());
      afterEach(() => vi.useRealTimers());

      it("recomputes the relative-time label without a parent re-render", () => {
        const NOW_MS = Date.UTC(2026, 4, 21, 12, 0, 0);
        let nowMs = NOW_MS;
        const at = new Date(NOW_MS - 1_000).toISOString();
        render(
          <AgentRow view={claimed({ lastActivityAt: at })} now={() => nowMs} />,
        );
        expect(screen.getByText("Working · active 1s ago")).toBeInTheDocument();

        nowMs += 15_000;
        act(() => {
          vi.advanceTimersByTime(10_000);
        });
        expect(screen.getByText(/active 16s ago/)).toBeInTheDocument();
      });
    });
  });

  describe("missing", () => {
    it('renders a red "agent missing" with no reason when errorClass is absent', () => {
      const { container } = render(
        <AgentRow view={missing({ displayName: "worker2" })} />,
      );
      expect(screen.getByText("agent missing")).toBeInTheDocument();
      expect(container.querySelector('[class*="activity"]')).toHaveAttribute(
        "data-state",
        "missing",
      );
    });

    it('enriches "agent missing" with the failure reason, keeping the raw class as the title', () => {
      const { container } = render(
        <AgentRow view={missing({ errorClass: "SpawnFailure" })} />,
      );
      expect(
        screen.getByText("agent missing · launch failed"),
      ).toBeInTheDocument();
      const activity = container.querySelector('[class*="activity"]');
      expect(activity).toHaveAttribute("data-state", "missing");
      expect(activity).toHaveAttribute("title", "SpawnFailure");
    });

    it("renders an unknown error_class as a generic 'run failed'", () => {
      render(<AgentRow view={missing({ errorClass: "Wat" })} />);
      expect(
        screen.getByText("agent missing · run failed"),
      ).toBeInTheDocument();
    });

    it("renders no status dot", () => {
      const { container } = render(
        <AgentRow view={missing({ errorClass: "SpawnFailure" })} />,
      );
      expect(container.querySelector('[class*="statusDot"]')).toBeNull();
    });
  });

  describe("review", () => {
    it('renders "Submitted for review" with no dot', () => {
      const { container } = render(<AgentRow view={review()} />);
      const activity = screen.getByText("Submitted for review");
      expect(activity).toBeInTheDocument();
      expect(activity).toHaveAttribute("data-state", "neutral");
      expect(container.querySelector('[class*="statusDot"]')).toBeNull();
    });
  });

  describe("[H] prefix stripping", () => {
    it("strips [H] prefix from display name", () => {
      render(<AgentRow view={missing({ displayName: "[H] Alice" })} />);
      expect(screen.getByText("Alice")).toBeInTheDocument();
      expect(screen.queryByText("[H] Alice")).not.toBeInTheDocument();
    });

    it("strips [H] prefix without space after bracket", () => {
      render(<AgentRow view={missing({ displayName: "[H]Bob" })} />);
      expect(screen.getByText("Bob")).toBeInTheDocument();
    });

    it("avatar initial uses the stripped display name", () => {
      render(<AgentRow view={missing({ displayName: "[H] Alice" })} />);
      expect(screen.getByText("A")).toBeInTheDocument();
    });

    it("does not strip [H] from the middle of a name", () => {
      render(<AgentRow view={missing({ displayName: "agent [H] test" })} />);
      expect(screen.getByText("agent [H] test")).toBeInTheDocument();
    });

    it("handles a name that is only [H]", () => {
      const { container } = render(
        <AgentRow view={missing({ displayName: "[H]" })} />,
      );
      const nameSpan = container.querySelector('[class*="name"]');
      expect(nameSpan).toBeInTheDocument();
      expect(nameSpan?.textContent).toBe("");
    });
  });

  describe("CSS classes", () => {
    it("renders with agentRow class", () => {
      const { container } = render(<AgentRow view={claimed()} />);
      expect((container.firstChild as HTMLElement).className).toMatch(
        /agentRow/,
      );
    });

    it("renders avatar with avatar class", () => {
      const { container } = render(<AgentRow view={claimed()} />);
      expect(container.querySelector('[class*="avatar"]')).toBeInTheDocument();
    });

    it("renders name with name class", () => {
      const { container } = render(<AgentRow view={claimed()} />);
      expect(container.querySelector('[class*="name"]')).toBeInTheDocument();
    });
  });
});
