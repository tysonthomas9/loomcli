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
import { TaskRunClient, type Task } from '../vendor/loom-sdk/index.js';

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
	/**
	 * PRD Phase B (docs/product/loom-typescript-sdk-spec.md). When true, the
	 * runner fetches the task's title/description/design/AC from loom serve via
	 * `@loom/sdk` (getTask) instead of relying on loom inlining them into
	 * `prompt`. The scoped bootstrap (server URL, workspace, task id, …) arrives
	 * via LOOM_* env vars. loom sets this only when it has a reachable server;
	 * otherwise `prompt` carries the full (inlined) task and this stays false.
	 */
	fetch_task?: boolean;
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
	// With fetch_task the task body comes from the SDK, so `prompt` may be just
	// the sandbox preamble (or empty); only require a prompt otherwise.
	if (!p.prompt && !p.fetch_task) throw new Error('runner: payload.prompt is required');
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
				// Clone via a GITHUB_TOKEN-authenticated URL so a private remote is
				// reachable from the credential-less sandbox; the repo_hydrated event
				// below keeps the clean (token-free) URL.
				const cloneToken = env.GITHUB_TOKEN ?? process.env.GITHUB_TOKEN;
				await sh(sandbox, `git ${gitAuthArgs(p.repo_remote_url, cloneToken)}clone --no-single-branch ${shq(p.repo_remote_url)} ${shq(cwd)}`);
				let effectiveRef = ref;
				if (ref) {
					// base_ref is the local worktree HEAD; it may carry local-only
					// commits not pushed to the remote (e.g. a prior task whose push
					// failed). Those aren't in this clone, so checkout would fail —
					// fall back to the cloned default-branch HEAD instead of aborting.
					const co = await shTry(sandbox, `cd ${shq(cwd)} && git checkout ${shq(ref)}`);
					if (!co.ok) {
						effectiveRef = '';
						emit({ type: 'hydrate_warning', base_ref: ref, message: 'base_ref not present on remote; working from default-branch HEAD', detail: co.output.slice(0, 200) });
					}
				}
				const commit = (await sh(sandbox, `cd ${shq(cwd)} && git rev-parse HEAD`)).trim();
				emit({ type: 'repo_hydrated', remote: p.repo_remote_url, ref: effectiveRef, commit });
			} else {
				await sh(sandbox, `mkdir -p ${shq(cwd)} && cd ${shq(cwd)} && git init -q`);
				emit({ type: 'repo_hydrated', remote: '', ref: '', commit: '' });
			}

			// 2. Resolve the prompt (SDK read path or inlined fallback), then run
			//    the agent in the sandbox. `loom` is the control-plane client when
			//    the bootstrap is present (null otherwise → LOOMRUNNER-only path).
			const loom = taskRunClient(p);
			const prompt = await resolvePrompt(p, loom);
			const agent = createAgent(() => ({ sandbox: daytona(sandbox), cwd, model }));
			const harness = await init(agent);
			const session = await harness.session();
			const resp = await session.prompt(prompt);
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

			// 3a. Branch-push (PRD Phase D): commit the agent's work and push it as a
			//     branch to the remote, then register a "commit" Artifact — so the
			//     result is server-visible without host patch-back. Gated on
			//     sync_strategy; the patch above stays an opt-in local convenience.
			if (p.sync_strategy === 'branch-push' && p.repo_remote_url && filesChanged > 0) {
				await pushResultBranch(sandbox, cwd, p, env, filesChanged, loom);
			}

			// 3b. Report results to loom serve via @loom/sdk (PRD Phase C) when the
			//     bootstrap is present. Best-effort: the work is already captured as a
			//     patch + LOOMRUNNER events, so a reporting failure must not fail the
			//     task.
			if (loom) {
				await reportToLoom(loom, resp.usage);
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

function errMessage(err: unknown): string {
	return err instanceof Error ? err.message : String(err);
}

/**
 * Build the loom control-plane client when the bootstrap is present (fetch_task).
 * Throws fast if fetch_task is set but the bootstrap env is incomplete, rather
 * than silently degrading to an empty task.
 */
function taskRunClient(p: RunnerInput): TaskRunClient | null {
	if (!p.fetch_task) return null;
	try {
		return TaskRunClient.fromEnv();
	} catch (err) {
		throw new Error(`runner: fetch_task set but the loom bootstrap env is incomplete: ${errMessage(err)}`);
	}
}

/**
 * Resolve the agent prompt. With a loom client, the runner pulls the task's
 * title/description/design/AC from loom serve via `@loom/sdk` (PRD Phase B) and
 * appends it to the sandbox preamble loom sent in `prompt` — replacing loom's
 * Go-side design-inlining. The runner process runs on the host, so it reaches
 * loom serve directly (no extra hosting). When the fetch fails we throw rather
 * than silently run an empty task. Without a client, `prompt` already carries
 * the (inlined) task.
 */
async function resolvePrompt(p: RunnerInput, loom: TaskRunClient | null): Promise<string> {
	if (!loom) return p.prompt;

	let task: Task;
	try {
		task = await loom.getTask();
	} catch (err) {
		throw new Error(
			`runner: could not fetch task ${loom.bootstrap.taskId} from ${loom.bootstrap.serverUrl} via @loom/sdk: ${errMessage(err)}`,
		);
	}

	emit({
		type: 'task_fetched',
		task_id: task.id,
		title: task.title,
		has_design: Boolean(task.design),
		source: loom.bootstrap.serverUrl,
	});

	const body = renderTaskBody(task);
	const preamble = p.prompt?.trim();
	return preamble ? `${preamble}\n\n${body}` : body;
}

/**
 * Report run results to loom serve via @loom/sdk (PRD Phase C). Best-effort:
 * each call is independently guarded so a server-side write failure surfaces a
 * warning event but never fails the task (the work is captured as a patch).
 */
async function reportToLoom(
	loom: TaskRunClient,
	usage: { input: number; output: number } | undefined,
): Promise<void> {
	// We deliberately do NOT register a "patch" artifact here: the patch lives at
	// a host-local temp path that isn't resolvable from the server, so it's not a
	// server-visible source of truth. The branch-push path (pushResultBranch)
	// registers a proper "commit" artifact with a resolvable ref; patch-back is a
	// host-local convenience with no server artifact.
	if (usage) {
		try {
			await loom.recordUsage({ inputTokens: usage.input, outputTokens: usage.output });
		} catch (err) {
			emit({ type: 'report_warning', op: 'recordUsage', error: errMessage(err) });
		}
	}
}

/**
 * Format a server-fetched task into the sandbox agent's prompt body. Mirrors
 * loom's Go-side buildSandboxPrompt body so the SDK read path and the inlined
 * fallback produce equivalent instructions.
 */
/**
 * Push the agent's work as a branch to the remote and register a "commit"
 * Artifact via the SDK (PRD Phase D). Commits with a loom identity; pushes via a
 * GITHUB_TOKEN-authenticated URL when available so a fresh sandbox (no host
 * creds) can write to a private remote.
 */
async function pushResultBranch(
	sandbox: { id: string; process: { executeCommand: (c: string) => Promise<{ result?: string; exitCode?: number }> } },
	cwd: string,
	p: RunnerInput,
	env: Record<string, string | undefined>,
	filesChanged: number,
	loom: TaskRunClient | null,
): Promise<void> {
	const remote = p.repo_remote_url ?? '';
	const safeTask = (p.task_id || 'run').replace(/[^A-Za-z0-9._-]/g, '-');
	const branch = `loom/${safeTask}-${sandbox.id.slice(0, 8)}`;
	// step 3 already staged everything (`git add -A`); commit it with a loom
	// identity. Do NOT swallow a commit failure: if it fails we must not push a
	// stale HEAD and register a "commit" artifact with none of the agent's work —
	// let it throw so the run is reported failed.
	await sh(sandbox, `cd ${shq(cwd)} && git -c user.name=loom -c user.email=loom@localhost commit -q -m ${shq('loom: ' + safeTask)}`);
	const sha = (await sh(sandbox, `cd ${shq(cwd)} && git rev-parse HEAD`)).trim();
	const token = env.GITHUB_TOKEN ?? process.env.GITHUB_TOKEN;
	await sh(sandbox, `cd ${shq(cwd)} && git ${gitAuthArgs(remote, token)}push ${shq(remote)} HEAD:refs/heads/${branch}`);
	const ref = `${remote}#${branch}@${sha}`;
	emit({ type: 'branch_pushed', branch, commit: sha, remote });
	if (loom) {
		try {
			await loom.postArtifact({ type: 'commit', uri: ref, summary: `branch ${branch}`, filesChanged, idempotencyKey: sha });
			emit({ type: 'artifact_reported', artifact_type: 'commit', files_changed: filesChanged });
		} catch (err) {
			emit({ type: 'report_warning', op: 'postArtifact(commit)', error: errMessage(err) });
		}
	}
}

/**
 * Build a `-c http.extraHeader=…` git argument that authenticates to GitHub with
 * a GITHUB_TOKEN, so a credential-less sandbox can clone/push a private remote
 * WITHOUT the token ever landing in the remote URL or `.git/config`. Host-pinned
 * to an exact `github.com` https URL (a crafted/lookalike remote, or one with
 * pre-existing userinfo, gets no header — the token can't be exfiltrated to
 * another host). Returns '' (no auth) when there's no token or the remote isn't a
 * bare https github.com URL. Always followed by a trailing space so it slots in
 * after `git `.
 */
function gitAuthArgs(remote: string, token: string | undefined): string {
	if (!token) return '';
	try {
		const u = new URL(remote);
		if (u.protocol !== 'https:' || u.hostname.toLowerCase() !== 'github.com' || u.username || u.password) {
			return '';
		}
	} catch {
		return '';
	}
	const basic = Buffer.from(`x-access-token:${token}`).toString('base64');
	return `-c ${shq('http.extraHeader=AUTHORIZATION: basic ' + basic)} `;
}

/** Strip git credentials (extraHeader basic auth, token-in-URL) from a string. */
function redactSecrets(s: string): string {
	return s
		.replace(/AUTHORIZATION: basic [A-Za-z0-9+/=]+/gi, 'AUTHORIZATION: basic <redacted>')
		.replace(/x-access-token:[^@\s'"]+/g, 'x-access-token:<redacted>');
}

function renderTaskBody(task: Task): string {
	const parts: string[] = [`Implement task ${task.id}: ${task.title}`];
	if (task.description) parts.push(`Description:\n${task.description}`);
	if (task.design) {
		parts.push(`Approved design / implementation plan (follow it exactly):\n${task.design}`);
	}
	if (task.acceptance_criteria) parts.push(`Acceptance criteria:\n${task.acceptance_criteria}`);
	parts.push('Make exactly the code changes this task requires in the current working directory, then stop.');
	return parts.join('\n\n');
}

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
		// redact: the command may carry an http.extraHeader basic-auth credential.
		throw new Error(redactSecrets(`runner: command failed (exit ${res.exitCode}): ${command}\n${res.result ?? ''}`));
	}
	return res.result ?? '';
}

/** Like sh() but never throws: returns ok=false + output for best-effort commands. */
async function shTry(
	sandbox: { process: { executeCommand: (c: string) => Promise<{ result?: string; exitCode?: number }> } },
	command: string,
): Promise<{ ok: boolean; output: string }> {
	const res = await sandbox.process.executeCommand(`sh -lc ${shq(command)}`);
	return { ok: (res.exitCode ?? 0) === 0, output: redactSecrets(res.result ?? '') };
}
