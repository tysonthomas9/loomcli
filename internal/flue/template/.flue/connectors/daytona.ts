/**
 * Daytona connector for Flue.
 *
 * Wraps an already-initialized Daytona sandbox into Flue's SandboxFactory
 * interface. The user creates and configures the sandbox using the Daytona
 * SDK directly — Flue just adapts it.
 *
 * Installed per flue's published Daytona connector (flue add daytona).
 */
import { createSandboxSessionEnv } from '@flue/runtime';
import type { SandboxApi, SandboxFactory, SessionEnv, FileStat } from '@flue/runtime';
import type { Sandbox as DaytonaSandbox } from '@daytona/sdk';

/**
 * Implements SandboxApi by wrapping Daytona's TypeScript SDK.
 */
class DaytonaSandboxApi implements SandboxApi {
	constructor(private sandbox: DaytonaSandbox) {}

	async readFile(path: string): Promise<string> {
		const buffer = await this.sandbox.fs.downloadFile(path);
		return buffer.toString('utf-8');
	}

	async readFileBuffer(path: string): Promise<Uint8Array> {
		const buffer = await this.sandbox.fs.downloadFile(path);
		return new Uint8Array(buffer);
	}

	async writeFile(path: string, content: string | Uint8Array): Promise<void> {
		const buffer =
			typeof content === 'string' ? Buffer.from(content, 'utf-8') : Buffer.from(content);
		await this.sandbox.fs.uploadFile(buffer, path);
	}

	async stat(path: string): Promise<FileStat> {
		const info = await this.sandbox.fs.getFileDetails(path);
		return {
			isFile: !info.isDir,
			isDirectory: info.isDir ?? false,
			isSymbolicLink: false,
			size: info.size ?? 0,
			mtime: info.modTime ? new Date(info.modTime) : new Date(),
		};
	}

	async readdir(path: string): Promise<string[]> {
		const entries = await this.sandbox.fs.listFiles(path);
		return entries.map((e) => e.name).filter((name): name is string => !!name);
	}

	async exists(path: string): Promise<boolean> {
		try {
			await this.sandbox.fs.getFileDetails(path);
			return true;
		} catch {
			return false;
		}
	}

	async mkdir(path: string, options?: { recursive?: boolean }): Promise<void> {
		if (options?.recursive) {
			await this.exec(`mkdir -p '${path.replace(/'/g, "'\\''")}'`);
			return;
		}
		await this.sandbox.fs.createFolder(path, '755');
	}

	async rm(path: string, options?: { recursive?: boolean; force?: boolean }): Promise<void> {
		await this.sandbox.fs.deleteFile(path, options?.recursive);
	}

	async exec(
		command: string,
		options?: { cwd?: string; env?: Record<string, string>; timeout?: number },
	): Promise<{ stdout: string; stderr: string; exitCode: number }> {
		const response = await this.sandbox.process.executeCommand(
			command,
			options?.cwd,
			options?.env,
			options?.timeout,
		);
		return {
			stdout: response.result ?? '',
			stderr: '',
			exitCode: response.exitCode ?? 0,
		};
	}
}

/**
 * Create a Flue sandbox factory from an initialized Daytona sandbox.
 * The user owns the sandbox lifecycle; Flue wraps it into a SessionEnv
 * for agent use.
 */
export function daytona(sandbox: DaytonaSandbox): SandboxFactory {
	return {
		async createSessionEnv({ cwd }: { id: string; cwd?: string }): Promise<SessionEnv> {
			const sandboxCwd = cwd ?? (await sandbox.getWorkDir()) ?? '/home/daytona';
			const api = new DaytonaSandboxApi(sandbox);
			return createSandboxSessionEnv(api, sandboxCwd);
		},
	};
}
