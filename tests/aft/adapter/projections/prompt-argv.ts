import { boundedText, requireProjection } from './text.js';

export const promptArgvContract = 'loom-lead-prompt-argv-precedence' as const;
export type PromptExpectation =
  | { kind: 'literal-template'; agentName: string; fixtureText: string }
  | { kind: 'inline-precedence'; inlineText: string }
  | { kind: 'builtin-lead' };
export interface ArgvObservation { complete: boolean; records: string[][] }

// Validates captured/double argv only. It does not build argv or launch lead.
// Passing injected records proves comparator behavior, not the production CLI.
export function comparePromptArgv(expectation: PromptExpectation, observation: ArgvObservation) {
  requireProjection(observation?.complete === true, 'incomplete-observation', 'Argv observation is incomplete');
  requireProjection(Array.isArray(observation.records) && observation.records.length > 0 && observation.records.length <= 256,
    'invalid-input', 'Argv records are missing or unbounded');
  for (const record of observation.records) {
    requireProjection(Array.isArray(record) && record.length > 0 && record.length <= 256, 'invalid-input', 'Invalid argv record');
    record.forEach(arg => boundedText(arg));
  }
  const records = observation.records.filter(record => record[0] === 'exec');
  const first = records[0];
  requireProjection(first && first.length >= 4, 'invalid-input', 'No exec argv record');
  requireProjection(JSON.stringify(first.slice(0, 3)) === JSON.stringify(['exec', '--json', '--dangerously-bypass-approvals-and-sandbox']),
    'invalid-input', 'Codex exec flag prefix changed');
  const prompt = first[first.length - 1]!;
  boundedText(prompt);
  requireProjection(expectation && ['literal-template', 'inline-precedence', 'builtin-lead'].includes(expectation.kind),
    'unsupported-input', 'Unknown prompt expectation');
  if (expectation.kind === 'literal-template') {
    boundedText(expectation.agentName, 512);
    boundedText(expectation.fixtureText);
    requireProjection(expectation.fixtureText.length > 0 && expectation.agentName.length > 0 &&
      prompt.startsWith(expectation.fixtureText) && prompt.includes('{{ .AgentName }}') && !prompt.includes(expectation.agentName),
      'invalid-input', 'Literal template prompt changed');
  } else if (expectation.kind === 'inline-precedence') {
    boundedText(expectation.inlineText);
    requireProjection(expectation.inlineText.length > 0 && prompt.startsWith(expectation.inlineText) && !prompt.includes('builtin:pr-review'),
      'invalid-input', 'Inline prompt precedence changed');
  } else {
    requireProjection(prompt.startsWith('## INTERACTIVE MODE: Project Lead'), 'invalid-input', 'Empty prompt did not use built-in Lead');
  }
  requireProjection(prompt.split('\n\n### Multi-Agent Safety Rules').length - 1 === 1, 'invalid-input', 'Safety rules must occur once');
  if (expectation.kind !== 'builtin-lead') requireProjection(!prompt.includes('## INTERACTIVE MODE: Project Lead'),
    'invalid-input', 'Custom prompt contains built-in Lead content');
  requireProjection(records.every(record => JSON.stringify(record) === JSON.stringify(first)), 'invalid-input', 'Repeated exec argv differs');
  return { contract: promptArgvContract, execRecords: records.length, prompt };
}

// The only injectable seam accepts typed data, not commands, executable paths,
// environment/auth or callbacks from YAML. This is an internal test seam only.
export interface PromptArgvDouble { observe(): Promise<ArgvObservation> }
export async function probePromptArgvDouble(double: PromptArgvDouble, expectation: PromptExpectation) {
  return comparePromptArgv(expectation, await double.observe());
}
