import { useEffect, useReducer, useRef } from "react";

/** The steady reveal rate while the buffer is short. */
export const BASE_CHARS_PER_SECOND = 90;
/** No text stays hidden longer than this after it arrives. */
export const MAX_LAG_MS = 300;
/** How long a newly revealed run takes to fade in. */
export const FADE_MS = 150;

const FRAME_MS = 1000 / 60;
const isSpace = (c: string | undefined) => c !== undefined && /\s/.test(c);

/**
 * Characters a second to reveal with `backlog` characters still hidden, the
 * oldest of them `ageMs` old: fast enough to show them all by MAX_LAG_MS.
 */
export function revealRate(backlog: number, ageMs = 0): number {
  const leftMs = Math.max(FRAME_MS, MAX_LAG_MS - ageMs);
  return Math.max(BASE_CHARS_PER_SECOND, (backlog * 1000) / leftMs);
}

/**
 * One frame's reveal: `pos` is the fractional reveal point, `shown` the
 * characters on screen. The new `shown` always ends a word.
 */
export function nextReveal(
  text: string,
  pos: number,
  shown: number,
  dtMs: number,
  ageMs = 0,
): { pos: number; shown: number } {
  const len = text.length;
  if (shown >= len) return { pos: len, shown: len };
  const rate = revealRate(len - shown, ageMs);
  const next = Math.min(len, pos + (rate * dtMs) / 1000);
  let k = Math.floor(next);
  if (k <= shown) return { pos: next, shown };
  while (k < len && isSpace(text[k - 1])) k++;
  while (k < len && !isSpace(text[k])) k++;
  return { pos: Math.max(next, k), shown: k };
}

/** A run revealed recently, from `from` to the next run, at this opacity. */
export interface FreshRun {
  from: number;
  opacity: number;
}

/** Whether the user asked the system for reduced motion. */
export const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  typeof window.matchMedia === "function" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/**
 * Streaming text revealed at a steady rate, a word at a time, catching up
 * when the model runs ahead. New words fade in. Text that is not streaming,
 * or under prefers-reduced-motion, shows as it is.
 */
export function useSmoothText(
  text: string,
  streaming: boolean,
): { text: string; fresh: FreshRun[] } {
  const smooth = streaming && !prefersReducedMotion();
  const [, render] = useReducer((n: number) => n + 1, 0);
  const s = useRef({
    text,
    pos: text.length,
    shown: text.length,
    runs: [] as { from: number; at: number }[],
    arrivals: [] as { end: number; at: number }[],
    now: 0,
    last: 0,
    frame: 0,
  }).current;
  s.text = text;

  useEffect(() => {
    if (!smooth) {
      if (s.frame) cancelAnimationFrame(s.frame);
      s.frame = s.last = 0;
      s.pos = s.shown = s.text.length;
      s.runs = [];
      s.arrivals = [];
      return;
    }
    if (s.frame || (s.shown >= s.text.length && s.runs.length === 0)) return;
    const step = (now: number) => {
      const dt = s.last ? now - s.last : FRAME_MS;
      s.last = s.now = now;
      if (s.shown > s.text.length) s.pos = s.shown = s.text.length;
      // When each piece of text arrived, to bound how long it stays hidden.
      const known = s.arrivals[s.arrivals.length - 1]?.end ?? s.shown;
      if (s.text.length > known)
        s.arrivals.push({ end: s.text.length, at: now });
      s.arrivals = s.arrivals.filter((a) => a.end > s.shown);
      const age = s.arrivals[0] ? now - s.arrivals[0].at : 0;
      const r = nextReveal(s.text, s.pos, s.shown, dt, age);
      if (r.shown > s.shown) s.runs.push({ from: s.shown, at: now });
      s.pos = r.pos;
      s.shown = r.shown;
      s.runs = s.runs.filter((run) => now - run.at < FADE_MS);
      s.frame =
        s.shown < s.text.length || s.runs.length
          ? requestAnimationFrame(step)
          : 0;
      if (!s.frame) s.last = 0;
      render();
    };
    s.frame = requestAnimationFrame(step);
  }, [smooth, text, s]);

  useEffect(
    () => () => {
      if (s.frame) cancelAnimationFrame(s.frame);
      s.frame = 0;
    },
    [s],
  );

  if (!smooth) return { text, fresh: [] };
  return {
    text: text.slice(0, Math.min(s.shown, text.length)),
    fresh: s.runs.map((run) => ({
      from: run.from,
      opacity: Math.min(1, (s.now - run.at) / FADE_MS),
    })),
  };
}
