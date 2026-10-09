import assert from 'node:assert/strict';
import { test } from 'node:test';
import { deriveMotionFacts, type MotionFactsInput } from './motion-facts.js';
import { expectedProjectionIdentity, projectMarkdown, projectRendererTree, type MotionProjection, type RendererNode } from './projections/index.js';

function fixture(late = 300): MotionFactsInput {
  const answer = 'one two three four five six seven eight';
  const times = [0,10,20,30,40,50,275,late,350];
  const stamp = (monoMs: number, offset = 2) => ({ clockId: 'document-clock', monoMs, utcMs: 1000 + monoMs, phase: Math.round(monoMs * 10) + offset });
  const raws = ['one two', 'one two three four five six', answer];
  const frameTicks = [5,6,7];
  const frames = raws.map((text,index) => ({ index, sampleIndex: frameTicks[index]!, tickIndex: frameTicks[index]!,
    time: stamp(times[frameTicks[index]!]!), text, visibleText: text, contentText: text, streaming: index < 2, marker: index === 2 }));
  const projection = projectMarkdown({ answer, frames:frames.map(({text,streaming}) => ({text,streaming})), arrivals: [answer] }, { identity: structuredClone(expectedProjectionIdentity) }) as MotionProjection;
  return { complete: true, clock: { id: 'document-clock', domain: 'page-monotonic', epochUtcMs: 1000, maxErrorMs: 1 },
    ticks: times.map((at,index) => ({ index, sampleIndex: index, time: stamp(at), rafAt: at })), frames,
    arrivals: [{ index: 0, sampleIndex: 0, time: stamp(0,1), sourceId: 'saved-sse-source', itemId: 'message-1', delta: answer }],
    completion: { time: stamp(290,3), sampleIndex: 1, eventId: 'saved-completion', itemId: 'message-1' },
    savedAnswer:answer, projection, pacing: { wordsPerTick: 2, deadlineMs: 300, cadenceWindowTicks: 12 }, maxRelationships: 10000 };
}

test('measured pacing retains unchanged RAF ticks, first-live frame and independent backlog facts', () => {
  const result = deriveMotionFacts(fixture());
  assert.equal(result.evidenceClass, 'deterministic');
  assert.equal(result.ticks.length, 9);
  assert.deepEqual(result.frames.map(frame => [frame.words,frame.addedWords]), [[2,2],[6,4],[8,2]]);
  assert.equal(result.firstLiveFrameIndex, 0);
  assert.deepEqual(result.frames[1]!.positiveIntervalsMs, [10,10,10,10,10,225]);
  assert.equal(result.frames[1]!.minimumIntervalMs, 10);
  assert.deepEqual(result.frames[1]!.maximumPressure, { arrivalIndex:0,sampleIndex:0,sourceId:'saved-sse-source',targetWords:8,
    requiredMinSourceUtf16:39,deadlineMonoMs:300,horizonRemainingMs:50,futureTickCount:1,requiredNow:4,firstTargetFrameIndex:2 });
  assert.equal(result.arrivals[0]!.renderedFrameIndex, 2);
  assert.equal(result.arrivals[0]!.lagMs, 300);
  assert.equal(result.frames[2]!.completionLagMs, 10);
  assert.equal(result.frames[2]!.terminalTextEqual, true);
  assert.deepEqual(result.pacing, { wordsPerTick:2,deadlineMs:300,cadenceWindowTicks:12 });
});

test('300.9ms remains a measured late result; caller expectation determines acceptance', () => {
  const result = deriveMotionFacts(fixture(300.9));
  assert.equal(result.arrivals[0]!.lagMs, 300.9);
  assert.equal(result.frames[1]!.maximumPressure!.futureTickCount, 0);
  assert.equal(result.frames[1]!.maximumPressure!.requiredNow, 6);
  assert.equal(result.frames[1]!.maximumPressure!.firstTargetFrameIndex, null);
  assert.equal(result.arrivals[0]!.lagMs! <= 300, false);
  assert.equal(result.arrivals[0]!.lagMs! <= 301, true);
  assert.equal('passed' in result, false);
});

test('observed delta prefix permits a full saved-completion suffix without inventing arrivals', () => {
  const input = fixture();
  input.frames = [input.frames[0]!, {...input.frames[2]!, index:1}];
  input.arrivals[0]!.delta = 'one two';
  input.projection = projectMarkdown({answer:input.savedAnswer,frames:input.frames.map(({text,streaming})=>({text,streaming})),arrivals:['one two']},
    {identity:structuredClone(expectedProjectionIdentity)}) as MotionProjection;
  const result = deriveMotionFacts(input);
  assert.equal(result.observedDeltaUtf16, 7);
  assert.equal(result.savedSourceUtf16, 39);
  assert.equal(result.frames[1]!.projection.maxSourceUtf16, 39);
  assert.equal(result.frames[1]!.arrivedSourceUtf16, 7);
  assert.equal(result.frames[1]!.completionLagMs, 10);
  assert.equal(result.arrivals[0]!.renderedFrameIndex, 0);
  input.arrivals[0]!.delta = 'foreign';
  assert.throws(()=>deriveMotionFacts(input),/not a prefix/);
});

test('terminal mismatch remains unmappable; late completion is a measured negative fact', () => {
  const input = fixture();
  input.completion.time.monoMs = 320; input.completion.time.utcMs = 1320; input.completion.time.phase = 3203;
  const result = deriveMotionFacts(input);
  assert.equal(result.frames[2]!.completionLagMs, -20);
  assert.equal(result.frames[2]!.completionPhaseDelta < 0, true);
  input.completion.time.monoMs = 0; input.completion.time.utcMs = 1000; input.completion.time.phase = 0;
  assert.throws(()=>deriveMotionFacts(input));
  const unmappable = fixture();
  unmappable.projection = projectMarkdown({answer:unmappable.savedAnswer,frames:[{text:'foreign terminal',streaming:false}],arrivals:[unmappable.savedAnswer]},
    {identity:structuredClone(expectedProjectionIdentity)}) as MotionProjection;
  unmappable.frames = [{...unmappable.frames[2]!,index:0}];
  assert.throws(()=>deriveMotionFacts(unmappable),/cannot be mapped/);
  const lateFlush = fixture(300.9);
  lateFlush.completion.time = {...lateFlush.completion.time,monoMs:0.1,utcMs:1000.1,phase:3};
  const measured = deriveMotionFacts(lateFlush);
  assert.equal(measured.frames[2]!.completionLagMs,300.9-0.1);
  assert.equal(measured.frames[2]!.completionLagMs>300,true);
});

test('same-clock timestamp ties retain shared event ordering instead of dropping arrivals', () => {
  const input = fixture();
  input.frames[0]!.tickIndex = 0; input.frames[0]!.sampleIndex = 0;
  input.frames[0]!.time = { ...input.ticks[0]!.time };
  const result = deriveMotionFacts(input);
  assert.equal(result.frames[0]!.time.monoMs, result.arrivals[0]!.time.monoMs);
  assert.equal(result.frames[0]!.time.phase > result.arrivals[0]!.time.phase, true);
  assert.equal(result.frames[0]!.arrivedSourceUtf16, 39);
});

test('an insufficient measured RAF horizon is reported without inferred future ticks', () => {
  const input = fixture();
  input.ticks = input.ticks.slice(0,7);
  input.frames[2]!.tickIndex = null; input.frames[2]!.sampleIndex = 7;
  const result = deriveMotionFacts(input);
  assert.equal(result.horizon.monoMs, 275);
  assert.equal(result.frames[1]!.maximumPressure!.horizonRemainingMs, -25);
  assert.equal(result.frames[1]!.maximumPressure!.futureTickCount, 0);
});

test('pacing parameters affect facts without embedding the original thresholds', () => {
  const input = fixture(); input.pacing = { wordsPerTick:1,deadlineMs:320,cadenceWindowTicks:3 };
  const result = deriveMotionFacts(input);
  assert.deepEqual(result.pacing, input.pacing);
  assert.equal(result.frames[1]!.maximumPressure!.requiredNow, 5);
  assert.deepEqual(result.frames[1]!.recentTickIndices, [4,5,6]);
});

test('missing, duplicate, unordered, foreign-clock, unmappable and over-budget history fails closed', () => {
  const changes: ((input: MotionFactsInput) => void)[] = [
    input => { input.complete = false; },
    input => { input.ticks[2]!.index = 3; },
    input => { input.ticks[2]!.sampleIndex = 1; },
    input => { input.arrivals[0]!.time.phase = input.ticks[0]!.time.phase; },
    input => { input.frames[1]!.time.clockId = 'foreign'; },
    input => { input.frames[1]!.time.utcMs += 2; },
    input => { input.frames[1]!.tickIndex = 5; },
    input => { input.projection.frames[1] = null; },
    input => { input.frames[1]!.sampleIndex = 0; },
    input => { input.completion.itemId = 'foreign'; },
    input => { input.maxRelationships = 1; },
    input => { input.projection.arrivals[0]!.sourceUtf16--; },
  ];
  for (const change of changes) { const input = fixture(); change(input); assert.throws(() => deriveMotionFacts(input)); }
});

test('observed visible text is retained independently from projected reply content and UTF16 counts', () => {
  const input = fixture(); input.frames[1]!.visibleText = 'measured browser innerText 😀';
  const result = deriveMotionFacts(input);
  assert.equal(result.frames[1]!.visibleText, 'measured browser innerText 😀');
  assert.equal(result.frames[1]!.visibleUtf16, 29);
  assert.equal(result.frames[1]!.words, 6);
});

test('observed reply-content differences remain measured facts for independent YAML expectations', () => {
  const input = fixture(); input.frames[1]!.contentText = 'one two wrong';
  const result = deriveMotionFacts(input);
  assert.deepEqual(result.frames[1]!.contentWords, ['one','two','wrong']);
  assert.deepEqual(result.frames[1]!.projectedContentWords, ['one','two','three','four','five','six']);
  assert.equal(result.frames[1]!.words, 3);
});

test('retractions and terminal snapshots retain sample indices without manufacturing RAF ticks', () => {
  const input = fixture();
  const frame = input.frames[1]!; frame.text = 'one'; frame.contentText = 'one'; frame.visibleText = 'one';
  const source = input.arrivals[0]!.delta;
  input.projection = projectMarkdown({ answer:source,frames:input.frames.map(({text,streaming}) => ({text,streaming})),arrivals:[source] }, { identity:structuredClone(expectedProjectionIdentity) }) as MotionProjection;
  input.frames[2]!.tickIndex = null; input.frames[2]!.sampleIndex = 9;
  input.frames[2]!.time.phase++;
  const result = deriveMotionFacts(input);
  assert.equal(result.frames[1]!.retractedWords, 1);
  assert.equal(result.frames[1]!.addedWords, 0);
  assert.equal(result.frames[2]!.tickIndex, null);
  assert.equal(result.frames[2]!.sampleIndex, 9);
  assert.equal(result.ticks.length, 9);
});

test('actual DOM boundary fixture preserves table tabs, BR, block boundaries and chrome exclusion', () => {
  const t = (text:string):RendererNode => ({kind:'text',text});
  const tree:RendererNode = {kind:'element',tag:'DIV',children:[
    {kind:'element',tag:'DIV',chrome:'code-header',separator:'codeblockHeader',children:[t('json'),t('Copy')]},
    {kind:'element',tag:'P',children:[t('first'),{kind:'element',tag:'BR',children:[]},t('second')]},
    {kind:'element',tag:'TABLE',children:[{kind:'element',tag:'TR',children:[
      {kind:'element',tag:'TH',children:[t('name')]},{kind:'element',tag:'TD',children:[t('value')]}]}]},
    {kind:'element',tag:'PRE',children:[t('😀 code')]},
  ]};
  const result = projectRendererTree({complete:true,streaming:true,tree});
  assert.equal(result.raw, 'jsonCopyfirstsecondnamevalue😀 code');
  assert.equal(result.content, 'first\nsecond\nname\tvalue\n😀 code');
  assert.equal(result.contentWords, 6);
});
