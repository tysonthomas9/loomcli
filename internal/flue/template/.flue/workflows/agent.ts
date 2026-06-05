/**
 * agent — the default workflow loom invokes via `flue run`.
 *
 * loom dispatches one-shot agents (`loom plan` / `loom task`) by running:
 *
 *   flue run agent --target node --root <projectDir> \
 *     --payload '{"prompt": "...", "cwd": "<worktree>", "model": "<model>"}'
 *
 * The agent runs against the real worktree via the `local()` sandbox, so it
 * has direct host filesystem + shell access and auto-discovers AGENTS.md /
 * CLAUDE.md and .agents/skills/ from the worktree. loom inspects the
 * resulting git state, so this workflow returns nothing.
 *
 * NOTE: unlike the lead agent, the one-shot path intentionally does NOT use a
 * remote Daytona sandbox — loom integrates the agent's changes from the local
 * worktree's git state, and a remote container's filesystem would be invisible
 * to it. A Daytona-backed coding agent would need the clone-into-sandbox +
 * push-back pattern instead.
 *
 * The workflow name is intentionally short: when the agent uses the ChatGPT
 * Codex provider (see app.ts), the per-session affinity key derived from the
 * workflow name must stay within that backend's 64-char prompt_cache_key cap.
 */
import { createAgent, type FlueContext } from '@flue/runtime';
import { local } from '@flue/runtime/node';

interface AgentPayload {
	/** The full rendered prompt for this agent turn. */
	prompt: string;
	/** Absolute path to the git worktree the agent operates on. */
	cwd: string;
	/** Provider/model id, e.g. "anthropic/claude-sonnet-4-6" or "openai-codex/gpt-5.5". */
	model?: string;
}

export async function run({ init, payload }: FlueContext) {
	const p = (payload ?? {}) as AgentPayload;
	if (!p.prompt) throw new Error('agent: payload.prompt is required');
	if (!p.cwd) throw new Error('agent: payload.cwd is required');

	const agent = createAgent(() => ({
		sandbox: local({ cwd: p.cwd }),
		model: p.model ?? 'anthropic/claude-sonnet-4-6',
	}));

	const harness = await init(agent);
	const session = await harness.session();
	await session.prompt(p.prompt);
}
