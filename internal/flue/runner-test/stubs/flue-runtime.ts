// Minimal @flue/runtime stub. The test supplies init/payload/env to run(), so
// createAgent's factory is never invoked here — these only need to exist so the
// runner module (and the Daytona connector) load.
export const createAgent = (factory: unknown) => ({ factory });
export const createSandboxSessionEnv = (..._args: unknown[]) => ({});

export type FlueContext = any;
export type SandboxApi = any;
export type SandboxFactory = any;
export type SessionEnv = any;
export type FileStat = any;
export type AgentRouteHandler = any;
