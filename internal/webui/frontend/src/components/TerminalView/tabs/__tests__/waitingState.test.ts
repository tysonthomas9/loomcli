/**
 * Unit tests for the waiting-for-input predicate.
 */

import { describe, it, expect } from "vitest";

import {
  isWaitingForInput,
  WAITING_QUIET_MS,
  type CursorProbe,
  type WaitingInputs,
} from "../waitingState";

const NOW = 1_700_000_000_000;
const MID_LINE: CursorProbe = { cursorAtLineStart: false, altScreen: false };
const LINE_START: CursorProbe = { cursorAtLineStart: true, altScreen: false };
const ALT_SCREEN: CursorProbe = { cursorAtLineStart: true, altScreen: true };

function inputs(overrides: Partial<WaitingInputs> = {}): WaitingInputs {
  return {
    connected: true,
    hasEverOutput: true,
    lastOutputAt: NOW - WAITING_QUIET_MS,
    lastInputAt: NOW - WAITING_QUIET_MS - 1000,
    probe: MID_LINE,
    now: NOW,
    ...overrides,
  };
}

describe("isWaitingForInput", () => {
  it("badges a quiet tab with the cursor parked mid-line", () => {
    expect(isWaitingForInput(inputs())).toBe(true);
  });

  it("does not badge a running command that echoed a newline", () => {
    // The `sleep 60` case: quiet, but the cursor rests in column 0.
    expect(isWaitingForInput(inputs({ probe: LINE_START }))).toBe(false);
  });

  it("badges a quiet alternate-screen TUI even with the cursor at column 0", () => {
    expect(isWaitingForInput(inputs({ probe: ALT_SCREEN }))).toBe(true);
  });

  it("waits for the full quiet period", () => {
    expect(
      isWaitingForInput(inputs({ lastOutputAt: NOW - WAITING_QUIET_MS + 1 })),
    ).toBe(false);
  });

  it("does not badge when the user typed after the last output", () => {
    expect(isWaitingForInput(inputs({ lastInputAt: NOW - 100 }))).toBe(false);
  });

  it("does not badge a tab that has never produced output", () => {
    expect(
      isWaitingForInput(inputs({ hasEverOutput: false, lastOutputAt: 0 })),
    ).toBe(false);
  });

  it("does not badge a disconnected or ended tab", () => {
    expect(isWaitingForInput(inputs({ connected: false }))).toBe(false);
  });

  it("never guesses without the emulator", () => {
    expect(isWaitingForInput(inputs({ probe: null }))).toBe(false);
  });

  it("honours a custom quiet threshold", () => {
    const i = inputs({ lastOutputAt: NOW - 1000 });
    expect(isWaitingForInput(i, 500)).toBe(true);
    expect(isWaitingForInput(i, 2000)).toBe(false);
  });
});
