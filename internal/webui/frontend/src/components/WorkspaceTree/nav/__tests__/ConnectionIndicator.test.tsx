/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ConnectionIndicator component.
 * Verifies rendering for each connection state, elapsed time display,
 * and null returns for connected/connecting states.
 */

import { render } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import "@testing-library/jest-dom";
import { ConnectionIndicator } from "../ConnectionIndicator";

// Mock useElapsedTime to return a deterministic value
let mockElapsedReturn = "";

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return { ...actual, useElapsedTime: () => mockElapsedReturn };
});

describe("ConnectionIndicator", () => {
  beforeEach(() => {
    mockElapsedReturn = "";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns null for state="connected"', () => {
    const { container } = render(
      <ConnectionIndicator state="connected" disconnectedSince={null} />,
    );

    expect(container.innerHTML).toBe("");
  });

  it('returns null for state="connecting"', () => {
    const { container } = render(
      <ConnectionIndicator state="connecting" disconnectedSince={null} />,
    );

    expect(container.innerHTML).toBe("");
  });

  it('renders dot with data-state="disconnected" for state="disconnected"', () => {
    mockElapsedReturn = "5s";

    const { container } = render(
      <ConnectionIndicator
        state="disconnected"
        disconnectedSince={Date.now() - 5000}
      />,
    );

    const dot = container.querySelector('[data-state="disconnected"]');
    expect(dot).toBeInTheDocument();
  });

  it('renders dot with data-state="reconnecting" for state="reconnecting"', () => {
    const { container } = render(
      <ConnectionIndicator
        state="reconnecting"
        disconnectedSince={Date.now() - 5000}
      />,
    );

    const dot = container.querySelector('[data-state="reconnecting"]');
    expect(dot).toBeInTheDocument();
  });

  it('shows elapsed time for "disconnected" state', () => {
    mockElapsedReturn = "2m";

    const { container } = render(
      <ConnectionIndicator
        state="disconnected"
        disconnectedSince={Date.now() - 120_000}
      />,
    );

    const elapsedSpan = container.querySelector('[class*="elapsedTime"]');
    expect(elapsedSpan).toBeInTheDocument();
    expect(elapsedSpan!.textContent).toBe("2m");
  });

  it('shows elapsed time for "reconnecting" state when available', () => {
    mockElapsedReturn = "5s";

    const { container } = render(
      <ConnectionIndicator
        state="reconnecting"
        disconnectedSince={Date.now() - 5000}
      />,
    );

    const elapsedSpan = container.querySelector('[class*="elapsedTime"]');
    expect(elapsedSpan).toBeInTheDocument();
    expect(elapsedSpan!.textContent).toBe("5s");
  });

  it("does NOT show elapsed time when elapsed string is empty", () => {
    mockElapsedReturn = "";

    const { container } = render(
      <ConnectionIndicator
        state="reconnecting"
        disconnectedSince={Date.now() - 5000}
      />,
    );

    const elapsedSpan = container.querySelector('[class*="elapsedTime"]');
    expect(elapsedSpan).not.toBeInTheDocument();
  });
});
