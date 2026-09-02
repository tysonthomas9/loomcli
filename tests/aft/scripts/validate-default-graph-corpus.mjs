import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import { join, relative, resolve, sep } from 'node:path'

const [aftDirectory, testsDirectory] = process.argv.slice(2)
assert(aftDirectory, 'usage: validate-default-graph-corpus.mjs <aft-dir> <tests-dir>')
assert(testsDirectory, 'usage: validate-default-graph-corpus.mjs <aft-dir> <tests-dir>')

const aftDir = resolve(aftDirectory)
const testsDir = resolve(testsDirectory)
const runner = await import(pathToFileURL(join(aftDir, 'dist/runner.js')).href)
const graph = await import(pathToFileURL(join(aftDir, 'dist/graph.js')).href)

const compileEnv = {
  ...process.env,
  AFT_BASE_URL: process.env.AFT_BASE_URL ?? 'http://127.0.0.1:3100',
  AFT_API_URL: process.env.AFT_API_URL ?? 'http://127.0.0.1:8090',
  AFT_WS: process.env.AFT_WS ?? 'E2E-WS',
  AFT_TESTS_DIR: process.env.AFT_TESTS_DIR ?? testsDir,
  AFT_WORK_DIR: process.env.AFT_WORK_DIR ?? '/tmp/aft-default-graph-corpus',
  RUN_ID: process.env.RUN_ID ?? 'corpus-audit',
}
Object.assign(process.env, compileEnv)

const expectedGraphs = new Map([
  ['suites/issue-create-ui.graph/flow.graph.yaml', ['issue-create-ui', 4]],
  ['suites/issue-detail.graph/flow.graph.yaml', ['issue-detail', 14]],
  ['suites/workspaces.graph/flow.graph.yaml', ['workspaces', 3]],
  ['suites/zz-agent-flow.graph/flow.graph.yaml', ['agent-flow', 5]],
  ['suites/zz-custom-prompt.graph/flow.graph.yaml', ['custom-prompts', 22]],
  ['suites/zz-lead-agent.graph/flow.graph.yaml', ['lead-agents', 23]],
  ['suites/zz-planner-agent.graph/flow.graph.yaml', ['planner-agent', 11]],
  ['suites/zz-pr-review-agents.graph/flow.graph.yaml', ['pr-review-agents', 8]],
  ['suites/zz-task-runner.graph/flow.graph.yaml', ['task-runner', 11]],
  ['surface-suites/agent-lifecycle-contracts.graph/flow.graph.yaml', ['agent-lifecycle-contracts', 5]],
  ['surface-suites/review-actions.graph/flow.graph.yaml', ['review-actions-surface', 2]],
])

// These are the meaningful shared prefixes reviewers depend on. A count of one
// would make a supposed shared trunk cosmetic; a changed count means a branch was
// flattened, moved, or silently lost.
const expectedPrefixes = [
  ['issue-create-ui', ['open-board'], 4],
  ['issue-create-ui', ['open-board', 'open-rich-create-form'], 2],
  ['issue-detail', ['open-kanban', 'open-create-modal', 'create-open-issue'], 14],
  ['workspaces', ['open-primary-workspace-board'], 2],
  ['agent-flow', ['provision-nova'], 4],
  ['custom-prompts', ['reset-template-prompt-subject', 'create-template-prompt-subject'], 2],
  ['lead-agents', ['prepare-primary-lead'], 11],
  ['lead-agents', ['prepare-primary-lead', 'connect-primary-lead-runtime'], 4],
  ['lead-agents', ['prepare-second-lead'], 3],
  ['planner-agent', ['open-planner-dialog', 'choose-planner-template'], 10],
  ['planner-agent', ['open-planner-dialog', 'choose-planner-template', 'create-default-planner'], 7],
  ['pr-review-agents', ['open-prr-agent-list'], 6],
  ['pr-review-agents', ['open-prr-agent-list', 'create-primary-agent-prerequisite'], 3],
  ['pr-review-agents', ['open-prr-agent-list', 'open-create-agent-dialog'], 2],
  ['task-runner', ['open-task-runner-dialog', 'choose-task-runner-template'], 5],
  ['agent-lifecycle-contracts', ['open-task-runner-dialog', 'choose-task-runner-template'], 2],
  ['review-actions-surface', ['open-case-review-workspace'], 2],
]

// Single-consumer lifecycle chains do not belong in expectedPrefixes: their
// descendant count is necessarily one. Pin the complete path instead so a
// destructive multi-stage proof cannot be flattened into an unrelated leaf.
const expectedCompletePaths = [
  ['lead-agents', 'verified-16-deleting-a-lead-leaves-its-terminal-pty-running-until-ta', [
    'prepare-disposable-lead-runtime',
    'delete-disposable-lead',
    'reclaim-disposable-lead-terminal',
  ]],
]

const portable = (path) => relative(testsDir, path).split(sep).join('/')
const files = runner.collectSuiteFiles([
  join(testsDir, 'suites'),
  join(testsDir, 'surface-suites'),
])
const graphFiles = files.filter((file) => graph.classifyYamlFile(file) === 'graph')
const linearFiles = files.filter((file) => graph.classifyYamlFile(file) === 'linear-suite')

assert.equal(files.length, 33, 'default corpus entrypoint count changed')
assert.equal(graphFiles.length, expectedGraphs.size, 'default graph package count changed')
assert.equal(linearFiles.length, 22, 'default linear suite count changed')
assert.deepEqual(new Set(graphFiles.map(portable)), new Set(expectedGraphs.keys()), 'default graph package set changed')

const caseIds = new Set()
const caseKeys = new Set()
const testsByFlow = new Map()
let graphExecutions = 0

for (const file of graphFiles) {
  const relativeFile = portable(file)
  const [expectedFlow, expectedExecutions] = expectedGraphs.get(relativeFile)
  const compiled = graph.compileGraphManifest(file, compileEnv)
  const tests = compiled.suites.flatMap((suite) => {
    assert.equal(suite.tests.length, 1, `${relativeFile}: every graph suite must represent one independent execution`)
    return suite.tests
  })

  assert.equal(compiled.graph.flow, expectedFlow, `${relativeFile}: flow identity changed`)
  assert.equal(tests.length, expectedExecutions, `${relativeFile}: execution count changed`)
  assert.equal(new Set(tests.map((test) => test.name)).size, tests.length, `${relativeFile}: duplicate execution name`)
  testsByFlow.set(expectedFlow, tests)
  graphExecutions += tests.length

  for (const test of tests) {
    const evidence = test.graph
    assert(evidence, `${relativeFile} :: ${test.name}: missing compiled graph evidence`)
    assert.match(evidence.caseKey, /^[A-Z0-9]{1,4}-[A-F0-9]{8}$/, `${relativeFile} :: ${test.name}: unsafe case key`)
    assert(!caseIds.has(evidence.caseId), `${relativeFile} :: ${test.name}: duplicate case id ${evidence.caseId}`)
    assert(!caseKeys.has(evidence.caseKey), `${relativeFile} :: ${test.name}: duplicate case key ${evidence.caseKey}`)
    caseIds.add(evidence.caseId)
    caseKeys.add(evidence.caseKey)

    const saved = new Set()
    for (const [index, step] of test.steps.entries()) {
      const serialized = JSON.stringify(step)
      for (const match of serialized.matchAll(/\$\{var:([^}]+)}/g)) {
        assert(saved.has(match[1]), `${relativeFile} :: ${test.name}: step ${index + 1} reads ${match[1]} before it is saved`)
      }
      const saves = step.api?.save
      if (Array.isArray(saves)) {
        for (const save of saves) saved.add(save.as)
      }
    }
  }
}

for (const [flow, prefix, expectedCount] of expectedPrefixes) {
  const tests = testsByFlow.get(flow)
  assert(tests, `missing compiled flow ${flow}`)
  const actualCount = tests.filter((test) => prefix.every((transition, index) => test.graph.transitions[index] === transition)).length
  assert.equal(actualCount, expectedCount, `${flow}: shared prefix ${prefix.join(' -> ')} changed descendant count`)
}

for (const [flow, terminalState, expectedPath] of expectedCompletePaths) {
  const tests = testsByFlow.get(flow)
  assert(tests, `missing compiled flow ${flow}`)
  const matches = tests.filter((test) => test.graph.states.at(-1) === terminalState)
  assert.equal(matches.length, 1, `${flow}: terminal state ${terminalState} must identify exactly one execution`)
  assert.deepEqual(matches[0].graph.transitions, expectedPath, `${flow}: complete path to ${terminalState} changed`)
}

const customPromptTests = testsByFlow.get('custom-prompts')
assert(customPromptTests, 'missing compiled custom-prompts flow')
const customPromptSteps = customPromptTests.flatMap((test) => test.steps)
const promptCaptures = customPromptSteps.filter((step) => step.intent === 'Capture only the mounted prompt fingerprint and UTF-8 byte count before submission')
const promptAssertions = customPromptSteps.filter((step) => step.intent === 'Confirm Claude received the same authored prompt captured from the mounted form and reports its run-scoped session UUID without exposing prompt text')
const unsafeRoleDiagnostics = customPromptSteps.filter((step) => typeof step.run === 'string' && /assert[^\n]*,\s*(?:role|r)\s*$/m.test(step.run))
const overstatedSessionClaims = customPromptSteps.filter((step) => step.intent?.includes('real session identity'))
assert.equal(promptCaptures.length, 6, 'custom prompt runtime paths must capture six mounted prompt fingerprints before submission')
assert.equal(promptAssertions.length, 6, 'custom prompt runtime paths must compare six safe prompt fingerprints at the terminal')
assert.equal(unsafeRoleDiagnostics.length, 0, 'custom prompt failure diagnostics must never serialize a complete role object containing prompt text')
assert.equal(overstatedSessionClaims.length, 0, 'controlled Claude evidence must describe a run-scoped UUID, not a real external session')
for (const step of promptCaptures) {
  const source = step.wait?.fn ?? ''
  assert(source.includes("String(field.value)"), 'custom prompt fingerprint capture must read the mounted form value')
  assert(source.includes('sessionStorage.setItem'), 'custom prompt fingerprint capture must persist only its derived contract')
  assert(!source.includes('String.raw') && !source.includes('expectedPrompt'), 'custom prompt fingerprint capture must not interpolate expected prompt bytes')
}
for (const step of promptAssertions) {
  const source = step.wait?.fn ?? ''
  assert(source.includes('sessionStorage.getItem'), 'custom prompt terminal assertion must read the pre-submit fingerprint')
  assert(!source.includes('String.raw') && !source.includes('expectedPrompt'), 'custom prompt terminal assertion must not interpolate expected prompt bytes')
}

const exactMonitorRows = [
  ['agent-flow', 'Wait until nova appears as an exact row inside the monitor activity panel', 'Agent: nova'],
  ['planner-agent', 'Wait until the exact Planner row appears inside the monitor activity panel', `Agent: planner-${compileEnv.RUN_ID}`],
  ['task-runner', 'Wait until the exact Task Runner row appears inside the monitor activity panel', `Agent: worker-a-${compileEnv.RUN_ID}`],
]
for (const [flow, intent, ariaLabel] of exactMonitorRows) {
  const steps = testsByFlow.get(flow).flatMap((test) => test.steps).filter((step) => step.intent === intent)
  assert.equal(steps.length, 1, `${flow}: expected one exact monitor-row assertion`)
  const source = steps[0].wait?.fn ?? ''
  assert(source.includes("querySelector('[data-testid=agent-activity-panel]')"), `${flow}: monitor assertion must bind to the activity panel`)
  assert(source.includes(`[aria-label="${ariaLabel}"]`), `${flow}: monitor assertion must bind to the exact agent row`)
  assert(!source.includes('document.body'), `${flow}: persistent rail content must not satisfy the monitor assertion`)
}

let linearExecutions = 0
for (const file of linearFiles) linearExecutions += runner.loadSuite(file).tests.length

assert.equal(graphExecutions, 108, 'default graph execution count changed')
assert.equal(caseIds.size, graphExecutions, 'graph case IDs must be unique')
assert.equal(caseKeys.size, graphExecutions, 'graph case keys must be unique')
assert.equal(linearExecutions, 44, 'default linear execution count changed')
assert.equal(graphExecutions + linearExecutions, 152, 'default deterministic corpus count changed')

console.log(`AFT graph corpus: PASS (${graphFiles.length} graphs, ${graphExecutions} graph executions, ${linearExecutions} linear executions)`)
