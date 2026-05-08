/**
 * API client for the onboarding status endpoint.
 * Stateless — caching belongs in the consuming hook.
 */

import { get, ApiError } from "@/api/common";
import type { OnboardingStatusWire } from "@/types/onboarding";

interface ApiSuccess<T> {
  success: true;
  data: T;
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

/**
 * Fetch onboarding status. When `workspaceId` is provided, hits the
 * workspace-scoped endpoint; otherwise the top-level endpoint that only
 * evaluates step 1.
 */
export async function fetchOnboardingStatus(
  workspaceId?: string,
): Promise<OnboardingStatusWire> {
  const path = workspaceId
    ? `/api/workspaces/${encodeURIComponent(workspaceId)}/onboarding/status`
    : "/api/onboarding/status";
  const response = await get<ApiResult<OnboardingStatusWire>>(path);
  return unwrap(response);
}
