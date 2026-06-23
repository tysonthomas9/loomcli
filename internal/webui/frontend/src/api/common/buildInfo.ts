import { get } from "./client";

export interface BuildInfo {
  frontend_hash?: string;
  build?: string;
  git_hash?: string;
  built_at?: string;
}

export function fetchBuildInfo(signal?: AbortSignal): Promise<BuildInfo> {
  return get<BuildInfo>("/api/build-info", {
    timeout: 10000,
    ...(signal ? { signal } : {}),
  });
}
