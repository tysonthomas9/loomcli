import { ClockSchema, SampleTimeSchema } from '@tysonthomas9/aft/types';
import type { z } from 'zod';
import { requireFact } from './protocol.js';
import { javascriptWords, words, type ArrivalProjection, type MotionProjection } from './projections/index.js';

type Time = z.infer<typeof SampleTimeSchema>;
type Clock = z.infer<typeof ClockSchema>;
export interface MotionTick { index: number; sampleIndex: number; time: Time; rafAt: number }
export interface MotionFrame {
  index: number; sampleIndex: number; tickIndex: number | null; time: Time;
  text: string; visibleText: string; contentText: string; streaming: boolean; marker: boolean;
}
export interface MotionArrival {
  index: number; sampleIndex: number; time: Time; sourceId: string; itemId: string; delta: string;
}
export interface MotionCompletion { time: Time; sampleIndex: number; eventId: string; itemId: string }
export interface MotionPacing { wordsPerTick: number; deadlineMs: number; cadenceWindowTicks: number }
/** Internal normal form after canonical capture validation. This pure computation
 * grants no capture authority and is deterministic transformation evidence only. */
export interface MotionFactsInput {
  complete: boolean; clock: Clock; ticks: readonly MotionTick[]; frames: readonly MotionFrame[];
  arrivals: readonly MotionArrival[]; completion: MotionCompletion; projection: MotionProjection;
  savedAnswer: string; pacing: MotionPacing; maxRelationships: number;
}
function ordered(time: Time, prior: Time): boolean { return time.phase > prior.phase && time.monoMs >= prior.monoMs; }
function sameTime(a: Time, b: Time): boolean { return a.clockId === b.clockId && a.phase === b.phase && a.monoMs === b.monoMs && a.utcMs === b.utcMs; }
function earlier(time: Time, than: Time): boolean { return time.phase < than.phase && time.monoMs <= than.monoMs; }
function natural(value: number): boolean { return Number.isSafeInteger(value) && value >= 0; }

export function deriveMotionFacts(input: MotionFactsInput) {
  requireFact(input.complete, 'incomplete-pages', 'Motion history is incomplete');
  const clock = ClockSchema.parse(input.clock);
  requireFact(input.ticks.length > 0 && input.frames.length > 0 && input.arrivals.length > 0 &&
    input.ticks.length <= 10000 && input.frames.length <= 10001 && input.arrivals.length <= 5000,
  'incomplete-pages', 'Motion history is missing or exceeds its work bound');
  requireFact(Number.isFinite(input.pacing.deadlineMs) && input.pacing.deadlineMs > 0 &&
    Number.isSafeInteger(input.pacing.wordsPerTick) && input.pacing.wordsPerTick > 0 &&
    Number.isSafeInteger(input.pacing.cadenceWindowTicks) && input.pacing.cadenceWindowTicks > 0 &&
    input.pacing.cadenceWindowTicks <= 10000 && Number.isSafeInteger(input.maxRelationships) &&
    input.maxRelationships > 0 && input.maxRelationships <= 1000000,
  'observation-failed', 'Motion pacing parameters or work bound are invalid');
  let relationships = 0;
  const spend = () => requireFact(++relationships <= input.maxRelationships, 'incomplete-pages', 'Motion relationship budget exhausted');
  let textUnits = 0;
  const text = (value: string) => {
    requireFact(typeof value === 'string' && value.length <= 128000,
      'incomplete-pages', 'Motion text is missing or exceeds its text bound');
    textUnits += value.length;
    requireFact(textUnits <= 4000000, 'incomplete-pages', 'Motion history text budget exhausted');
  };
  const time = (value: Time) => {
    const parsed = SampleTimeSchema.parse(value);
    requireFact(parsed.clockId === clock.id && Math.abs(parsed.utcMs - clock.epochUtcMs - parsed.monoMs) <= clock.maxErrorMs,
      'identity-mismatch', 'Motion time does not belong to its calibrated clock');
  };
  const ordinals = new Set<number>();
  const observe = (value: Time) => { time(value); requireFact(!ordinals.has(value.phase), 'identity-mismatch', 'Motion event ordinal is duplicated'); ordinals.add(value.phase); };
  input.ticks.forEach((tick, index) => {
    observe(tick.time);
    requireFact(tick.index === index && natural(tick.sampleIndex) && Number.isFinite(tick.rafAt) && tick.rafAt >= 0 &&
      (index === 0 || (ordered(tick.time, input.ticks[index - 1]!.time) && tick.sampleIndex > input.ticks[index - 1]!.sampleIndex && tick.rafAt > input.ticks[index - 1]!.rafAt)),
    'incomplete-pages', 'Motion RAF history is duplicate, gapped or unordered');
  });
  const ids = new Set<string>();
  text(input.savedAnswer);
  const savedSourceUtf16 = input.savedAnswer.length;
  let observedDeltaUtf16 = 0;
  requireFact(input.projection.arrivals.length === input.arrivals.length && input.projection.frames.length === input.frames.length,
    'incomplete-pages', 'Motion projection does not cover its entire history');
  input.arrivals.forEach((arrival, index) => {
    observe(arrival.time);
    const projected = input.projection.arrivals[index]!;
    text(arrival.delta);
    observedDeltaUtf16 += arrival.delta.length;
    const id = `${arrival.sourceId}\u0000${arrival.sampleIndex}`;
    requireFact(arrival.index === index && natural(arrival.sampleIndex) && arrival.sourceId.length > 0 &&
      arrival.itemId === input.completion.itemId && typeof arrival.delta === 'string' && !ids.has(id) &&
      (index === 0 || ordered(arrival.time, input.arrivals[index - 1]!.time)) && projected.sourceUtf16 === observedDeltaUtf16,
    'identity-mismatch', 'Motion arrival identity, order or source projection differs');
    ids.add(id);
  });
  requireFact(input.savedAnswer.startsWith(input.arrivals.map(arrival => arrival.delta).join('')) &&
    savedSourceUtf16 === input.projection.sourceUtf16,
    'identity-mismatch', 'Motion arrivals are not a prefix of their saved source');
  observe(input.completion.time);
  requireFact(input.completion.eventId.length > 0 && natural(input.completion.sampleIndex) &&
    earlier(input.arrivals.at(-1)!.time, input.completion.time), 'identity-mismatch', 'Motion completion precedes its arrivals');
  let previousWords = 0;
  const frames = input.frames.map((frame, index) => {
    time(frame.time);
    text(frame.text); text(frame.visibleText); text(frame.contentText);
    const mapped = input.projection.frames[index];
    requireFact(mapped !== null && mapped !== undefined, 'incomplete-pages', 'Motion frame cannot be mapped to saved Markdown');
    requireFact(frame.index === index && natural(frame.sampleIndex) && typeof frame.text === 'string' &&
      typeof frame.visibleText === 'string' && typeof frame.contentText === 'string' &&
      frame.text.length <= 128000 && frame.visibleText.length <= 128000 && frame.contentText.length <= 128000 &&
      typeof frame.streaming === 'boolean' && typeof frame.marker === 'boolean' &&
      mapped.minSourceUtf16 <= mapped.maxSourceUtf16 &&
      mapped.maxSourceUtf16 <= savedSourceUtf16 && (index === 0 || (ordered(frame.time, input.frames[index - 1]!.time) && frame.sampleIndex > input.frames[index - 1]!.sampleIndex)),
    'identity-mismatch', 'Motion frame identity, content or mapping differs');
    if (frame.tickIndex !== null) {
      const tick = input.ticks[frame.tickIndex];
      requireFact(natural(frame.tickIndex) && tick && tick.sampleIndex === frame.sampleIndex && sameTime(frame.time, tick.time),
        'identity-mismatch', 'Motion frame does not belong to its recorded RAF tick');
    } else {
      requireFact(index === input.frames.length - 1, 'incomplete-pages', 'Only terminal snapshot may lack a RAF tick');
      observe(frame.time);
    }
    const contentWords = words(frame.contentText);
    const count = contentWords.length, addedWords = Math.max(0, count - previousWords);
    const retractedWords = Math.max(0, previousWords - count); previousWords = count;
    return { ...frame, projection: mapped, rawUtf16: frame.text.length, visibleUtf16: frame.visibleText.length,
      contentUtf16: frame.contentText.length, words: count, javascriptWords: javascriptWords(frame.contentText).length,
      contentWords, projectedContentWords: words(mapped.content), projectedVisibleWords: words(mapped.visible), visibleWords: words(frame.visibleText),
      addedWords, retractedWords };
  });
  const ticks = input.ticks.map((tick, index) => ({ ...tick, intervalMs: index === 0 ? null : tick.time.monoMs - input.ticks[index - 1]!.time.monoMs,
    rafLagMs: tick.time.monoMs - tick.rafAt }));
  const events = [...input.ticks.map(tick => tick.time), ...input.arrivals.map(arrival => arrival.time), input.completion.time,
    ...input.frames.filter(frame => frame.tickIndex === null).map(frame => frame.time)].sort((a,b) => a.phase - b.phase);
  for (let index = 1; index < events.length; index++) requireFact(ordered(events[index]!, events[index-1]!),
    'identity-mismatch', 'Motion shared event order contradicts its clock');
  const horizon = input.ticks.at(-1)!.time;
  const frameFacts = frames.map((frame, index) => {
    const prior = frames[index - 1];
    let previousSourceUtf16 = 0;
    for (let i = 0; i < index; i++) { spend(); previousSourceUtf16 = Math.max(previousSourceUtf16, frames[i]!.projection.minSourceUtf16); }
    const previousVisibleWords = prior?.words ?? 0;
    const earlierArrivals: { arrival: MotionArrival; projection: ArrivalProjection }[] = [];
    let arrivedSourceUtf16 = 0;
    input.arrivals.forEach((arrival, i) => { spend(); if (earlier(arrival.time, frame.time)) {
      arrivedSourceUtf16 += arrival.delta.length; earlierArrivals.push({ arrival, projection: input.projection.arrivals[i]! });
    } });
    const recent = frame.tickIndex === null ? [] : ticks.slice(Math.max(0, frame.tickIndex + 1 - input.pacing.cadenceWindowTicks), frame.tickIndex + 1);
    const positiveIntervals = recent.slice(1).map(tick => tick.intervalMs!).filter(value => value > 0);
    const candidates = earlierArrivals.filter(({ projection }) => projection.contentChanged &&
      projection.requiredMinSourceUtf16 > previousSourceUtf16 && projection.projectedWords > previousVisibleWords).map(({ arrival, projection }) => {
      const deadlineMonoMs = arrival.time.monoMs + input.pacing.deadlineMs;
      let futureTicks = 0;
      for (const tick of ticks) { spend(); if (frame.tickIndex !== null && tick.index > frame.tickIndex && tick.time.monoMs <= deadlineMonoMs) futureTicks++; }
      let firstTargetFrameIndex: number | null = null;
      for (let i = index; i < frames.length; i++) { spend(); if (frames[i]!.time.monoMs <= deadlineMonoMs && frames[i]!.words >= projection.projectedWords) { firstTargetFrameIndex = i; break; } }
      return { arrivalIndex: arrival.index, sampleIndex: arrival.sampleIndex, sourceId: arrival.sourceId,
        targetWords: projection.projectedWords, requiredMinSourceUtf16: projection.requiredMinSourceUtf16,
        deadlineMonoMs, horizonRemainingMs: horizon.monoMs - deadlineMonoMs, futureTickCount: futureTicks,
        requiredNow: projection.projectedWords - previousVisibleWords - input.pacing.wordsPerTick * futureTicks, firstTargetFrameIndex };
    });
    let maximumPressure: typeof candidates[number] | null = null;
    for (const candidate of candidates) { spend(); if (maximumPressure === null || candidate.requiredNow > maximumPressure.requiredNow) maximumPressure = candidate; }
    return { ...frame, previousVisibleWords, previousSourceUtf16, arrivedSourceUtf16,
      earlierArrivalIndices: earlierArrivals.map(({arrival}) => arrival.index),
      recentTickIndices: recent.map(tick => tick.index), positiveIntervalsMs: positiveIntervals,
      minimumIntervalMs: positiveIntervals.length === 0 ? null : Math.min(...positiveIntervals),
      backlogWords: (earlierArrivals.at(-1)?.projection.projectedWords ?? 0) - previousVisibleWords,
      candidates, maximumPressure, completionLagMs: frame.time.monoMs - input.completion.time.monoMs,
      completionPhaseDelta: frame.time.phase - input.completion.time.phase,
      terminalTextEqual: frame.text === input.projection.terminal };
  });
  const arrivals = input.arrivals.map((arrival, index) => {
    const projection = input.projection.arrivals[index]!;
    let rendered: typeof frames[number] | null = null;
    if (projection.contentChanged) for (const frame of frames) { spend(); if (earlier(arrival.time, frame.time) && frame.projection.minSourceUtf16 >= projection.requiredMinSourceUtf16) { rendered = frame; break; } }
    return { ...arrival, projection, deadlineMonoMs: arrival.time.monoMs + input.pacing.deadlineMs,
      renderedFrameIndex: rendered?.index ?? null, renderedSampleIndex: rendered?.sampleIndex ?? null,
      renderedMinSourceUtf16: rendered?.projection.minSourceUtf16 ?? null,
      lagMs: rendered === null ? null : rendered.time.monoMs - arrival.time.monoMs,
      utcLagMs: rendered === null ? null : rendered.time.utcMs - arrival.time.utcMs };
  });
  return { evidenceClass: 'deterministic' as const, clock, pacing: { ...input.pacing }, ticks,
    frames: frameFacts, arrivals, completion: { ...input.completion }, horizon,
    firstLiveFrameIndex: frames.find(frame => frame.streaming && frame.words > 0)?.index ?? null,
    observedDeltaUtf16, savedSourceUtf16, relationshipsExamined: relationships };
}
export type MotionFacts = ReturnType<typeof deriveMotionFacts>;
