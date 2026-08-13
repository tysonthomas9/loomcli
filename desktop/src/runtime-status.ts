export type RuntimeInfo = {
  status?: string;
  pid?: number;
  serve_pid?: number;
  data_dir?: string;
  url?: string;
  port?: number;
  claims_paused?: boolean;
  started_at?: string;
  updated_at?: string;
  error?: string;
};

export type RuntimeStatus = {
  runtime?: RuntimeInfo;
  healthy: boolean;
  error?: string;
};

function sameRuntimeSnapshot(
  current: RuntimeInfo | undefined,
  stale: RuntimeInfo | undefined,
): boolean {
  if (!current || !stale) return false;
  return (
    current.status === stale.status &&
    current.pid === stale.pid &&
    current.serve_pid === stale.serve_pid &&
    current.started_at === stale.started_at &&
    current.updated_at === stale.updated_at
  );
}

export function terminalRuntimeFailure(
  status: RuntimeStatus,
  preStartRuntime?: RuntimeInfo,
): string {
  if (status.healthy || status.runtime?.status !== "failed") {
    return "";
  }
  if (sameRuntimeSnapshot(status.runtime, preStartRuntime)) {
    return "";
  }
  return (
    status.runtime.error ||
    status.error ||
    "The local runtime failed before it became healthy."
  );
}
