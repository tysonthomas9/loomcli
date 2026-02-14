import { execFile as execFileCallback } from 'child_process';
import { createHash } from 'crypto';
import { fileURLToPath } from 'url';
import * as path from 'path';
import { promisify } from 'util';

const execFile = promisify(execFileCallback);
const currentFilePath = fileURLToPath(import.meta.url);
const currentDir = path.dirname(currentFilePath);

const DETERMINISTIC_SCRIPT = path.resolve(
  currentDir,
  '../fixtures/deterministic-terminal.sh'
);

const DEFAULT_COLS = 120;
const DEFAULT_ROWS = 36;

function shellEscape(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

async function runTmux(args: string[]): Promise<string> {
  const { stdout } = await execFile('tmux', args, {
    env: { ...process.env, TERM: 'xterm-256color' },
  });
  return stdout ?? '';
}

function isNoServerError(error: unknown): boolean {
  const stderr = String((error as { stderr?: string })?.stderr ?? '').toLowerCase();
  const message = String((error as Error)?.message ?? '').toLowerCase();
  return (
    stderr.includes('failed to connect to server') ||
    stderr.includes('no server running') ||
    message.includes('failed to connect to server') ||
    message.includes('no server running')
  );
}

export async function isTmuxAvailable(): Promise<boolean> {
  try {
    await execFile('tmux', ['-V']);
    return true;
  } catch {
    return false;
  }
}

export async function listSessions(): Promise<string[]> {
  try {
    const out = await runTmux(['list-sessions', '-F', '#{session_name}']);
    return out
      .split('\n')
      .map((entry) => entry.trim())
      .filter((entry) => entry.length > 0);
  } catch (error) {
    if (isNoServerError(error)) {
      return [];
    }
    throw error;
  }
}

export async function cleanupSession(sessionName: string): Promise<void> {
  try {
    await runTmux(['kill-session', '-t', sessionName]);
  } catch {
    // Session may not exist.
  }
}

export async function cleanupSessionsMatching(pattern: RegExp): Promise<void> {
  const sessions = await listSessions();
  for (const session of sessions) {
    if (pattern.test(session)) {
      await cleanupSession(session);
    }
  }
}

export async function seedDeterministicSession(
  sessionName: string,
  cols = DEFAULT_COLS,
  rows = DEFAULT_ROWS
): Promise<void> {
  await cleanupSession(sessionName);

  await runTmux([
    'new-session',
    '-d',
    '-s',
    sessionName,
    '-x',
    String(cols),
    '-y',
    String(rows),
    `bash ${shellEscape(DETERMINISTIC_SCRIPT)}`,
  ]);
  await runTmux(['set-option', '-t', sessionName, 'status', 'off']);
  await runTmux(['set-option', '-t', sessionName, 'mouse', 'off']);
  await runTmux(['resize-window', '-t', sessionName, '-x', String(cols), '-y', String(rows)]);
}

export async function seedParitySessions(
  prefix = '8080',
  agentName = 'ember'
): Promise<{ talkSession: string; agentSession: string }> {
  const talkSession = `${prefix}-talk-to-lead`;
  const agentSession = `loom-plan0-${agentName}-12345`;

  // Remove competing agent sessions so server discovery picks the seeded session.
  await cleanupSessionsMatching(new RegExp(`^loom-[a-zA-Z0-9_-]+-${agentName}-[0-9]+$`));

  await seedDeterministicSession(talkSession);
  await seedDeterministicSession(agentSession);
  return { talkSession, agentSession };
}

export async function capturePane(sessionName: string): Promise<string> {
  return runTmux(['capture-pane', '-p', '-t', sessionName, '-S', '-200']);
}

function normalizePane(text: string): string {
  return text
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => line.replace(/\s+$/g, ''))
    .join('\n')
    .trim();
}

export async function capturePaneChecksum(sessionName: string): Promise<string> {
  const pane = await capturePane(sessionName);
  return createHash('sha256').update(normalizePane(pane)).digest('hex');
}
