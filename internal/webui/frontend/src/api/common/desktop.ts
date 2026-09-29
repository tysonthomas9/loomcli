type TauriInvoke = <T>(
  command: string,
  args?: Record<string, unknown>,
) => Promise<T>;

type TauriGlobal = {
  core?: {
    invoke?: TauriInvoke;
  };
};

type TauriInternals = {
  invoke?: TauriInvoke;
};

declare global {
  interface Window {
    __TAURI__?: TauriGlobal;
    __TAURI_INTERNALS__?: TauriInternals;
  }
}

function getTauriInvoke(): TauriInvoke | null {
  return (
    window.__TAURI__?.core?.invoke ?? window.__TAURI_INTERNALS__?.invoke ?? null
  );
}

export function isDesktopRuntime(): boolean {
  return getTauriInvoke() !== null;
}

export async function pickDesktopFolder(): Promise<string | null> {
  const invoke = getTauriInvoke();
  if (!invoke) {
    return null;
  }
  return invoke<string | null>("pick_folder");
}

export type BrowserOperatorSessionAction = "issue" | "refresh" | "revoke";

/**
 * Result of the native `browser_operator_session` command. The shell always
 * resolves with this shape; `ok: false` carries `error`/`code`.
 */
export interface BrowserOperatorSessionResult {
  ok: boolean;
  token?: string;
  session_id?: string;
  workspace?: string;
  expires_at?: string;
  absolute_expires_at?: string;
  idle_timeout_seconds?: number;
  error?: string;
  code?: string;
}

/**
 * Ask the Loom Desktop shell to issue, refresh, or revoke the local operator
 * bearer for a workspace. Returns null when there is no native bridge (an
 * ordinary browser tab). The bearer is returned to the caller only; callers
 * must keep it in memory and never persist or log it.
 */
export async function browserOperatorSession(
  action: BrowserOperatorSessionAction,
  workspace: string,
  token?: string,
): Promise<BrowserOperatorSessionResult | null> {
  const invoke = getTauriInvoke();
  if (!invoke) {
    return null;
  }
  try {
    const result = await invoke<BrowserOperatorSessionResult | null>(
      "browser_operator_session",
      { action, workspace, token: token ?? null },
    );
    if (result == null || typeof result !== "object") {
      return {
        ok: false,
        error: "Loom Desktop returned no operator session",
        code: "browser_operator_bridge_unavailable",
      };
    }
    return result;
  } catch (err) {
    return {
      ok: false,
      error:
        err instanceof Error
          ? err.message
          : typeof err === "string"
            ? err
            : "Loom Desktop operator bridge failed",
      code: "browser_operator_bridge_unavailable",
    };
  }
}
