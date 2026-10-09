import { z } from 'zod';
import { readFileSync } from 'node:fs';
import { Json } from '../protocol.js';

const ScenarioIds = [
  "fake-model-e31296792340e32b",
  "fake-model-7469feee76d64297",
  "fake-model-c847a6fd8bce451b",
  "fake-model-d71b43a482d216cf",
  "fake-model-9eb610f9e3804ed6",
  "fake-model-058796d15129c2d7",
  "fake-model-ec40b5a4348e1862",
  "fake-model-a2d7dec06518da5e",
  "fake-model-72337b7875831996",
  "fake-model-12ae9c651f7e895f",
  "fake-model-ff1bc09f82ac358f",
  "fake-model-98e8f08e4ed44fda",
  "fake-model-514226effcc33a90",
  "fake-model-54cdd6a283ca43aa",
  "fake-model-8473f01c0d4ae1a6",
  "fake-model-b47666158369ce17",
  "fake-model-9acd22b7da2b40fc",
  "fake-model-35af60643195fb77",
  "fake-model-165c42410e463d4b",
  "scripted-backend-3ff9a4ad939cd300",
  "scripted-backend-7d2016684b7bf3ee",
  "fake-model-reset",
  "fake-github-reset",
  "scripted-backend-reset",
  "fake-model-9e0075f2af10ebb0",
  "fake-model-57c6d1082a5d999e",
  "fake-model-1be8855da509058b",
  "fake-model-c3524025903c6605",
  "fake-github-review-widget",
  "scripted-backend-a27dc18bbec0bd65",
  "fake-model-97dba493a1eba94d",
  "scripted-backend-a3e8df600adb9f2c",
  "fake-model-7ca7612599c8f225",
  "scripted-backend-3f4cf55682f7ac7f",
  "fake-model-b0b6a626d17cb752",
  "scripted-backend-6e3ad96ba0dd2ab9",
  "fake-model-dba7d35600981eed",
  "scripted-backend-7763a8792b5ee738",
  "fake-model-098e1d5166438680",
  "fake-model-24846d131947ec5d",
  "fake-model-bb9dab0869ea6943",
  "fake-model-f96e31cbcdad021b",
  "scripted-backend-36bb16b1d10e461d",
  "fake-model-093d4a3e46c9c10f",
  "fake-model-2eb7e07c69e5d413",
  "fake-model-d414159ce7e8819b",
  "fake-model-f13aa2f970797b8c",
  "fake-model-2253814a6bbba6ba",
  "fake-model-88ba74da857be641",
  "fake-model-7b3f8c4568d4814b",
  "fake-model-06e54031289009ca",
  "fake-model-a988ffd2f9e784d3",
  "fake-model-two-owned-children"
] as const;
const Entry = z.object({ id: z.enum(ScenarioIds), fixtureId: z.enum(['fake-model', 'fake-github', 'scripted-backend']),
  payload: Json, sources: z.array(z.string()).min(1), reset: z.boolean().optional(),
  parameters: z.array(z.enum(['headSha', 'baseSha', 'agentName', 'secondAgentName'])).optional(),
  agentPrefixes: z.object({ agentName: z.string(), secondAgentName: z.string().optional() }).strict().optional() }).strict();
export const scenarioCatalog = z.object({ loomBase: z.string(), inventoryIndexSha256: z.string(), evidenceClass: z.string(),
  entries: z.array(Entry) }).strict().parse(JSON.parse(readFileSync(new URL('./scenarios.json', import.meta.url), 'utf8')));
const data = scenarioCatalog;

// Closed fixture protocol data, never test journeys or expected outcomes. The
// suite cannot supply tool code, shell text, arbitrary payloads or module paths.
export const ScenarioId = z.enum(ScenarioIds);
export const FixtureId = z.enum(['fake-model', 'fake-github', 'scripted-backend']);
export const FixtureParameters = z.object({
  headSha: z.string().regex(/^[a-f0-9]{40}$/).optional(),
  baseSha: z.string().regex(/^[a-f0-9]{40}$/).optional(),
  agentName: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/).optional(),
  secondAgentName: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/).optional(),
}).strict();
export function scenario(fixtureId: z.infer<typeof FixtureId>, scenarioId: string,
  parameters: z.infer<typeof FixtureParameters>, runId: string) {
  const entry = data.entries.find(item => item.id === scenarioId && item.fixtureId === fixtureId);
  if (!entry) throw new Error('Unknown fixture protocol stimulus');
  const required = 'parameters' in entry ? entry.parameters ?? [] : [];
  if (Object.keys(parameters).length !== required.length || required.some(key => !(key in parameters))) {
    throw new Error('Fixture protocol parameters do not match');
  }
  for (const [key, prefix] of Object.entries(entry.agentPrefixes ?? {})) {
    if (parameters[key as 'agentName' | 'secondAgentName'] !== prefix + runId) throw new Error('Foreign child fixture name');
  }
  const createCode = (name: string | undefined) => `return await tools.loom.agent_create({"name": ${JSON.stringify(name)}, "brief": "do the task"})`;
  const replace = (value: unknown): unknown => {
    if (value === '$agentCreate') return createCode(parameters.agentName);
    if (value === '$secondAgentCreate') return createCode(parameters.secondAgentName);
    if (value === '$headSha') return parameters.headSha;
    if (value === '$baseSha') return parameters.baseSha;
    if (Array.isArray(value)) return value.map(replace);
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, replace(v)]));
    return value;
  };
  return { reset: 'reset' in entry && entry.reset === true, payload: replace(entry.payload), sources: entry.sources };
}
