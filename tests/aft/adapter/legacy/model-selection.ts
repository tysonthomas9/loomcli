import type { FixtureModelSelection } from '../fixture/routing.js';
import { LegacyError } from './operations.js';

/** Configured selection follows product precedence; it is not an observation
 * of the model chosen by a provider. Cursor's frozen actor uses its default. */
export function checkConfiguredModel(selection: FixtureModelSelection, backend: string,
  env: Readonly<Record<string, string>>, roleModel: string | undefined, configuredModel?: string): void {
  const reject = (): never => { throw new LegacyError('source-mismatch', 'Configured model selection differs from owned routing'); };
  if (selection.kind === 'backend-default') {
    if (backend !== 'cursor' || env.LOOM_AGENT_MODEL || env.LOOM_OPENCODE_MODEL) reject();
    return;
  }
  switch (selection.selector) {
    case 'agent-env':
      // supervisor/spawn.go appends Role.Model after the parent environment.
      if (!['codex', 'claude'].includes(backend) || env.LOOM_AGENT_MODEL?.trim() !== selection.model ||
        roleModel && roleModel.trim() !== selection.model) reject();
      break;
    case 'opencode-env':
      // backend_opencode.go prefers this variable over LOOM_AGENT_MODEL.
      if (backend !== 'opencode' || env.LOOM_OPENCODE_MODEL?.trim() !== selection.model) reject();
      break;
    case 'opencode-config':
      // The other deterministic actors are source-attested stubs, not models.
      if (backend === 'opencode') {
        const argumentModel = env.LOOM_OPENCODE_MODEL?.trim() || env.LOOM_AGENT_MODEL?.trim();
        if (configuredModel !== selection.model || argumentModel && argumentModel !== selection.model) reject();
      }
      break;
    case 'native-model':
      throw new LegacyError('unsupported-capability', 'Native model selection has no legacy CLI actor');
  }
}
