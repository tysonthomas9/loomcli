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

  it("speeds up as the oldest hidden text nears 300ms", () => {
    expect(revealRate(900, 200)).toBeCloseTo(9000);
    expect(revealRate(900, 400)).toBeCloseTo(900 * 60);
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
    vi.spyOn(performance, "now").mockImplementation(() => now);
    vi.stubGlobal("requestAnimationFrame", (f: FrameRequestCallback) => {
      frames.push(f);
      return frames.length;
    });
    vi.stubGlobal("cancelAnimationFrame", () => {});
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q }));
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

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

  it("reveals a new live row from zero and flushes its backlog at completion", () => {
    const first = "word ".repeat(15).trim();
    const { result, rerender } = renderHook(
      ({ t, s }) => useSmoothText(t, s, true),
      { initialProps: { t: first, s: true } },
    );
    expect(result.current.text).toBe("");
    tick();
    expect(result.current.text.trim().split(/\s+/)).toHaveLength(1);
    rerender({ t: `${first} final`, s: false });
    expect(result.current.text).toBe(`${first} final`);
    expect(result.current.fresh).toEqual([]);
  });

  it("advances toward a later batched prefix when its deadline is more urgent", () => {
    const text = "one two three four five";
    const arrivals = [
      { end: 4, at: 0 },
      { end: text.length, at: 0 },
    ];
    const { result } = renderHook(() =>
      useSmoothText(text, true, true, arrivals),
    );
    tick();
    expect(result.current.text.trim().split(/\s+/).length).toBeLessThanOrEqual(
      2,
    );
    tick();
    expect(result.current.text.startsWith("one two")).toBe(true);
  });

  it("keeps an ordinary 36-word first packet to two words on its second frame", () => {
    const packet = "word ".repeat(35) + "word";
    const { result } = renderHook(() => useSmoothText(packet, true, true));
    expect(result.current.text).toBe("");
    tick();
    const first = result.current.text.trim().split(/\s+/).length;
    tick();
    const second = result.current.text.trim().split(/\s+/).length;
    expect(first).toBe(2);
    expect(second - first).toBeLessThanOrEqual(2);
  });

  it("shows a new live row immediately under reduced motion", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true }));
    const first = "word ".repeat(15).trim();
    const { result } = renderHook(() => useSmoothText(first, true, true));
    expect(result.current.text).toBe(first);
    expect(result.current.fresh).toEqual([]);
    expect(frames).toHaveLength(0);
  });

  it("shows a 900-character burst within 300ms of its arrival", () => {
    const burst = "word ".repeat(180);
    const { result, rerender } = renderHook(({ t, s }) => useSmoothText(t, s), {
      initialProps: { t: "", s: true },
    });
    rerender({ t: burst, s: true });
    tick(); // the frame that sees it arrive
    const arrived = now;
    while (result.current.text.length < burst.length && now - arrived < 1000)
      tick();
    expect(now - arrived).toBeLessThanOrEqual(300 + FRAME);
  });

  it("shows every chunk of a bursty stream within 300ms", () => {
    // 120 characters every 24 frames (400ms), as chunked SDKs flush.
    const words = "The quick brown fox jumps over the lazy dog. ".repeat(20);
    const { result, rerender } = renderHook(({ t, s }) => useSmoothText(t, s), {
      initialProps: { t: "", s: true },
    });
    const pending: { end: number; at: number }[] = [];
    let worst = 0;
    for (let f = 0, i = 0; i < words.length || pending.length; f++) {
      if (i < words.length && f % 24 === 0) {
        i = Math.min(words.length, i + 120);
        rerender({ t: words.slice(0, i), s: true });
        pending.push({ end: i, at: now });
      }
      tick();
      while (pending[0] && result.current.text.length >= pending[0].end)
        worst = Math.max(worst, now - pending.shift()!.at);
    }
    expect(worst).toBeLessThanOrEqual(300 + 2 * FRAME);
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
    // The run stays, faded in, so the span showing it never moves.
    expect(result.current.fresh).toEqual([{ from: 2, opacity: 1 }]);
    expect(frames).toHaveLength(0);
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
