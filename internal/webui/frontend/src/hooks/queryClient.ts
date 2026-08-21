import { QueryClient } from "@tanstack/react-query";

import { ApiError } from "@/types/common";

export function getHttpErrorStatus(error: unknown): number | null {
  if (error instanceof ApiError) return error.status;
  if (error && typeof error === "object" && "status" in error) {
    const status = (error as { status?: unknown }).status;
    return typeof status === "number" ? status : null;
  }
  return null;
}

export function isHttp429Error(error: unknown): boolean {
  return getHttpErrorStatus(error) === 429;
}

export function shouldRetryQuery(count: number, error: unknown): boolean {
  if (isHttp429Error(error)) return false;
  return count < 1;
}

export function createAppQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: false,
        refetchOnReconnect: true,
        refetchOnMount: true,
        retry: shouldRetryQuery,
      },
    },
  });
}
