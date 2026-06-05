// Minimal @daytona/sdk stub. The test installs the sandbox it wants via
// setSandbox(); Daytona.create() returns it. The sandbox's executeCommand is
// what the runner drives, so the test gives it one backed by real git.
let nextSandbox: unknown = null;

export function setSandbox(s: unknown): void {
  nextSandbox = s;
}

export class Daytona {
  constructor(_cfg?: unknown) {}
  async create(): Promise<unknown> {
    if (!nextSandbox) throw new Error('runner-test: call setSandbox() before run()');
    return nextSandbox;
  }
}

export type Sandbox = any;
