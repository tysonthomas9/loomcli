import { readdir } from 'node:fs/promises';
import path from 'node:path';
import { calculateImplementationPin, type ImplementationPin, type CapabilityRegistry } from '@tysonthomas9/aft/capabilities';
import { createCoreProviders } from './index.js';
import { createFixtureProviders, type FixtureProviderOptions } from './fixture/providers.js';
import { fixtureRouting } from './fixture/routing.js';
import type { FixturePlan } from './fixture/lifecycle.js';
import { productionLegacyAccess } from './legacy/host-access.js';
import { createLegacyProviders, type LegacyAccessFactory } from './legacy/providers.js';
import type { LegacyProviderOptions } from './legacy/effects.js';
import { createProjectionProvider, pinProjectionImplementation } from './projection.js';
import { requireFact } from './protocol.js';
import type { GitReader } from './git.js';

export interface LoomCompositionOptions {
  implementation: ImplementationPin & { sha256: string };
  fixtures: Omit<FixtureProviderOptions, 'implementation' | 'implementationSha256'>;
  legacyAccess?: LegacyAccessFactory;
  gitReader?: GitReader;
}
/** A descriptor admits one task execution class. Native-only fixtures and
 * real fixtures without external task authority do not select a paid route. */
export function selectLegacyProviderOptions(plans: readonly FixturePlan[]): LegacyProviderOptions {
  const profiles = new Set<string>();
  const executions = new Set<LegacyProviderOptions['taskExecution']>();
  for (const plan of plans) {
    requireFact(!profiles.has(plan.profile), 'source-mismatch', 'Duplicate trusted fixture profile');
    profiles.add(plan.profile);
    const route = fixtureRouting(plan);
    if (!route.profile.startsWith('legacy-')) continue;
    if (route.evidenceClass === 'deterministic') executions.add('deterministic');
    else if (route.externalProvider) executions.add('live-provider');
  }
  requireFact(executions.size <= 1, 'source-mismatch', 'Mixed task routes require separate adapter compositions');
  return Object.freeze({ taskExecution: executions.values().next().value ?? 'deterministic' });
}
/** All transports are code-owned. Suite data selects closed operations only;
 * it cannot supply callbacks, executable paths or alternate schema modules. */
export function createLoomProviders(options: LoomCompositionOptions) {
  const pin = options.implementation;
  // Snapshot trusted inputs once: later caller edits cannot change the route
  // underneath descriptors already admitted by the canonical registry.
  const plans = structuredClone(options.fixtures.plans);
  const legacyOptions = selectLegacyProviderOptions(plans);
  return [
    ...createCoreProviders(pin, options.gitReader),
    ...createFixtureProviders({ ...options.fixtures, plans, implementation: pin, implementationSha256: pin.sha256 }),
    ...createLegacyProviders(pin, pin.sha256, options.legacyAccess ?? ((_context, fixture) => productionLegacyAccess(fixture)), legacyOptions),
    createProjectionProvider(pin),
  ];
}
export function registerLoomAdapter(registry: CapabilityRegistry, options: LoomCompositionOptions) {
  for (const provider of createLoomProviders(options)) registry.register(provider);
  return registry;
}
/** Byte-pin the shared module, leaf schemas/catalog, and actual parser closure.
 * Emitted mode additionally pins generated JS and copied runtime JSON/dependencies. */
export async function pinLoomImplementation(adapterRoot: string, mode: 'source' | 'emitted' = 'emitted') {
  const projection = await pinProjectionImplementation(adapterRoot, mode);
  const files = new Set(projection.files.map(file => file.path));
  const walk = async (relative: string) => {
    for (const entry of await readdir(path.join(adapterRoot, relative), { withFileTypes: true })) {
      if (entry.name === 'node_modules' || entry.name === 'dist' || entry.name.startsWith('.')) continue;
      requireFact(!entry.isSymbolicLink(), 'source-mismatch', 'Adapter source closure contains a symlink');
      const file = path.posix.join(relative, entry.name);
      if (entry.isDirectory()) {
        if (!relative && ['fixture','legacy','projections'].includes(entry.name)) await walk(file);
      }
      else if (entry.isFile() && !file.endsWith('.test.ts') && /\.(ts|json|mjs)$/.test(file)) files.add(file);
    }
  };
  await walk('');
  return calculateImplementationPin(adapterRoot, [...files].sort(), mode === 'emitted' ? 'dist/composition.js' : 'composition.ts', 'createLoomProviders');
}
