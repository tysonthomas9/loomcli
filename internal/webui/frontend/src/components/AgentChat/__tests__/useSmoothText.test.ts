/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  BASE_CHARS_PER_SECOND,
  FADE_MS,
  nextReveal,
  revealRate,
  useSmoothText,
} from "../useSmoothText";

const FRAME = 1000 / 60;

describe("revealRate", () => {
  it("holds the base rate for a short backlog", () => {
    expect(revealRate(5)).toBe(BASE_CHARS_PER_SECOND);
  });

  it("speeds up so the backlog drains within about 300ms", () => {
    expect(revealRate(900)).toBeCloseTo(3000);
  });
});

describe("nextReveal", () => {
  const text = "The quick brown fox jumps over the lazy dog";

  it("reveals whole words only", () => {
    const shown = nextReveal(text, 0, 0, FRAME).shown;
    expect(shown).toBe(3); // "The", never "Th"
    expect(text[shown]).toBe(" ");
  });

  it("reveals at most about two words a frame at a model's 300 chars/s", () => {
    // A 300 chars/s model keeps about 90 characters (300ms) buffered.
    const long = "word ".repeat(200);
    let pos = 0;
    let shown = 0;
    const steps: number[] = [];
    for (let target = 0; target < 600; target += 5) {
      const r = nextReveal(long.slice(0, target), pos, shown, FRAME);
      steps.push(r.shown - shown);
      pos = r.pos;
      shown = r.shown;
    }
    expect(Math.max(...steps)).toBeLessThanOrEqual(10); // two "word " units
    expect(600 - shown).toBeLessThanOrEqual(100); // never far behind
  });

  it("never passes the text it has", () => {
    expect(nextReveal("abc", 0, 0, 10_000).shown).toBe(3);
  });
});

describe("useSmoothText", () => {
  let frames: FrameRequestCallback[] = [];
  let now = 0;
  const tick = (ms = FRAME) =>
    act(() => {
      now += ms;
      const run = frames;
      frames = [];
      run.forEach((f) => f(now));
    });

  beforeEach(() => {
    frames = [];
    now = 0;
    vi.stubGlobal("requestAnimationFrame", (f: FrameRequestCallback) => {
      frames.push(f);
      return frames.length;
    });
    vi.stubGlobal("cancelAnimationFrame", () => {});
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q }));
  });

  afterEach(() => vi.unstubAllGlobals());

  it("starts from the text already there and reveals what arrives", () => {
    const { result, rerender } = renderHook(({ t, s }) => useSmoothText(t, s), {
      initialProps: { t: "Hello", s: true },
    });
    expect(result.current.text).toBe("Hello");
    rerender({ t: "Hello there my friend", s: true });
    expect(result.current.text).toBe("Hello");
    tick();
    expect(result.current.text).toBe("Hello there");
    for (let i = 0; i < 60; i++) tick();
    expect(result.current.text).toBe("Hello there my friend");
  });

  it("fades new words in, by opacity alone", () => {
    const { result, rerender } = renderHook(({ t, s }) => useSmoothText(t, s), {
      initialProps: { t: "Hi", s: true },
    });
    rerender({ t: "Hi you", s: true });
    tick();
    tick();
    expect(result.current.fresh[0]?.from).toBe(2);
    expect(result.current.fresh[0]?.opacity).toBeLessThan(1);
    for (let i = 0; i < Math.ceil(FADE_MS / FRAME) + 1; i++) tick();
    expect(result.current.fresh).toEqual([]);
  });

  it("flushes everything at once when the turn ends", () => {
    const { result, rerender } = renderHook(({ t, s }) => useSmoothText(t, s), {
      initialProps: { t: "a", s: true },
    });
    rerender({ t: "a " + "lots of words ".repeat(50), s: true });
    tick();
    rerender({ t: "a " + "lots of words ".repeat(50) + "end", s: false });
    expect(result.current.text).toBe(
      "a " + "lots of words ".repeat(50) + "end",
    );
    expect(result.current.fresh).toEqual([]);
  });

  it("renders text as received under prefers-reduced-motion", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({
      matches: q.includes("reduce"),
      media: q,
    }));
    const { result, rerender } = renderHook(({ t, s }) => useSmoothText(t, s), {
      initialProps: { t: "a", s: true },
    });
    rerender({ t: "a b c d e f", s: true });
    expect(result.current.text).toBe("a b c d e f");
    expect(result.current.fresh).toEqual([]);
    expect(frames).toHaveLength(0);
  });
});
