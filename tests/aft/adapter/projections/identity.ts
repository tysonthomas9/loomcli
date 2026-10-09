import { requireProjection } from './text.js';

export const expectedProjectionIdentity = {
  chatMarkdownSha256: 'cbf6aad87f1f4f4e2160679732a886d2248f6b60b07aee9d7c48c5395600522f',
  longTextSha256: '8d13402bbd13e3647f58712d78f72adf5823f8116296c4eb747a3c26f421d91e',
  codeHighlightSha256: 'bd32aba5f11904e87b5fabc89275e7a0535d91bd307bce2310ee4899f19cb42b',
  messageCopySha256: '1a7a6d73bb0038d932380438369695d1affcaac44ef44860d7b73c4726b365a2',
  cssSha256: '8fc71740bb4e2e99b526105279f286544e1e27c4fb81b4eb1fc9c051573bda4b',
  lockSha256: 'baa61f945938c7c46e01ac840e08b6e3ca919994240c15cd2ab3505969ebbf2c',
  dependencies: {
    react: '18.3.1', 'react-dom': '18.3.1', 'react-markdown': '9.1.0', 'remark-gfm': '4.0.1',
    'rehype-sanitize': '6.0.0', esbuild: '0.21.5', jsdom: '27.4.0',
  },
} as const;
export interface ProjectionIdentity {
  chatMarkdownSha256: string; longTextSha256: string; codeHighlightSha256: string;
  messageCopySha256: string; cssSha256: string; lockSha256: string;
  dependencies: Record<keyof typeof expectedProjectionIdentity.dependencies, string>;
}
export interface ProjectionContext { identity: ProjectionIdentity }

// The code-owned context must measure source and installed versions. This
// comparison does not measure them, nor claim browser parity from a hash alone.
export function validateProjectionIdentity(identity: ProjectionIdentity): ProjectionIdentity {
  requireProjection(identity && typeof identity === 'object' && !Array.isArray(identity), 'invalid-input', 'Projection identity is missing');
  const keys = Object.keys(expectedProjectionIdentity) as (keyof ProjectionIdentity)[];
  requireProjection(Object.keys(identity).length === keys.length && keys.every(k => Object.hasOwn(identity, k)),
    'invalid-input', 'Projection identity fields changed');
  for (const key of keys.filter(k => k !== 'dependencies')) requireProjection(identity[key] === expectedProjectionIdentity[key],
    'unsupported-input', 'Unverified projection source identity');
  const dependencies = Object.keys(expectedProjectionIdentity.dependencies) as (keyof ProjectionIdentity['dependencies'])[];
  requireProjection(identity.dependencies && typeof identity.dependencies === 'object' &&
    Object.keys(identity.dependencies).length === dependencies.length &&
    dependencies.every(name => identity.dependencies[name] === expectedProjectionIdentity.dependencies[name]),
    'unsupported-input', 'Unverified installed renderer dependencies');
  return { ...identity, dependencies: { ...identity.dependencies } };
}
