/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentRow component.
 */

import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { ParsedLoomStatus } from "@/types";

import { AgentRow } from "../AgentRow";
import type { AgentRowProps } from "../AgentRow";

/**
 * Create default props for AgentRow.
 */
function createProps(overrides: Partial<AgentRowProps> = {}): AgentRowProps {
  return {
    agentName: "nova",
    status: null,
    avatarColor: "#4a90d9",
    ...overrides,
  };
}

/**
 * Create a mock ParsedLoomStatus.
 */
function createParsedStatus(
  overrides: Partial<ParsedLoomStatus> = {},
): ParsedLoomStatus {
  return {
    type: "working",
    taskId: "loom-123",
    duration: "5m",
    raw: "working: loom-123 (5m)",
    ...overrides,
  };
}

describe("AgentRow", () => {
  describe("rendering", () => {
    it("renders agent name", () => {
      render(<AgentRow {...createProps({ agentName: "falcon" })} />);

      expect(screen.getByText("falcon")).toBeInTheDocument();
    });

    it("renders avatar with first letter uppercased", () => {
      render(<AgentRow {...createProps({ agentName: "nova" })} />);

      expect(screen.getByText("N")).toBeInTheDocument();
    });

    it("renders ? for empty agent name", () => {
      render(<AgentRow {...createProps({ agentName: "" })} />);

      expect(screen.getByText("?")).toBeInTheDocument();
    });

    it("applies avatarColor as inline style", () => {
      const { container } = render(
        <AgentRow {...createProps({ avatarColor: "#ff5500" })} />,
      );

      // Find the avatar div (has inline style, inside avatarContainer)
      const avatar = container.querySelector("[style]");
      expect(avatar).toBeInTheDocument();
      expect(avatar!.style.backgroundColor).toBeTruthy();
    });
  });

  describe("with agent data (status present)", () => {
    it("renders status dot when status and dotColor are provided", () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            dotColor: "#22c55e",
          })}
        />,
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).toBeInTheDocument();
      expect(dot).toHaveStyle({ backgroundColor: "#22c55e" });
    });

    it("status dot has aria-hidden", () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            dotColor: "#22c55e",
          })}
        />,
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).toHaveAttribute("aria-hidden", "true");
    });

    it("renders activity text when provided", () => {
      render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            activity: "Working: loom-123",
          })}
        />,
      );

      expect(screen.getByText("Working: loom-123")).toBeInTheDocument();
    });

    it("activity text has title attribute", () => {
      render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            activity: "Working: loom-123",
          })}
        />,
      );

      const activityEl = screen.getByText("Working: loom-123");
      expect(activityEl).toHaveAttribute("title", "Working: loom-123");
    });
  });

  describe("without agent data (status null)", () => {
    it("does not render status dot when status is null", () => {
      const { container } = render(
        <AgentRow {...createProps({ status: null, dotColor: "#22c55e" })} />,
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).not.toBeInTheDocument();
    });

    it("does not render status dot when dotColor is undefined", () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            dotColor: undefined,
          })}
        />,
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).not.toBeInTheDocument();
    });

    it("does not render activity when not provided", () => {
      const { container } = render(
        <AgentRow {...createProps({ activity: undefined })} />,
      );

      const activity = container.querySelector('[class*="activity"]');
      expect(activity).not.toBeInTheDocument();
    });

    it("renders only name when no status or activity", () => {
      render(
        <AgentRow {...createProps({ agentName: "nova", status: null })} />,
      );

      expect(screen.getByText("nova")).toBeInTheDocument();
      expect(screen.getByText("N")).toBeInTheDocument();
    });
  });

  describe("[H] prefix stripping", () => {
    it("strips [H] prefix from display name", () => {
      render(<AgentRow {...createProps({ agentName: "[H] Alice" })} />);

      expect(screen.getByText("Alice")).toBeInTheDocument();
      expect(screen.queryByText("[H] Alice")).not.toBeInTheDocument();
    });

    it("strips [H] prefix without space after bracket", () => {
      render(<AgentRow {...createProps({ agentName: "[H]Bob" })} />);

      expect(screen.getByText("Bob")).toBeInTheDocument();
    });

    it("avatar initial uses stripped display name", () => {
      render(<AgentRow {...createProps({ agentName: "[H] Alice" })} />);

      // Initial is from the stripped displayName, so 'A' for 'Alice'
      expect(screen.getByText("A")).toBeInTheDocument();
    });

    it("does not strip [H] from middle of name", () => {
      render(<AgentRow {...createProps({ agentName: "agent [H] test" })} />);

      expect(screen.getByText("agent [H] test")).toBeInTheDocument();
    });

    it("handles name that is only [H]", () => {
      render(<AgentRow {...createProps({ agentName: "[H]" })} />);

      // After stripping "[H]", display name should be empty string
      // The name span will exist but be empty
      const { container } = render(
        <AgentRow {...createProps({ agentName: "[H]" })} />,
      );
      const nameSpan = container.querySelector('[class*="name"]');
      expect(nameSpan).toBeInTheDocument();
      expect(nameSpan?.textContent).toBe("");
    });
  });

  describe("liveness (lastActivityAt / agentMissing)", () => {
    const NOW_MS = Date.UTC(2026, 4, 21, 12, 0, 0); // 2026-05-21T12:00:00Z
    const fixedNow = () => NOW_MS;

    it('renders red "agent missing" text when agentMissing is true', () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            agentName: "worker2",
            agentMissing: true,
          })}
        />,
      );
      expect(screen.getByText("agent missing")).toBeInTheDocument();
      const activity = container.querySelector('[class*="activity"]');
      expect(activity).toHaveAttribute("data-state", "missing");
    });

    it('enriches "agent missing" with the failure reason when lastErrorClass is known', () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            agentName: "worker2",
            agentMissing: true,
            lastErrorClass: "SpawnFailure",
          })}
        />,
      );
      expect(
        screen.getByText("agent missing · launch failed"),
      ).toBeInTheDocument();
      const activity = container.querySelector('[class*="activity"]');
      expect(activity).toHaveAttribute("data-state", "missing");
      // Raw class is preserved as the hover title for precision.
      expect(activity).toHaveAttribute("title", "SpawnFailure");
    });

    it("renders an unknown error_class as a generic red 'run failed'", () => {
      const { container } = render(
        <AgentRow
          {...createProps({ agentMissing: true, lastErrorClass: "Wat" })}
        />,
      );
      expect(
        screen.getByText("agent missing · run failed"),
      ).toBeInTheDocument();
      expect(container.querySelector('[class*="activity"]')).toHaveAttribute(
        "data-state",
        "missing",
      );
    });

    it("shows the failure reason in red for an idle (not-missing) agent whose last run failed", () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            agentMissing: false,
            lastErrorClass: "RateLimited",
          })}
        />,
      );
      const activity = container.querySelector('[class*="activity"]');
      expect(screen.getByText("rate limited")).toBeInTheDocument();
      expect(activity).toHaveAttribute("data-state", "missing");
      expect(activity).toHaveAttribute("title", "RateLimited");
    });

    it("suppresses the status dot when agentMissing is true (live status is meaningless)", () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            dotColor: "#22c55e",
            agentMissing: true,
          })}
        />,
      );
      expect(container.querySelector('[class*="statusDot"]')).toBeNull();
    });

    it('renders "active Ns ago" when lastActivityAt is seconds ago', () => {
      const at = new Date(NOW_MS - 3_000).toISOString();
      render(
        <AgentRow
          {...createProps({
            lastActivityAt: at,
            now: fixedNow,
          })}
        />,
      );
      expect(screen.getByText("active 3s ago")).toBeInTheDocument();
    });

    it('renders "active Nm ago" when lastActivityAt is within 5 minutes', () => {
      const at = new Date(NOW_MS - 3 * 60_000).toISOString();
      render(
        <AgentRow
          {...createProps({
            lastActivityAt: at,
            now: fixedNow,
          })}
        />,
      );
      expect(screen.getByText("active 3m ago")).toBeInTheDocument();
    });

    it('renders "last seen Xm ago" past the 5-minute mark', () => {
      const at = new Date(NOW_MS - 30 * 60_000).toISOString();
      render(
        <AgentRow
          {...createProps({
            lastActivityAt: at,
            now: fixedNow,
          })}
        />,
      );
      expect(screen.getByText("last seen 30m ago")).toBeInTheDocument();
    });

    it('renders "last seen Xh ago" past the hour mark', () => {
      const at = new Date(NOW_MS - 4 * 60 * 60_000).toISOString();
      render(
        <AgentRow
          {...createProps({
            lastActivityAt: at,
            now: fixedNow,
          })}
        />,
      );
      expect(screen.getByText("last seen 4h ago")).toBeInTheDocument();
    });

    it("combines an existing activity prop with the relative-time label", () => {
      const at = new Date(NOW_MS - 5_000).toISOString();
      render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            activity: "Working",
            lastActivityAt: at,
            now: fixedNow,
          })}
        />,
      );
      expect(screen.getByText("Working · active 5s ago")).toBeInTheDocument();
    });

    it('renders "awaiting activity" when lastActivityAt is explicitly null (agent up, no PTY output yet)', () => {
      render(
        <AgentRow
          {...createProps({
            lastActivityAt: null,
          })}
        />,
      );
      expect(screen.getByText("awaiting activity")).toBeInTheDocument();
    });

    it("agentMissing wins over a stale lastActivityAt", () => {
      const at = new Date(NOW_MS - 60_000).toISOString();
      render(
        <AgentRow
          {...createProps({
            agentMissing: true,
            lastActivityAt: at,
            now: fixedNow,
          })}
        />,
      );
      expect(screen.getByText("agent missing")).toBeInTheDocument();
      expect(screen.queryByText(/active|last seen/)).toBeNull();
    });

    describe("self-refresh ticker (mirrors ConnectionBanner pattern)", () => {
      beforeEach(() => {
        vi.useFakeTimers();
      });
      afterEach(() => {
        vi.useRealTimers();
      });

      it("recomputes the 'Xs ago' label without a parent re-render", () => {
        let nowMs = NOW_MS;
        const at = new Date(NOW_MS - 1_000).toISOString();
        render(
          <AgentRow
            {...createProps({
              lastActivityAt: at,
              now: () => nowMs,
            })}
          />,
        );
        expect(screen.getByText("active 1s ago")).toBeInTheDocument();

        nowMs += 15_000; // advance virtual clock 15s
        act(() => {
          vi.advanceTimersByTime(10_000); // fire one ticker
        });

        expect(screen.getByText(/active 16s ago/)).toBeInTheDocument();
      });
    });
  });

  describe("CSS classes", () => {
    it("renders with agentRow class", () => {
      const { container } = render(<AgentRow {...createProps()} />);

      const row = container.firstChild;
      expect((row as HTMLElement).className).toMatch(/agentRow/);
    });

    it("renders avatar with avatar class", () => {
      const { container } = render(<AgentRow {...createProps()} />);

      const avatar = container.querySelector('[class*="avatar"]');
      expect(avatar).toBeInTheDocument();
    });

    it("renders name with name class", () => {
      const { container } = render(<AgentRow {...createProps()} />);

      const name = container.querySelector('[class*="name"]');
      expect(name).toBeInTheDocument();
    });
  });
});
