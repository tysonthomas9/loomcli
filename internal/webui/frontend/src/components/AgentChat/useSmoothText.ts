import { useEffect, useReducer, useRef } from "react";
import { countReplyWords } from "./ChatMarkdown";

/** The steady reveal rate while the buffer is short. */
export const BASE_CHARS_PER_SECOND = 90;
/** No text stays hidden longer than this after it arrives. */
export const MAX_LAG_MS = 300;
/** How long a newly revealed run takes to fade in. */
export const FADE_MS = 150;

const FRAME_MS = 1000 / 60;
const isSpace = (c: string | undefined) => c !== undefined && /\s/.test(c);

/** Keep a raw-word cut within the measured reply-word budget for this frame. */
function boundedReplyCut(
  text: string,
  shown: number,
  proposed: number,
  allowedWords: number,
  minimumWords: number,
  before: number,
): { cut: number; words: number } {
  let cut = Math.min(text.length, proposed);
  let words = countReplyWords(text.slice(0, cut));
  if (words - before <= allowedWords && words - before >= minimumWords)
    return { cut, words };
  const ends = [shown];
  for (let k = shown; k < text.length; ) {
    while (k < text.length && isSpace(text[k])) k++;
    while (k < text.length && !isSpace(text[k])) k++;
    if (k > (ends[ends.length - 1] ?? shown)) ends.push(k);
  }
  while (cut > shown && words - before > allowedWords) {
    cut = [...ends].reverse().find((end) => end < cut) ?? shown;
    words = countReplyWords(text.slice(0, cut));
  }
  if (words - before < minimumWords) {
    for (const end of ends) {
      if (end <= cut) continue;
      const nextWords = countReplyWords(text.slice(0, end));
      if (nextWords - before > allowedWords) break;
      cut = end;
      words = nextWords;
      if (words - before >= minimumWords) break;
    }
  }
  return { cut, words };
}

/**
 * Characters a second to reveal with `backlog` characters still hidden, the
 * oldest of them `ageMs` old: fast enough to show them all by MAX_LAG_MS.
 */
export function revealRate(backlog: number, ageMs = 0, reserveMs = 0): number {
  const leftMs = Math.max(FRAME_MS, MAX_LAG_MS - ageMs - reserveMs);
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
  reserveMs = 0,
): { pos: number; shown: number } {
  const len = text.length;
  if (shown >= len) return { pos: len, shown: len };
  const rate = revealRate(len - shown, ageMs, reserveMs);
  const next = Math.min(len, pos + (rate * dtMs) / 1000);
  let k = Math.floor(next);
  if (k <= shown) return { pos: next, shown };
  while (k < len && isSpace(text[k - 1])) k++;
  while (k < len && !isSpace(text[k])) k++;
  // The first paint has no earlier frame to establish a reading cadence.
  // Hold only this step to two whole words; later frames still use the
  // measured backlog rate to meet the 300ms arrival bound.
  if (shown === 0) {
    let firstEnd = 0;
    for (let words = 0; words < 2 && firstEnd < len; words++) {
      while (firstEnd < len && isSpace(text[firstEnd])) firstEnd++;
      while (firstEnd < len && !isSpace(text[firstEnd])) firstEnd++;
    }
    if (k > firstEnd) k = firstEnd;
  }
  if (shown === 0 && k < next) return { pos: k, shown: k };
  return { pos: Math.max(next, k), shown: k };
}

/** A revealed run, from `from` to the next run, at its fade's opacity. */
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
  newlyLive = false,
  arrivals?: readonly { end: number; at: number }[],
): { text: string; fresh: FreshRun[] } {
  const smooth = streaming && !prefersReducedMotion();
  const [, render] = useReducer((n: number) => n + 1, 0);
  // A new synced row begins with its first delta; an existing midstream row
  // keeps the text it already had when this component mounted.
  const initial = smooth && newlyLive ? 0 : text.length;
  const s = useRef({
    text,
    pos: initial,
    shown: initial,
    runs: [] as { from: number; at: number }[],
    arrivals: [] as { end: number; at: number; words: number }[],
    seenArrivalEnd: initial,
    shownWords: initial ? countReplyWords(text) : 0,
    intervals: [] as number[],
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
      s.seenArrivalEnd = s.text.length;
      s.shownWords = countReplyWords(s.text);
      s.intervals = [];
      return;
    }
    for (const arrival of arrivals ?? []) {
      if (arrival.end > s.seenArrivalEnd && arrival.end <= s.text.length) {
        s.arrivals.push({
          ...arrival,
          words: countReplyWords(s.text.slice(0, arrival.end)),
        });
        s.seenArrivalEnd = arrival.end;
      }
    }
    if (s.text.length > s.seenArrivalEnd) {
      s.arrivals.push({
        end: s.text.length,
        at: performance.now(),
        words: countReplyWords(s.text),
      });
      s.seenArrivalEnd = s.text.length;
    }
    if (s.frame || s.shown >= s.text.length) return;
    const step = (now: number) => {
      const dt = s.last ? now - s.last : FRAME_MS;
      if (s.last && dt > 0 && Number.isFinite(dt)) {
        s.intervals.push(dt);
        if (s.intervals.length > 12) s.intervals.shift();
      }
      s.last = s.now = now;
      if (s.shown > s.text.length) {
        s.pos = s.shown = s.text.length;
        s.shownWords = countReplyWords(s.text);
      }
      // When each piece of text arrived, to bound how long it stays hidden.
      if (s.text.length > s.seenArrivalEnd) {
        s.arrivals.push({
          end: s.text.length,
          at: now,
          words: countReplyWords(s.text),
        });
        s.seenArrivalEnd = s.text.length;
      }
      s.arrivals = s.arrivals.filter(
        (a) => a.end > s.shown && s.text.slice(s.shown, a.end).trim(),
      );
      // A burst is warranted only when two words on every faster observed
      // frame cannot meet a pending prefix's own deadline. The slower
      // observed interval plans progress early enough for React to commit
      // and the next frame to observe it, without increasing that burst cap.
      // Keep the same bounded recent window as the browser's cadence proof.
      const fastest = s.intervals.length ? Math.min(...s.intervals) : dt;
      const slowest = s.intervals.length ? Math.max(...s.intervals) : dt;
      const pending = s.arrivals.map((arrival) => {
        const age = now - arrival.at;
        const left = Math.max(0, MAX_LAG_MS - age);
        const remainingFrames = Math.max(
          0,
          Math.floor(left / Math.max(1, fastest)) - 1,
        );
        return {
          ...arrival,
          age,
          remainingFrames,
          progressFrames: Math.max(
            1,
            Math.floor((left - 3 * slowest) / Math.max(1, slowest)),
          ),
          neededNow: arrival.words - s.shownWords - 2 * remainingFrames,
        };
      });
      const target = pending.reduce<(typeof pending)[number] | undefined>(
        (best, arrival) =>
          !best || arrival.neededNow > best.neededNow ? arrival : best,
        undefined,
      );
      const age = target?.age ?? 0;
      const targetEnd = target?.end ?? s.text.length;
      const targetText = s.text.slice(0, targetEnd);
      // A new row's first paint is deliberately capped; reserve two nominal
      // frames so the remaining backlog still reaches the DOM within 300ms.
      const r = nextReveal(
        targetText,
        s.pos,
        s.shown,
        dt,
        age,
        newlyLive ? 2 * FRAME_MS : 0,
      );
      const targetWords = target?.words ?? countReplyWords(targetText);
      const shownWords = s.shownWords;
      const neededNow = target?.neededNow ?? targetWords - shownWords;
      // A single interval cannot establish a refresh cadence for catch-up.
      const allowedWords =
        s.shownWords === 0 || s.intervals.length < 2
          ? 2
          : Math.max(2, neededNow);
      const minimumWords =
        s.shownWords === 0
          ? 0
          : Math.min(
              allowedWords,
              Math.ceil(
                (targetWords - shownWords) / (target?.progressFrames ?? 1),
              ),
            );
      const bounded = boundedReplyCut(
        targetText,
        s.shown,
        r.shown,
        allowedWords,
        minimumWords,
        shownWords,
      );
      const cut = bounded.cut;
      if (cut > s.shown) s.runs.push({ from: s.shown, at: now });
      s.pos = cut === r.shown ? r.pos : cut;
      s.shown = cut;
      s.shownWords = bounded.words;
      // Runs stay (at full opacity) so the spans that show them never move.
      const fading = s.runs.some((run) => now - run.at < FADE_MS);
      s.frame =
        s.shown < s.text.length || fading ? requestAnimationFrame(step) : 0;
      if (!s.frame) s.last = 0;
      render();
    };
    s.frame = requestAnimationFrame(step);
  }, [smooth, text, s, newlyLive, arrivals]);

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
