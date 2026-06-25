//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/loomcli/sdk/internal.js
function trim(value) {
	if (value === void 0 || value === null) return "";
	return String(value).trim();
}
function pickEnv(env, ...names) {
	for (const name of names) {
		const value = trim(env?.[name]);
		if (value) return value;
	}
	return "";
}
//#endregion
//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/loomcli/sdk/runner.js
var RunnerEnv = Object.freeze({
	apiUrl: "LOOM_TASK_RUN_API_URL",
	baseUrl: "LOOM_FLEET_DB_URL",
	apiKey: "LOOM_FLEET_DB_API_KEY",
	actor: "LOOM_FLEET_DB_ACTOR",
	agentName: "LOOM_AGENT_NAME",
	workspace: "LOOM_WORKSPACE",
	taskRunId: "LOOM_TASK_RUN_ID",
	taskId: "LOOM_TASK_ID",
	nodeId: "LOOM_TASK_RUN_NODE_ID",
	leaseId: "LOOM_TASK_RUN_LEASE_ID",
	leaseToken: "LOOM_TASK_RUN_LEASE_TOKEN",
	runnerLeaseToken: "LOOM_RUNNER_LEASE_TOKEN",
	fencingToken: "LOOM_TASK_RUN_FENCING_TOKEN",
	requestJson: "LOOM_TASK_RUN_REQUEST_JSON"
});
var LoomAPIError = class extends Error {
	constructor(message, options = {}) {
		super(message);
		this.name = "LoomAPIError";
		this.status = options.status || 0;
		this.code = options.code || "";
		this.details = options.details;
		this.responseBody = options.responseBody || "";
	}
};
var TaskRunClient = class TaskRunClient {
	#requestPayload;
	static fromEnv(env = process.env, options = {}) {
		return new TaskRunClient({
			apiUrl: options.apiUrl || pickEnv(env, RunnerEnv.apiUrl),
			baseUrl: options.baseUrl || pickEnv(env, RunnerEnv.baseUrl),
			apiKey: options.apiKey || pickEnv(env, RunnerEnv.apiKey),
			actor: options.actor || pickEnv(env, RunnerEnv.actor, RunnerEnv.agentName, "USER"),
			authToken: options.authToken,
			fetch: options.fetch,
			workspace: options.workspace || pickEnv(env, RunnerEnv.workspace, "LOOM_DRIVER_WORKSPACE"),
			taskRunId: options.taskRunId || pickEnv(env, RunnerEnv.taskRunId),
			taskId: options.taskId || pickEnv(env, RunnerEnv.taskId),
			nodeId: options.nodeId || pickEnv(env, RunnerEnv.nodeId),
			leaseId: options.leaseId || pickEnv(env, RunnerEnv.leaseId),
			leaseToken: options.leaseToken || pickEnv(env, RunnerEnv.leaseToken, RunnerEnv.runnerLeaseToken),
			fencingToken: options.fencingToken || pickEnv(env, RunnerEnv.fencingToken),
			requestJson: options.requestJson ?? pickEnv(env, RunnerEnv.requestJson)
		});
	}
	constructor(options = {}) {
		this.apiUrl = trim(options.apiUrl);
		this.serveMode = this.apiUrl !== "";
		this.baseUrl = this.serveMode ? this.apiUrl.replace(/\/+$/, "") : normalizeBaseUrl(options.baseUrl);
		this.workspace = required("workspace", options.workspace);
		this.taskRunId = required("taskRunId", options.taskRunId);
		this.taskId = trim(options.taskId);
		this.nodeId = required("nodeId", options.nodeId);
		this.leaseId = required("leaseId", options.leaseId);
		this.leaseToken = required("leaseToken", options.leaseToken);
		this.fencingToken = parseFencingToken(required("fencingToken", options.fencingToken), { preserveString: this.serveMode });
		this.requestJson = trim(options.requestJson);
		this.#requestPayload = parseRequestJson(this.requestJson);
		this.apiKey = trim(options.apiKey);
		this.actor = trim(options.actor);
		this.authToken = trim(options.authToken);
		this.fetch = options.fetch || globalThis.fetch;
		if (typeof this.fetch !== "function") throw new TypeError("fetch is required; use Node.js 18+ or pass a fetch implementation");
		this.logs = Object.freeze({ append: (entry, requestOptions) => this.appendLog(entry, requestOptions) });
		this.artifacts = Object.freeze({
			declare: (input, requestOptions) => this.declareArtifact(input, requestOptions),
			get: (artifactId, requestOptions) => this.getArtifact(artifactId, requestOptions),
			list: (input, requestOptions) => this.listArtifacts(input, requestOptions)
		});
		this.runtimeCredentials = Object.freeze({ get: (input, requestOptions) => this.getRuntimeCredential(input, requestOptions) });
	}
	request() {
		return cloneRequestPayload(this.#requestPayload);
	}
	input() {
		const request = this.#requestPayload;
		if (!request || request.input === void 0 || request.input === null) return;
		return cloneRequestPayload(request.input);
	}
	async getTaskRun(options = {}) {
		if (this.serveMode) return this.#op("get", {}, options);
		return this.#json("GET", this.#taskRunPath(), void 0, options);
	}
	async getTask(options = {}) {
		if (this.serveMode) {
			const out = await this.#op("task-get", {}, options);
			const taskRun = out?.taskRun;
			if (!out?.task) return { taskRun };
			return {
				...out.task,
				task_run: taskRun,
				taskRun
			};
		}
		const taskRun = await this.getTaskRun(options);
		const taskId = trim(taskRun.task_id || taskRun.taskId || this.taskId);
		if (!taskId) return { taskRun };
		return {
			...await this.#json("GET", this.#workspacePath(`/issues/${escapePath(taskId)}`), void 0, options),
			task_run: taskRun,
			taskRun
		};
	}
	async heartbeat(input = {}, options = {}) {
		if (this.serveMode) return this.#op("heartbeat", compact({
			runtimeMetadata: metadata(input.runtime_metadata || input.runtimeMetadata),
			logsRef: input.logs_ref || input.logsRef,
			artifactsRef: input.artifacts_ref || input.artifactsRef
		}), options);
		const body = this.#ownerBody({
			runtime_metadata: metadata(input.runtime_metadata || input.runtimeMetadata),
			logs_ref: input.logs_ref || input.logsRef,
			artifacts_ref: input.artifacts_ref || input.artifactsRef
		});
		return this.#json("POST", this.#taskRunPath("/heartbeat"), body, withLease(options));
	}
	async appendLog(input = {}, options = {}) {
		if (input.text === void 0 || input.text === null) throw new TypeError("logs.append requires text");
		if (this.serveMode) return this.#op("log-append", compact({
			stream: input.stream || "stdout",
			text: String(input.text),
			timestamp: input.timestamp
		}), options);
		const body = this.#ownerBody({
			stream: input.stream || "stdout",
			text: String(input.text),
			timestamp: input.timestamp
		});
		return this.#json("POST", this.#taskRunPath("/logs"), body, withLease(options));
	}
	async declareArtifact(input = {}, options = {}) {
		const type = required("artifact type", input.type);
		const artifactId = trim(input.artifact_id || input.artifactId || input.id);
		const idempotencyKey = trim(input.idempotency_key || input.idempotencyKey);
		const artifactMetadata = metadata(input.metadata) || {};
		if (idempotencyKey && artifactMetadata.idempotency_key === void 0) artifactMetadata.idempotency_key = idempotencyKey;
		if (this.serveMode) {
			const artifact = await this.#op("artifact-declare", compact({
				artifactId,
				taskId: input.task_id || input.taskId || this.taskId,
				type,
				uri: input.uri,
				summary: input.summary,
				mimeType: input.mime_type || input.mimeType,
				sizeBytes: input.size_bytes ?? input.sizeBytes,
				checksum: input.checksum,
				contentHash: input.content_hash || input.contentHash,
				visibility: input.visibility,
				redactionStatus: input.redaction_status || input.redactionStatus,
				durableStatus: input.durable_status || input.durableStatus || "declared",
				metadata: Object.keys(artifactMetadata).length > 0 ? artifactMetadata : void 0
			}), options);
			return new ArtifactHandle(this, artifact);
		}
		const body = compact({
			artifact_id: artifactId,
			owner_type: "task_run",
			owner_id: this.taskRunId,
			task_id: input.task_id || input.taskId || this.taskId,
			type,
			uri: input.uri,
			summary: input.summary,
			mime_type: input.mime_type || input.mimeType,
			size_bytes: input.size_bytes ?? input.sizeBytes,
			checksum: input.checksum,
			content_hash: input.content_hash || input.contentHash,
			visibility: input.visibility,
			redaction_status: input.redaction_status || input.redactionStatus,
			durable_status: input.durable_status || input.durableStatus || "declared",
			metadata: Object.keys(artifactMetadata).length > 0 ? artifactMetadata : void 0
		});
		const artifact = await this.#json("POST", this.#workspacePath("/artifacts"), body, options);
		return new ArtifactHandle(this, artifact);
	}
	async getArtifact(artifactId, options = {}) {
		if (this.serveMode) {
			const artifact = await this.#op("artifact-get", { artifactId: required("artifactId", artifactId) }, options);
			return new ArtifactHandle(this, artifact);
		}
		const artifact = await this.#json("GET", this.#artifactPath(artifactId), void 0, options);
		return new ArtifactHandle(this, artifact);
	}
	async listArtifacts(input = {}, options = {}) {
		if (this.serveMode) {
			const out = await this.#op("artifact-list", compact({
				type: input.type,
				durableStatus: input.durable_status || input.durableStatus || input.status,
				limit: input.limit
			}), options);
			return {
				...out,
				artifacts: (out.artifacts || []).map((artifact) => new ArtifactHandle(this, artifact))
			};
		}
		const params = new URLSearchParams();
		params.set("owner_type", "task_run");
		params.set("owner_id", this.taskRunId);
		for (const [key, value] of Object.entries({
			type: input.type,
			durable_status: input.durable_status || input.durableStatus || input.status,
			limit: input.limit
		})) if (value !== void 0 && value !== null && String(value).trim() !== "") params.set(key, String(value));
		const out = await this.#json("GET", `${this.#workspacePath("/artifacts")}?${params}`, void 0, options);
		return {
			...out,
			artifacts: (out.artifacts || []).map((artifact) => new ArtifactHandle(this, artifact))
		};
	}
	async uploadArtifactContent(artifactId, content, options = {}) {
		if (content === void 0 || content === null) throw new TypeError("artifact upload content is required");
		const headers = {};
		const contentType = trim(options.mimeType || options.contentType);
		if (contentType) headers["Content-Type"] = contentType;
		if (this.serveMode) {
			const path = `${this.#workspacePath("/task-run/artifacts")}/${escapePath(required("artifactId", artifactId))}/content`;
			return this.#raw("PUT", path, content, {
				...options,
				headers
			});
		}
		return this.#raw("PUT", `${this.#artifactPath(artifactId)}/content`, content, {
			...options,
			headers
		});
	}
	async finalizeArtifact(artifactId, input = {}, options = {}) {
		if (this.serveMode) return this.#op("artifact-finalize", compact({
			artifactId: required("artifactId", artifactId),
			uri: input.uri,
			summary: input.summary,
			mimeType: input.mime_type || input.mimeType,
			sizeBytes: input.size_bytes ?? input.sizeBytes,
			checksum: input.checksum,
			contentHash: input.content_hash || input.contentHash,
			visibility: input.visibility,
			redactionStatus: input.redaction_status || input.redactionStatus,
			metadata: input.metadata
		}), options);
		const body = compact({
			uri: input.uri,
			summary: input.summary,
			mime_type: input.mime_type || input.mimeType,
			size_bytes: input.size_bytes ?? input.sizeBytes,
			checksum: input.checksum,
			content_hash: input.content_hash || input.contentHash,
			visibility: input.visibility,
			redaction_status: input.redaction_status || input.redactionStatus,
			metadata: input.metadata
		});
		return this.#json("POST", `${this.#artifactPath(artifactId)}/finalize`, body, options);
	}
	async getRuntimeCredential(input = {}, options = {}) {
		if (!this.serveMode) throw new LoomAPIError("runtime credentials require the loom serve task-run API", { code: "serve_transport_required" });
		return this.#op("runtime-credential", { provider: required("credential provider", input.provider) }, options);
	}
	async completeRun(input = {}, options = {}) {
		const artifactIds = normalizeStringList(input.required_artifact_ids || input.requiredArtifactIDs || input.artifact_ids || input.artifactIds);
		const policy = input.taskStatusPolicy || input.task_status_policy || {};
		const closeTask = input.close_task ?? input.closeTask ?? policy.action === "close";
		const reason = input.close_reason || input.closeReason || policy.reason;
		if (this.serveMode) return this.#op("complete", compact({
			completionId: input.completion_id || input.completionId || `complete-${this.taskRunId}`,
			status: input.status || "completed",
			exitCode: input.exit_code ?? input.exitCode,
			logsRef: input.logs_ref || input.logsRef,
			artifactsRef: input.artifacts_ref || input.artifactsRef,
			requiredArtifactIds: artifactIds.length > 0 ? artifactIds : void 0,
			requireArtifacts: input.require_artifacts ?? input.requireArtifacts ?? (artifactIds.length > 0 ? true : void 0),
			inputTokens: input.input_tokens ?? input.inputTokens,
			outputTokens: input.output_tokens ?? input.outputTokens,
			cacheReadTokens: input.cache_read_tokens ?? input.cacheReadTokens,
			cacheWriteTokens: input.cache_write_tokens ?? input.cacheWriteTokens,
			estimatedCostUsd: input.estimated_cost_usd ?? input.estimatedCostUsd,
			runtimeMetadata: metadata(input.runtime_metadata || input.runtimeMetadata),
			errorClass: input.error_class || input.errorClass,
			errorMessage: input.error_message || input.errorMessage,
			closeTask: closeTask || void 0,
			closeReason: reason
		}), options);
		const body = this.#ownerBody({
			completion_id: input.completion_id || input.completionId || `complete-${this.taskRunId}`,
			status: input.status || "completed",
			exit_code: input.exit_code ?? input.exitCode,
			logs_ref: input.logs_ref || input.logsRef,
			artifacts_ref: input.artifacts_ref || input.artifactsRef,
			required_artifact_ids: artifactIds.length > 0 ? artifactIds : void 0,
			require_artifacts: input.require_artifacts ?? input.requireArtifacts ?? (artifactIds.length > 0 ? true : void 0),
			input_tokens: input.input_tokens ?? input.inputTokens,
			output_tokens: input.output_tokens ?? input.outputTokens,
			cache_read_tokens: input.cache_read_tokens ?? input.cacheReadTokens,
			cache_write_tokens: input.cache_write_tokens ?? input.cacheWriteTokens,
			estimated_cost_usd: input.estimated_cost_usd ?? input.estimatedCostUsd,
			runtime_metadata: metadata(input.runtime_metadata || input.runtimeMetadata),
			error_class: input.error_class || input.errorClass,
			error_message: input.error_message || input.errorMessage,
			close_task: closeTask || void 0,
			close_reason: reason
		});
		return this.#json("POST", this.#taskRunPath("/complete"), body, withLease(options));
	}
	#ownerBody(fields) {
		return compact({
			node_id: this.nodeId,
			lease_id: this.leaseId,
			fencing_token: this.fencingToken,
			...fields
		});
	}
	#taskRunPath(suffix = "") {
		return this.#workspacePath(`/task-runs/${escapePath(this.taskRunId)}${suffix}`);
	}
	#artifactPath(artifactId) {
		return this.#workspacePath(`/artifacts/${escapePath(required("artifactId", artifactId))}`);
	}
	#workspacePath(suffix) {
		if (this.serveMode) return `/api/workspaces/${escapePath(this.workspace)}${suffix}`;
		return `/api/v1/${escapePath(this.workspace)}${suffix}`;
	}
	async #op(op, body, options = {}) {
		return this.#json("POST", this.#workspacePath(`/task-run/${op}`), body, options);
	}
	async #json(method, path, body, options = {}) {
		return this.#request(method, path, {
			...options,
			body: body === void 0 ? void 0 : JSON.stringify(body),
			headers: {
				"Content-Type": "application/json",
				...options.headers || {}
			}
		});
	}
	async #raw(method, path, body, options = {}) {
		return this.#request(method, path, {
			...options,
			body,
			headers: options.headers || {}
		});
	}
	async #request(method, path, options = {}) {
		const headers = {
			Accept: "application/json",
			...options.headers || {}
		};
		if (this.serveMode) {
			headers.Authorization = `Bearer ${this.leaseToken}`;
			headers["X-Loom-Task-Run-Id"] = this.taskRunId;
			headers["X-Loom-Task-Run-Node-Id"] = this.nodeId;
			headers["X-Loom-Task-Run-Lease-Id"] = this.leaseId;
			headers["X-Loom-Task-Run-Fencing-Token"] = String(this.fencingToken);
		} else {
			if (this.apiKey) {
				headers["X-API-Key"] = this.apiKey;
				headers["X-Fleet-API-Key"] = this.apiKey;
			}
			if (this.actor) headers["X-Actor"] = this.actor;
			if (this.authToken) headers.Authorization = `Bearer ${this.authToken}`;
			if (options.useLeaseToken !== false && this.leaseToken) headers["X-Lease-Token"] = this.leaseToken;
		}
		const response = await this.fetch(this.baseUrl + path, {
			method,
			headers,
			body: options.body,
			signal: options.signal
		});
		const text = await response.text();
		if (!response.ok) throw errorFromResponse(response, text);
		if (response.status === 204 || trim(text) === "") return;
		try {
			return JSON.parse(text);
		} catch (error) {
			throw new LoomAPIError(`invalid JSON response from FleetDB: ${error.message}`, {
				status: response.status,
				responseBody: text
			});
		}
	}
};
var ArtifactHandle = class {
	constructor(client, artifact) {
		this.client = client;
		this.artifact = artifact || {};
		this.id = this.artifact.artifact_id || this.artifact.artifactId || "";
	}
	async upload(content, options = {}) {
		this.artifact = await this.client.uploadArtifactContent(this.id, content, options);
		return this;
	}
	async finalize(input = {}, options = {}) {
		this.artifact = await this.client.finalizeArtifact(this.id, input, options);
		return this;
	}
	toJSON() {
		return this.artifact;
	}
};
function withLease(options) {
	return {
		...options,
		useLeaseToken: true
	};
}
function normalizeBaseUrl(value) {
	const out = trim(value);
	if (!out) throw new TypeError("baseUrl is required");
	return out.replace(/\/+$/, "");
}
function required(name, value) {
	const out = trim(value);
	if (!out) throw new TypeError(`${name} is required`);
	return out;
}
function parseFencingToken(value, options = {}) {
	const raw = trim(value);
	if (!/^[1-9]\d*$/.test(raw)) throw new TypeError("fencingToken must be a positive integer");
	if (options.preserveString) return raw;
	const token = Number(raw);
	if (!Number.isSafeInteger(token)) throw new TypeError("fencingToken must be a positive integer");
	return token;
}
function parseRequestJson(raw) {
	raw = trim(raw);
	if (!raw) return {};
	try {
		const parsed = JSON.parse(raw);
		return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
	} catch (err) {
		throw new TypeError(`LOOM_TASK_RUN_REQUEST_JSON is invalid JSON: ${err.message}`);
	}
}
function cloneRequestPayload(value) {
	if (value === void 0 || value === null) return value;
	return JSON.parse(JSON.stringify(value));
}
function escapePath(value) {
	return encodeURIComponent(String(value));
}
function metadata(value) {
	if (value === void 0 || value === null) return;
	if (typeof value !== "object" || Array.isArray(value)) throw new TypeError("metadata must be an object");
	const out = {};
	for (const [key, item] of Object.entries(value)) if (item !== void 0 && item !== null) out[key] = String(item);
	return out;
}
function compact(value) {
	const out = {};
	for (const [key, item] of Object.entries(value)) {
		if (item === void 0 || item === null) continue;
		if (typeof item === "string" && item.trim() === "") continue;
		out[key] = item;
	}
	return out;
}
function normalizeStringList(value) {
	if (value === void 0 || value === null) return [];
	if (!Array.isArray(value)) throw new TypeError("artifactIds must be an array");
	return value.map((item) => trim(item)).filter(Boolean);
}
function errorFromResponse(response, text) {
	let message = `FleetDB request failed with HTTP ${response.status}`;
	let code = "";
	let details;
	if (trim(text)) try {
		const body = JSON.parse(text);
		if (body?.error) {
			message = body.error.message || message;
			code = body.error.code || "";
			details = body.error.details;
		} else if (body?.message) {
			message = body.message;
			code = body.code || "";
			details = body.details;
		}
	} catch {
		message = text;
	}
	return new LoomAPIError(message, {
		status: response.status,
		code,
		details,
		responseBody: text
	});
}
//#endregion
export { TaskRunClient as i, LoomAPIError as n, RunnerEnv as r, ArtifactHandle as t };

//# sourceMappingURL=runner-x7sUbDag.js.map