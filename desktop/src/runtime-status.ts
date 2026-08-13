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

export function terminalRuntimeFailure(status: RuntimeStatus): string {
  if (status.healthy || status.runtime?.status !== "failed") {
    return "";
  }
  return (
    status.runtime.error ||
    status.error ||
    "The local runtime failed before it became healthy."
  );
}
