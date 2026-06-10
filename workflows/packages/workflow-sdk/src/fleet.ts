/**
 * Scoped FleetDB HTTP client for workflow code.
 *
 * Every request is workspace-scoped and identifies itself as the run
 * (X-Actor: workflow:<run_id>) so FleetDB audit trails attribute side
 * effects to the wake that performed them. Local mode runs against
 * fleet-db's --auth-dev-mode; cloud mode will swap in brokered tokens
 * without changing this surface (Phase 4).
 */

export interface FleetClientOptions {
	baseUrl: string;
	workspace: string;
	/** Actor identity, e.g. `workflow:run-EPIC-1-…`. */
	actor: string;
	fetchFn?: typeof fetch;
}

export interface Issue {
	id: string;
	title?: string;
	status: string;
	parent?: string;
	issue_type?: string;
	[key: string]: unknown;
}

export interface TaskRun {
	task_run_id: string;
	driver_run_id?: string;
	task_id: string;
	status?: string;
	[key: string]: unknown;
}

export interface LedgerEntry {
	action_id: string;
	idempotency_key: string;
	action_type: string;
	target_ref: string;
	status: 'pending' | 'applied' | 'failed' | 'skipped';
	[key: string]: unknown;
}

/** Error carrying the HTTP status for callers that branch on 409s. */
export class FleetError extends Error {
	constructor(
		readonly status: number,
		message: string,
	) {
		super(message);
		this.name = 'FleetError';
	}
}

export class FleetClient {
	private readonly baseUrl: string;
	private readonly workspace: string;
	private readonly actor: string;
	private readonly fetchFn: typeof fetch;

	constructor(opts: FleetClientOptions) {
		this.baseUrl = opts.baseUrl.replace(/\/+$/, '');
		this.workspace = opts.workspace;
		this.actor = opts.actor;
		this.fetchFn = opts.fetchFn ?? fetch;
	}

	async request<T>(method: string, path: string, body?: unknown): Promise<T> {
		const res = await this.fetchFn(`${this.baseUrl}/api/v1/${encodeURIComponent(this.workspace)}/${path}`, {
			method,
			headers: {
				'Content-Type': 'application/json',
				Accept: 'application/json',
				'X-Actor': this.actor,
			},
			body: body === undefined ? undefined : JSON.stringify(body),
		});
		const text = await res.text();
		if (!res.ok) {
			throw new FleetError(res.status, `${method} ${path}: HTTP ${res.status}: ${text.slice(0, 500)}`);
		}
		return (text ? JSON.parse(text) : {}) as T;
	}

	/** Ready (unblocked, open) child tasks of an epic — the frontier. */
	async listReadyTasks(epicId: string): Promise<Issue[]> {
		const res = await this.request<{ issues: Issue[] }>(
			'GET',
			`issues/ready?parent_id=${encodeURIComponent(epicId)}`,
		);
		return res.issues ?? [];
	}

	/** All children of an epic, any status. */
	async listChildren(epicId: string): Promise<Issue[]> {
		const res = await this.request<{ issues: Issue[] }>('GET', `issues/${encodeURIComponent(epicId)}/children`);
		return res.issues ?? [];
	}

	async getIssue(id: string): Promise<Issue> {
		return this.request<Issue>('GET', `issues/${encodeURIComponent(id)}`);
	}

	async closeIssue(id: string, reason?: string): Promise<void> {
		await this.request('POST', `issues/${encodeURIComponent(id)}/close`, reason ? { reason } : {});
	}
}
