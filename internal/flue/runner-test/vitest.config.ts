import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const dir = path.dirname(fileURLToPath(import.meta.url));

// runner.ts imports heavy externals (@flue/runtime, @daytona/sdk) that aren't
// (and needn't be) installed here — alias them to tiny stubs so the module
// loads. The test drives run() with its own init/session + a sandbox whose
// executeCommand shells to REAL git, exercising the actual clone → agent →
// commit → push flow. More-specific aliases first.
export default defineConfig({
  resolve: {
    alias: [
      { find: '@flue/runtime/node', replacement: path.join(dir, 'stubs/flue-runtime-node.ts') },
      { find: '@flue/runtime', replacement: path.join(dir, 'stubs/flue-runtime.ts') },
      { find: '@daytona/sdk', replacement: path.join(dir, 'stubs/daytona-sdk.ts') },
    ],
  },
  test: {
    include: ['*.test.ts'],
    testTimeout: 30000,
  },
});
