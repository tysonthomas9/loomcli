import { readFile, lstat, readdir, realpath } from 'node:fs/promises';
import path from 'node:path';
import { z } from 'zod';
import { calculateImplementationPin, type ImplementationPin } from '@tysonthomas9/aft/capabilities';
import { defineOperation } from './operation.js';
import { containedPath } from './filesystem.js';
import { Digest, Id, requireFact, sha256 } from './protocol.js';
import { projectMarkdown, validateProjectionIdentity, type ProjectionIdentity } from './projections/index.js';

const dependencies = ['react', 'react-dom', 'react-markdown', 'remark-gfm', 'rehype-sanitize', 'esbuild', 'jsdom'] as const;
const sources = {
  chatMarkdownSha256: 'src/components/AgentChat/ChatMarkdown.tsx', longTextSha256: 'src/components/AgentChat/LongText.tsx',
  codeHighlightSha256: 'src/components/AgentChat/codeHighlight.ts', messageCopySha256: 'src/components/AgentChat/MessageCopyButton.tsx',
  cssSha256: 'src/components/AgentChat/ChatMarkdown.module.css', lockSha256: 'package-lock.json',
} as const;
const ProjectionIdentitySchema = z.object({ chatMarkdownSha256: Digest, longTextSha256: Digest, codeHighlightSha256: Digest,
  messageCopySha256: Digest, cssSha256: Digest, lockSha256: Digest,
  dependencies: z.object({ react: Id, 'react-dom': Id, 'react-markdown': Id, 'remark-gfm': Id, 'rehype-sanitize': Id, esbuild: Id, jsdom: Id }).strict(),
}).strict();
const MotionInput = z.object({ mode: z.literal('motion'), answer: z.string().min(1).max(8000),
  frames: z.array(z.object({ text: z.string().max(128000), streaming: z.boolean() }).strict()).max(20000),
  arrivals: z.array(z.string().max(128000)).max(5000),
}).strict();
export const MarkdownInputSchema = z.discriminatedUnion('mode', [MotionInput,
  z.object({ mode: z.literal('terminal-full'), answer: z.string().min(1).max(128000), frames: z.array(z.never()).max(0), arrivals: z.array(z.never()).max(0) }).strict(),
]);
const Count = z.number().int().nonnegative();
const Frame = z.object({ minSourceUtf16: Count, maxSourceUtf16: Count, visibleWords: Count, visible: z.string(), content: z.string(), contentWords: Count }).strict();
export const MarkdownOutputSchema = z.discriminatedUnion('mode', [
  z.object({ mode: z.literal('motion'), version: z.literal(1), terminal: z.string(), terminalVisible: z.string(), terminalContent: z.string(),
    frames: z.array(Frame), arrivals: z.array(z.object({ sourceUtf16: Count, visibleChanged: z.boolean(), contentChanged: z.boolean(),
      requiredMinSourceUtf16: Count, projectedUtf16: Count, projectedWords: Count }).strict()), sourceUtf16: Count, identity: ProjectionIdentitySchema }).strict(),
  z.object({ mode: z.literal('terminal-full'), version: z.literal(1), sourceUtf16: Count, terminal: z.string(), chatMarkdownSha256: Digest, cssSha256: Digest }).strict(),
]);
export interface MeasuredProjection {
  readonly identity: ProjectionIdentity;
  verify(): Promise<ProjectionIdentity>;
}
/** Called by owned code with source-built package roots, never with YAML paths.
 * Measures installed package manifests rather than copying lock/expected values. */
export async function measureProjection(frontendRoot: string, installedPackagesRoot: string): Promise<MeasuredProjection> {
  requireFact(await realpath(frontendRoot) === frontendRoot && await realpath(installedPackagesRoot) === installedPackagesRoot,
    'source-mismatch', 'Projection roots are not canonical');
  const identities = await Promise.all([lstat(frontendRoot), lstat(installedPackagesRoot)]);
  const measure = async () => {
    for (const [i, root] of [frontendRoot, installedPackagesRoot].entries()) {
      const stat = await lstat(root);
      requireFact(!stat.isSymbolicLink() && stat.ino === identities[i]!.ino && stat.dev === identities[i]!.dev,
        'source-mismatch', 'Projection root changed');
    }
    const measured: Record<string, unknown> = {};
    for (const [key, relative] of Object.entries(sources)) measured[key] = await sha256(await readFile(await containedPath(frontendRoot, relative)));
    const versions: Record<string, string> = {};
    for (const name of dependencies) {
      const manifest = JSON.parse(await readFile(await containedPath(installedPackagesRoot, `${name}/package.json`), 'utf8')) as { name: string; version: string };
      requireFact(manifest.name === name && typeof manifest.version === 'string', 'source-mismatch', 'Installed renderer package identity is missing');
      versions[name] = manifest.version;
    }
    measured.dependencies = versions;
    const parsed = ProjectionIdentitySchema.parse(measured);
    try { return validateProjectionIdentity(parsed); }
    catch { requireFact(false, 'source-mismatch', 'Projection source or installed dependency differs from the reviewed contract'); }
  };
  const identity = await measure();
  return { identity, verify: measure };
}
/** Pin the actual parser runtime closure, including transitive installed bytes. */
export async function pinProjectionImplementation(adapterRoot: string, mode: 'source' | 'emitted' = 'source'): Promise<ImplementationPin & { sha256: string }> {
  const files: string[] = [];
  for (const name of await readdir(adapterRoot)) if (name.endsWith('.ts') && !name.endsWith('.test.ts')) files.push(name);
  for (const name of await readdir(path.join(adapterRoot, 'projections'))) if (name.endsWith('.ts') && !name.endsWith('.test.ts')) files.push(`projections/${name}`);
  files.push('projections/package.json', 'projections/package-lock.json');
  const lock = JSON.parse(await readFile(path.join(adapterRoot, 'projections/package-lock.json'), 'utf8')) as {
    packages: Record<string, { dependencies?: Record<string, string> }>;
  };
  const manifest = JSON.parse(await readFile(path.join(adapterRoot, 'projections/package.json'), 'utf8')) as { dependencies: Record<string, string> };
  const packages = new Set<string>();
  const visitPackage = (key: string) => {
    if (packages.has(key)) return;
    requireFact(lock.packages[key], 'source-mismatch', 'Parser closure dependency is missing');
    packages.add(key);
    for (const name of Object.keys(lock.packages[key]!.dependencies ?? {})) {
      let parent = key; let candidate: string;
      for (;;) {
        candidate = parent + '/node_modules/' + name;
        if (lock.packages[candidate]) break;
        if (!parent.includes('/node_modules/')) { candidate = 'node_modules/' + name; break; }
        parent = parent.slice(0, parent.lastIndexOf('/node_modules/'));
      }
      visitPackage(candidate);
    }
  };
  for (const name of Object.keys(manifest.dependencies)) visitPackage(`node_modules/${name}`);
  const visitFiles = async (relative: string, includeDependencies = false): Promise<void> => {
    for (const entry of await readdir(path.join(adapterRoot, relative), { withFileTypes: true })) {
      if (entry.name === 'node_modules' && !includeDependencies) continue;
      requireFact(!entry.isSymbolicLink(), 'source-mismatch', 'Parser closure contains a symlink');
      const file = `${relative}/${entry.name}`;
      if (entry.isDirectory()) await visitFiles(file, includeDependencies);
      else if (entry.isFile()) files.push(file);
    }
  };
  for (const key of packages) await visitFiles('projections/' + key);
  if (mode === 'emitted') {
    await visitFiles('dist', true);
    return calculateImplementationPin(adapterRoot, files, 'dist/projection.js', 'createProjectionProvider');
  }
  return calculateImplementationPin(adapterRoot, files, 'projection.ts', 'createProjectionProvider');
}
export function createProjectionProvider(implementation: ImplementationPin & { sha256: string }, measured: MeasuredProjection) {
  return defineOperation({ id: 'loom.markdown.project', implementation, implementationSha256: implementation.sha256,
    inputSchema: MarkdownInputSchema, outputSchema: MarkdownOutputSchema, effects: ['read-filesystem'], retry: 'read-only-until-deadline', cleanup: 'none',
    evidenceClasses: ['deterministic'],
    async run(input, context) {
      context.signal.throwIfAborted();
      const identity = await measured.verify();
      const result = projectMarkdown(input.mode === 'motion' ? { answer: input.answer, frames: input.frames, arrivals: input.arrivals } : input, { identity });
      await measured.verify(); context.signal.throwIfAborted();
      if ('frames' in result) {
        requireFact(result.frames.every(frame => frame !== null), 'incomplete-pages', 'Markdown frame cannot be mapped to saved source');
        return { value: MarkdownOutputSchema.parse({ ...result, mode: 'motion' }), evidenceClass: 'deterministic' };
      }
      return { value: result, evidenceClass: 'deterministic' };
    },
  });
}
