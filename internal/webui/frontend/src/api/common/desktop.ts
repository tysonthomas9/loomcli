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
