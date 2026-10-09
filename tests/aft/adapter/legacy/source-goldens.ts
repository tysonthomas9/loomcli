import type { Json } from '../protocol.js';

// Independently transcribed from the pinned YAML run/helper literals, not from
// scenarios.json or scenario(). IDs only select which source contract to test.
// Source hash integrity is checked separately against the pinned Git blobs.
const md = 'Files:\n\n| Name | Lines |\n| --- | ---: |\n| README.md | 1 |\n\n```go\nfunc main() {\n\treturn\n}\n```\n\n';
const questions = [
  { header: 'Color', question: 'AGV1-UI4 which color?', options: [{ label: 'Blue', description: 'calm' }, { label: 'Red', description: 'loud' }] },
  { header: 'Size', question: 'AGV1-UI4 which size?', options: [{ label: 'Small', description: 'fits' }, { label: 'Large', description: 'roomy' }] },
];

// agents-v1-{children,lead-parity,lead,lifecycle,resume,ui}.test.yaml.
export const literalPayloads: Record<string, Json> = {
  'fake-model-e31296792340e32b': { steps: [{ text: 'AGV1-KIDS-A2' }] },
  'fake-model-7469feee76d64297': { steps: [{ text: 'PAR-ONE-REPLY' }, { text: 'PAR-TWO-REPLY' }] },
  'fake-model-c847a6fd8bce451b': { steps: [{ text: 'PAR-REOPEN-ONE' }, { text: 'PAR-REOPEN-TWO' }] },
  'fake-model-d71b43a482d216cf': { steps: [{ text: 'PAR-SWITCH-ONE' }, { text: 'PAR-SWITCH-TWO' }] },
  'fake-model-9eb610f9e3804ed6': { steps: [{ bash: 'sleep 120' }, { text: 'PAR-STOP-AFTER' }] },
  'fake-model-058796d15129c2d7': { steps: [{ text: 'AGV1-REPLY-ONE' }] },
  'fake-model-ec40b5a4348e1862': { steps: [{ text: 'AGV1-REPLY-AFTER-RESTART' }] },
  'fake-model-a2d7dec06518da5e': { steps: [{ text: 'AGV1-MODAL-REPLY' }] },
  'fake-model-72337b7875831996': { steps: [{ bash: 'sleep 120' }] },
  'fake-model-12ae9c651f7e895f': { steps: [{ tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_list({})' } }] }, { text: 'AGV1-BRIDGE-REPLY' }] },
  'fake-model-ff1bc09f82ac358f': { steps: [{ text: 'AGV1-EFFORT-REPLY' }] },
  'fake-model-98e8f08e4ed44fda': { steps: [{ text: 'AGV1-UI2-REPLY' }] },
  'fake-model-514226effcc33a90': { steps: [{ text: 'AGV1-BEFORE-ARCHIVE' }, { text: 'AGV1-AFTER-UNARCHIVE' }] },
  'fake-model-54cdd6a283ca43aa': { steps: [{ text: 'AGV1-BEFORE-EXPIRY' }] },
  'fake-model-8473f01c0d4ae1a6': { steps: [{ text: 'AGV1-RESUME-BEFORE' }] },
  'fake-model-b47666158369ce17': { steps: [{ text: 'AGV1-RESUME-AFTER' }] },
  'fake-model-9acd22b7da2b40fc': { steps: [{ text: 'AGV1-RESUME-CONTINUED' }, { text: 'AGV1-RESUME-PENDING' }] },
  'fake-model-35af60643195fb77': { steps: [{ bash: 'sleep 20' }, { text: 'AGV1-SERVE-CONTINUED' }] },
  'fake-model-165c42410e463d4b': { steps: [{ text: 'AGV1-SERVE-PENDING' }] },
  'scripted-backend-3ff9a4ad939cd300': { tools: [
    { id: 'call_ui_ls', name: 'shell', input: { command: 'ls' }, output: 'README.md' },
    { id: 'call_ui_read', name: 'read', input: { filePath: 'missing-ui.txt' }, output: '', fail: 'File not found: missing-ui.txt' },
  ], text: 'AGV1-UI-TOOLS' },
  'scripted-backend-7d2016684b7bf3ee': { reasoning: 'AGV1-UI-THINKING weigh the options', text: 'AGV1-UI-THOUGHT' },
  'fake-model-reset': {}, 'fake-github-reset': {}, 'scripted-backend-reset': {},
  'fake-model-9e0075f2af10ebb0': { steps: [{ text: 'AGV1-UI-EFFORT-ONE' }] },
  'fake-model-57c6d1082a5d999e': { steps: [{ text: 'AGV1-UI-EFFORT-TWO' }] },
  'fake-model-1be8855da509058b': { steps: [{ text: md + 'AGV1-UI-MD' }] },
  'fake-model-c3524025903c6605': { steps: [{ text: md + 'AGV1-UI-COPY' }] },
  // agents-v1-lead.test.yaml:452-473, both backend branches.
  'scripted-backend-a27dc18bbec0bd65': [{ tools: [
    { id: 'call_ui3_read', name: 'read', input: { filePath: 'missing-ui3.txt' }, output: '', fail: 'File not found: missing-ui3.txt' },
    { id: 'call_ui3_ls', name: 'shell', input: { command: 'ls' }, output: 'README.md' },
  ], text: md + 'AGV1-UI3-REPLY' }, { reasoning: 'AGV1-UI3-THINKING weigh the files', text: 'AGV1-UI3-DONE' }],
  'fake-model-97dba493a1eba94d': { steps: [{ tool_calls: [
    { name: 'read', arguments: { filePath: 'missing-ui3.txt' } }, { name: 'shell', arguments: { command: 'ls' } },
  ] }, { text: md + 'AGV1-UI3-REPLY' }, { text: 'AGV1-UI3-DONE' }] },
  // agents-v1-lead.test.yaml:749-768, 828-847, 908-927, 1018-1032.
  'scripted-backend-a3e8df600adb9f2c': [{ tools: [{ id: 'call_ui4_bash', name: 'shell', input: { command: 'echo AGV1-UI4-RAN' },
    output: 'AGV1-UI4-RAN', permission: { action: 'bash', resources: ['echo AGV1-UI4-RAN'], save: ['echo *'] } }], text: 'AGV1-UI4-AFTER' },
  { fail: 'AGV1-UI4-FAIL the model refused the request' }],
  'fake-model-7ca7612599c8f225': { steps: [{ bash: 'echo AGV1-UI4-RAN' }, { text: 'AGV1-UI4-AFTER' }, { error: 'AGV1-UI4-FAIL the model refused the request' }] },
  'scripted-backend-3f4cf55682f7ac7f': [{ tools: [{ id: 'call_ui4_q', name: 'question', input: { questions }, output: '', questions }], text: 'AGV1-UI4-ANSWERED' }],
  'fake-model-b0b6a626d17cb752': { steps: [{ tool_calls: [{ name: 'question', arguments: { questions } }] }, { text: 'AGV1-UI4-ANSWERED' }] },
  'scripted-backend-6e3ad96ba0dd2ab9': [{ tools: [{ id: 'call_oc1_bash', name: 'shell', input: { command: 'echo AGV1-OC1-NEVER-RAN' },
    output: 'AGV1-OC1-NEVER-RAN', permission: { action: 'bash', resources: ['echo AGV1-OC1-NEVER-RAN'], save: ['echo *'] } }], text: 'AGV1-OC1-NEVER-SAID' },
  { text: 'AGV1-OC1-NEXT' }, { text: 'AGV1-OC1-SUSPENDED', suspend: true }],
  'fake-model-dba7d35600981eed': { steps: [{ bash: 'echo AGV1-OC1-NEVER-RAN' }, { text: 'AGV1-OC1-NEXT' }, { bash: 'sleep 120' }] },
  'scripted-backend-7763a8792b5ee738': [{ tools: [{ id: 'call_sa1_sub', name: 'subagent',
    input: { agent: 'general', description: 'AGV1-SA1 helper', prompt: 'say hi' }, output: 'AGV1-SA1-CHILD' }], text: 'AGV1-SA1-AFTER' }],
  'fake-model-098e1d5166438680': { steps: [{ tool_calls: [{ name: 'subagent', arguments: { agent: 'general', description: 'AGV1-SA1 helper', prompt: 'say hi' } }] }, { text: 'AGV1-SA1-AFTER' }] },
  'fake-model-24846d131947ec5d': { steps: [{ next: 'prompt', text: 'AGV1-CL2-ACK a is done; waiting for b.' }] },
  'fake-model-bb9dab0869ea6943': { steps: [{ next: 'prompt', text: 'AGV1-CL2-SUMMARY a and b are both done.' }] },
  'fake-model-f96e31cbcdad021b': { steps: [{ tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.github_read({"op": "repo_view"})' } }] }, { text: 'AGV1-GH-DONE' }, { text: 'AGV1-GH-AFTER' }] },
  'scripted-backend-36bb16b1d10e461d': [{ text: 'w1 w2 w3 w4 w5 w6 w7 w8 w9 w10 w11 w12 w13 w14 w15 AGV1-SERVE-CONTINUED', delay_ms: 1000 }],
  'fake-model-093d4a3e46c9c10f': { steps: [{ tool_calls: [{ name: 'shell', arguments: { command: 'ls' } },
    { name: 'read', arguments: { filePath: 'missing-ui.txt' } }] }, { text: 'AGV1-UI-TOOLS' }] },
  'fake-model-2eb7e07c69e5d413': { steps: [{ text: 'AGV1-UI-THOUGHT' }] },
};

// Complete parameterized payloads are literal expected values; in particular
// do not generate agent_create code with the production interpolation helper.
export const parameterizedGoldens: Record<string, { parameters: Record<string, string>; payload: Json }> = {
  // live-pr-review-suites/lp-pr-review.test.yaml:91-104.
  'fake-github-review-widget': { parameters: { headSha: '1234567890abcdef1234567890abcdef12345678', baseSha: 'fedcba0987654321fedcba0987654321fedcba09' },
    payload: { pr: { number: 7, state: 'open', title: 'Trim leading character', draft: false, merged_at: null,
      user: { login: 'octocat' }, updated_at: '2026-01-01T00:00:00Z',
      head: { sha: '1234567890abcdef1234567890abcdef12345678', ref: 'feature/auth' },
      base: { sha: 'fedcba0987654321fedcba0987654321fedcba09', ref: 'main' } },
    files: [{ filename: 'widget.go', status: 'modified', additions: 1, deletions: 1,
      patch: '@@ -1,5 +1,5 @@\n package widget\n \n func ParseName(s string) string {\n-\treturn s\n+\treturn s[1:]\n }' }] } },
  // children setup kids-script:22-28, callers at tests 0, 1 and 5.
  'fake-model-d414159ce7e8819b': { parameters: { agentName: 'agv1-kid-run' }, payload: { steps: [
    { tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-kid-run", "brief": "do the task"})' } }] },
    { text: 'AGV1-KIDS-ONE' }, { text: 'AGV1-KIDS-ONE' },
  ] } },
  'fake-model-f13aa2f970797b8c': { parameters: { agentName: 'agv1-kid2-run' }, payload: { steps: [
    { tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-kid2-run", "brief": "do the task"})' } }] },
    { text: 'AGV1-KIDS-A1' }, { text: 'AGV1-KIDS-A1' },
  ] } },
  'fake-model-a988ffd2f9e784d3': { parameters: { agentName: 'agv1-kid3-run' }, payload: { steps: [
    { tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-kid3-run", "brief": "do the task"})' } }] },
    { text: 'AGV1-KIDS-TABS' }, { text: 'AGV1-KIDS-TABS' },
  ] } },
  // children setup tray-script:36-42, callers at tests 2 and 4.
  'fake-model-2253814a6bbba6ba': { parameters: { agentName: 'agv1-tray-a-run' }, payload: { steps: [
    { next: 'prompt', tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-tray-a-run", "brief": "do the task"})' } }] },
    { text: 'AGV1-TRAY-STARTED-A', next: 'tool' }, { tool_calls: [{ name: 'df1_hold' }], next: 'prompt' },
  ] } },
  'fake-model-88ba74da857be641': { parameters: { agentName: 'agv1-tray-b-run' }, payload: { steps: [
    { next: 'prompt', tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-tray-b-run", "brief": "do the task"})' } }] },
    { tool_calls: [{ name: 'df1_hold' }], next: 'tool' }, { text: 'AGV1-TRAY-B-DONE', next: 'prompt' },
  ] } },
  'fake-model-7b3f8c4568d4814b': { parameters: { agentName: 'agv1-side-a-run' }, payload: { steps: [
    { next: 'prompt', tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-side-a-run", "brief": "do the task"})' } }] },
    { text: 'AGV1-SIDE-STARTED-A', next: 'tool' }, { tool_calls: [{ name: 'df1_hold' }], next: 'prompt' },
  ] } },
  'fake-model-06e54031289009ca': { parameters: { agentName: 'agv1-side-b-run' }, payload: { steps: [
    { next: 'prompt', tool_calls: [{ name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-side-b-run", "brief": "do the task"})' } }] },
    { text: 'AGV1-SIDE-STARTED-B', next: 'tool' }, { tool_calls: [{ name: 'df1_hold' }], next: 'prompt' },
  ] } },
  // children.test.yaml:328-339: two distinct execute calls and two holds.
  'fake-model-two-owned-children': { parameters: { agentName: 'agv1-cl2-a-run', secondAgentName: 'agv1-cl2-b-run' }, payload: { steps: [
    { next: 'prompt', tool_calls: [
      { name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-cl2-a-run", "brief": "do the task"})' } },
      { name: 'execute', arguments: { code: 'return await tools.loom.agent_create({"name": "agv1-cl2-b-run", "brief": "do the task"})' } },
    ] }, { next: 'tool', text: 'AGV1-CL2-STARTED' },
    { next: 'prompt', tool_calls: [{ name: 'df1_hold' }] }, { next: 'prompt', tool_calls: [{ name: 'df1_hold' }] },
  ] } },
};
