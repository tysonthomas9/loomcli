/**
 * runner — loom's Flue runner (per docs/design/flue-daytona-runtime-proposal.md).
 *
 * loom drives this with `flue run runner --payload '<RunnerInput>'`. It runs a
 * one-shot agent in either the local worktree or a fresh remote Daytona sandbox
 * (Daytona-per-task), then — for the Daytona path — captures the remote git
 * diff as a patch on the host so loom can sync it back into the local worktree
 * and finalize through its existing worktree/git path.
 *
 * Structured events are emitted to stderr, one JSON object per line prefixed
 * with "LOOMRUNNER ", so the Go backend can map them to logs / usage / patch /
 * sandbox metadata without parsing flue's own output:
 *
 *   LOOMRUNNER {"type":"runner_started", ...}
 *   LOOMRUNNER {"type":"sandbox_created","provider":"daytona","sandbox_id":"...","cwd":"..."}
 *   LOOMRUNNER {"type":"repo_hydrated","remote":"...","ref":"...","commit":"..."}
 *   LOOMRUNNER {"type":"patch_ready","path":"/tmp/...","files_changed":3}
 *   LOOMRUNNER {"type":"usage","input_tokens":..,"output_tokens":..}
 *   LOOMRUNNER {"type":"final","status":"completed|failed","sandbox_id":"...","exit_code":0}
 */
import { writeFile } from 'node:fs/promises';
import { createAgent, type FlueContext } from '@flue/runtime';
import { local } from '@flue/runtime/node';
import { Daytona } from '@daytona/sdk';
import { daytona } from '../connectors/daytona';

interface RunnerInput {
	/** "local" runs in the host worktree; "daytona-task" in a fresh remote sandbox. */
	sandbox: 'local' | 'daytona-task';
	prompt: string;
	model?: string;
	/** local mode: agent cwd on the host. */
	local_worktree_path?: string;
	/** daytona-task: clone source + checkout target. */
	repo_remote_url?: string;
	repo_branch?: string;
	base_ref?: string;
	/** daytona-task: working dir inside the sandbox (default /workspace/project). */
	sandbox_cwd?: string;
	/** daytona-task: host path the captured patch is written to. */
	patch_out?: string;
	task_id?: string;
	/** How loom syncs the result back: "patch-back" (default) | "branch-push" | "none". */
	sync_strategy?: string;
}

function emit(event: Record<string, unknown>): void {
	process.stderr.write(`LOOMRUNNER ${JSON.stringify(event)}\n`);
}

/** Count the files touched by a unified diff (number of "diff --git" headers). */
function countDiffFiles(patch: string): number {
	const m = patch.match(/^diff --git /gm);
	return m ? m.length : 0;
}

export async function run({ init, payload, env }: FlueContext) {
	const p = (payload ?? {}) as RunnerInput;
	if (!p.prompt) throw new Error('runner: payload.prompt is required');
	const model = p.model ?? 'anthropic/claude-sonnet-4-6';
	emit({ type: 'runner_started', sandbox: p.sandbox, task_id: p.task_id });

	if (p.sandbox === 'daytona-task') {
		const key = env.DAYTONA_API_KEY?.trim();
		if (!key) throw new Error('runner: DAYTONA_API_KEY is required for sandbox=daytona-task');

		const client = new Daytona({ apiKey: key });
		const sandbox = await client.create();
		// Resolve a writable working dir under the sandbox user's home (the
		// default image's /workspace is not writable by the `daytona` user).
		const home = (await sh(sandbox, 'echo "$HOME"')).trim() || '/home/daytona';
		const cwd = p.sandbox_cwd ?? `${home}/project`;
		emit({ type: 'sandbox_created', provider: 'daytona', sandbox_id: sandbox.id, cwd });

		try {
			// 1. Hydrate the repo into the sandbox at `cwd`, checked out to base_ref.
			if (p.repo_remote_url) {
				const ref = p.base_ref || p.repo_branch || '';
				await sh(sandbox, `git clone --no-single-branch ${shq(p.repo_remote_url)} ${shq(cwd)}`);
				if (ref) await sh(sandbox, `cd ${shq(cwd)} && git checkout ${shq(ref)}`);
				const commit = (await sh(sandbox, `cd ${shq(cwd)} && git rev-parse HEAD`)).trim();
				emit({ type: 'repo_hydrated', remote: p.repo_remote_url, ref, commit });
			} else {
				await sh(sandbox, `mkdir -p ${shq(cwd)} && cd ${shq(cwd)} && git init -q`);
				emit({ type: 'repo_hydrated', remote: '', ref: '', commit: '' });
			}

			// 2. Run the agent in the sandbox.
			const agent = createAgent(() => ({ sandbox: daytona(sandbox), cwd, model }));
			const harness = await init(agent);
			const session = await harness.session();
			const resp = await session.prompt(p.prompt);
			if (resp.usage) {
				emit({ type: 'usage', input_tokens: resp.usage.input, output_tokens: resp.usage.output });
			}

			// 3. Capture ALL changes since base_ref — committed (the task prompt
			//    tells the agent to `git commit`), staged, unstaged, untracked, and
			//    deletes — so nothing the agent did in the sandbox is lost. Diffing
			//    against working-tree-vs-index would miss anything it committed.
			await sh(sandbox, `cd ${shq(cwd)} && git add -A`);
			const diffCmd = p.base_ref
				? `git diff --binary --full-index --no-ext-diff ${shq(p.base_ref)}`
				: `git diff --binary --full-index --no-ext-diff --cached`;
			const patch = await sh(sandbox, `cd ${shq(cwd)} && ${diffCmd} || true`);
			const filesChanged = countDiffFiles(patch);
			if (p.patch_out) {
				await writeFile(p.patch_out, patch);
				emit({ type: 'patch_ready', path: p.patch_out, files_changed: filesChanged });
			}

			// 4. Delete the sandbox on success (proposal: successful sandboxes are
			//    deleted by default). Best-effort — the work is already captured, so
			//    a delete failure must not fail the task; report it as retained.
			let cleanup = 'retained';
			try {
				await sandbox.delete();
				cleanup = 'deleted';
				emit({ type: 'sandbox_deleted', sandbox_id: sandbox.id });
			} catch (delErr) {
				emit({ type: 'sandbox_delete_failed', sandbox_id: sandbox.id, error: delErr instanceof Error ? delErr.message : String(delErr) });
			}

			emit({ type: 'final', status: 'completed', sandbox_id: sandbox.id, files_changed: filesChanged, cleanup, exit_code: 0 });
			return { status: 'completed', sandbox_id: sandbox.id, files_changed: filesChanged, cleanup };
		} catch (err) {
			const message = err instanceof Error ? err.message : String(err);
			// Retain the sandbox on failure (proposal: failed sandboxes are kept for debugging).
			emit({ type: 'final', status: 'failed', sandbox_id: sandbox.id, error: message, cleanup: 'retained', exit_code: 1 });
			throw err;
		}
	}

	// local mode: run on the host worktree; loom finalizes from local git state.
	const cwd = p.local_worktree_path ?? process.cwd();
	const agent = createAgent(() => ({ sandbox: local({ cwd }), model }));
	const harness = await init(agent);
	const session = await harness.session();
	const resp = await session.prompt(p.prompt);
	if (resp.usage) {
		emit({ type: 'usage', input_tokens: resp.usage.input, output_tokens: resp.usage.output });
	}
	emit({ type: 'final', status: 'completed', exit_code: 0 });
	return { status: 'completed' };
}

// ── helpers ──────────────────────────────────────────────────────────────────

function shq(value: string): string {
	return `'${value.replace(/'/g, "'\\''")}'`;
}

/** Run a shell command in the Daytona sandbox, returning stdout (throws non-zero). */
async function sh(
	sandbox: { process: { executeCommand: (c: string) => Promise<{ result?: string; exitCode?: number }> } },
	command: string,
): Promise<string> {
	const res = await sandbox.process.executeCommand(`sh -lc ${shq(command)}`);
	if ((res.exitCode ?? 0) !== 0) {
		throw new Error(`runner: command failed (exit ${res.exitCode}): ${command}\n${res.result ?? ''}`);
	}
	return res.result ?? '';
}
