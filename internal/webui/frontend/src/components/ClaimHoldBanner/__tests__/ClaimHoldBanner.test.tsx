/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ClaimHoldBanner.
 *
 * The banner is the only place a quiesced workspace announces itself, so the
 * cases that matter are: it shows the holder, reason and age; it stays out of
 * the way when claims are free; it escalates once a hold is old enough to be
 * an oversight; and its Release button actually reaches the mutation.
 */

import { act, render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { KeyboardShortcutProvider } from "@/hooks";
import { ClaimHoldBanner, formatHoldAge } from "../ClaimHoldBanner";

const mockRelease = vi.fn();
const mockUseClaimHold = vi.fn();

function renderBanner() {
  return render(
    <KeyboardShortcutProvider>
      <ClaimHoldBanner />
    </KeyboardShortcutProvider>,
  );
}

vi.mock("@/hooks/agents", () => ({
  useClaimHold: () => mockUseClaimHold(),
}));

interface HoldOverrides {
  actor?: string;
  reason?: string;
  since?: string;
}

function holdState(
  overrides: HoldOverrides = {},
  extra: Record<string, unknown> = {},
) {
  return {
    hold: {
      held: true,
      actor: overrides.actor ?? "deployer",
      reason: overrides.reason ?? "loom redeploy",
      since: overrides.since ?? "2026-01-15T11:46:00.000Z",
    },
    running: [],
    gated: 0,
    busy: false,
    error: null,
    canForceRelease: false,
    release: mockRelease,
    refresh: vi.fn(),
    ...extra,
  };
}

function freeState() {
  return {
    hold: null,
    running: [],
    gated: 0,
    busy: false,
    error: null,
    canForceRelease: false,
    release: mockRelease,
    refresh: vi.fn(),
  };
}

describe("ClaimHoldBanner", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-15T12:00:00.000Z"));
    mockRelease.mockReset();
    mockRelease.mockResolvedValue(true);
    mockUseClaimHold.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders nothing when there is no active hold", () => {
    mockUseClaimHold.mockReturnValue(freeState());
    const { container } = renderBanner();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the holder and the reason", () => {
    mockUseClaimHold.mockReturnValue(holdState());
    renderBanner();

    expect(screen.getByRole("status")).toBeInTheDocument();
    expect(screen.getByText("deployer")).toBeInTheDocument();
    expect(screen.getByText(/loom redeploy/)).toBeInTheDocument();
  });

  it("reports how many agents the hold is gating", () => {
    mockUseClaimHold.mockReturnValue(holdState({}, { gated: 6 }));
    renderBanner();

    expect(screen.getByText(/6 agents gated/)).toBeInTheDocument();
  });

  it("stays unescalated for a young hold", () => {
    mockUseClaimHold.mockReturnValue(holdState());
    renderBanner();

    expect(screen.getByRole("status")).not.toHaveAttribute("data-stale");
    expect(screen.queryByText(/forgotten\?/)).not.toBeInTheDocument();
  });

  it("shows the escalated marker once a hold passes 2h", () => {
    mockUseClaimHold.mockReturnValue(
      holdState({ since: "2026-01-15T09:46:00.000Z" }),
    );
    renderBanner();

    expect(screen.getByRole("status")).toHaveAttribute("data-stale", "true");
    expect(screen.getByText(/HELD 2h14m — forgotten\?/)).toBeInTheDocument();
  });

  it("calls the release mutation when Release is clicked", () => {
    mockUseClaimHold.mockReturnValue(holdState());
    renderBanner();

    fireEvent.click(screen.getByRole("button", { name: /release/i }));
    expect(mockRelease).toHaveBeenCalledTimes(1);
  });

  it("disables the button and surfaces an error while a release is refused", () => {
    mockUseClaimHold.mockReturnValue(
      holdState({}, { busy: true, error: "claims held by someone-else" }),
    );
    renderBanner();

    expect(screen.getByRole("button", { name: /release/i })).toBeDisabled();
    expect(screen.getByText(/claims held by someone-else/)).toBeInTheDocument();
  });

  it("offers a confirmed force release after an ownership conflict", async () => {
    mockUseClaimHold.mockReturnValue(
      holdState(
        {},
        {
          error:
            "claims held by someone-else since 2026-01-15T11:46:00Z; use --force to release",
          canForceRelease: true,
        },
      ),
    );
    renderBanner();

    fireEvent.click(screen.getByRole("button", { name: /force release/i }));
    expect(
      screen.getByRole("alertdialog", { name: /force release claim hold/i }),
    ).toBeInTheDocument();
    expect(screen.getByText(/someone-else/)).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Force release" }));
    });
    expect(mockRelease).toHaveBeenCalledWith(true);
  });
});

describe("formatHoldAge", () => {
  it.each([
    [40_000, "40s"],
    [90_000, "1m"],
    [14 * 60_000, "14m"],
    [(2 * 60 + 14) * 60_000, "2h14m"],
    [-5_000, "0s"],
  ])("formats %ims as %s", (ms, want) => {
    expect(formatHoldAge(ms as number)).toBe(want);
  });
});
