/**
 * Typed client for FleetDB-backed delivery groups on the Pull Requests facade.
 * Mutations require If-Match (revision) and X-Idempotency-Key (intent).
 * Never mutates GitHub.
 */

import {
  api,
  apiErrorFromResponse,
  unwrapResponse,
  ApiError,
} from "@/api/common";
import type { components } from "@/types/generated/openapi";

export type DeliveryGroupView = components["schemas"]["DeliveryGroupView"];
export type DeliveryGroupMemberView =
  components["schemas"]["DeliveryGroupMemberView"];
export type DeliveryGroupList = components["schemas"]["DeliveryGroupList"];
export type DeliveryGroupWrite = components["schemas"]["DeliveryGroupWrite"];
export type DeliveryGroupPreview =
  components["schemas"]["DeliveryGroupPreview"];
export type DeliveryGroupCreateRequest =
  components["schemas"]["DeliveryGroupCreateRequest"];
export type DeliveryGroupUpdateRequest =
  components["schemas"]["DeliveryGroupUpdateRequest"];
export type DeliveryGroupSetMembersRequest =
  components["schemas"]["DeliveryGroupSetMembersRequest"];
export type DeliveryGroupMemberInput =
  components["schemas"]["DeliveryGroupMemberInput"];

export type DeliveryGroupStateFilter = "active" | "archived" | "all";

export type DeliveryGroupWriteErrorKind =
  | "stale_revision"
  | "duplicate_membership"
  | "idempotency_key_reused"
  | "conflict"
  | "not_found"
  | "unavailable"
  | "unknown";

export interface DeliveryGroupWriteError {
  kind: DeliveryGroupWriteErrorKind;
  status: number;
  message: string;
  /** Persisted group when the server still returns one after a partial failure. */
  group?: DeliveryGroupView;
  code?: string;
}

function errorBody(error: unknown): {
  code?: string;
  message?: string;
  data?: { group?: DeliveryGroupView };
} {
  if (error && typeof error === "object") {
    return error as {
      code?: string;
      message?: string;
      data?: { group?: DeliveryGroupView };
    };
  }
  return {};
}

export function classifyDeliveryGroupWriteError(
  err: unknown,
): DeliveryGroupWriteError {
  if (err instanceof ApiError) {
    const body = errorBody(err.body);
    const code = body.code ?? "";
    const message =
      body.message ||
      err.statusText ||
      err.message ||
      "Delivery group write failed";
    const group = body.data?.group;

    if (err.status === 412 || code === "stale_revision") {
      return {
        kind: "stale_revision",
        status: err.status,
        message,
        ...(group ? { group } : {}),
        code: code || "stale_revision",
      };
    }
    if (err.status === 409 && code === "duplicate_membership") {
      return {
        kind: "duplicate_membership",
        status: err.status,
        message,
        ...(group ? { group } : {}),
        code,
      };
    }
    if (err.status === 409 && code === "idempotency_key_reused") {
      return {
        kind: "idempotency_key_reused",
        status: err.status,
        message,
        ...(group ? { group } : {}),
        code,
      };
    }
    if (err.status === 409) {
      return {
        kind: "conflict",
        status: err.status,
        message,
        ...(group ? { group } : {}),
        ...(code ? { code } : {}),
      };
    }
    if (err.status === 404) {
      return { kind: "not_found", status: err.status, message, code };
    }
    if (err.status === 503) {
      return { kind: "unavailable", status: err.status, message, code };
    }
    return {
      kind: "unknown",
      status: err.status,
      message,
      ...(group ? { group } : {}),
      ...(code ? { code } : {}),
    };
  }
  return {
    kind: "unknown",
    status: 0,
    message: err instanceof Error ? err.message : String(err),
  };
}

export async function listDeliveryGroups(
  ws: string,
  options: {
    state?: DeliveryGroupStateFilter;
    epicId?: string;
    limit?: number;
    cursor?: string;
  } = {},
): Promise<DeliveryGroupList> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/delivery-groups",
    {
      params: {
        path: { ws },
        query: {
          ...(options.state ? { state: options.state } : {}),
          ...(options.epicId ? { epic_id: options.epicId } : {}),
          ...(options.limit != null ? { limit: options.limit } : {}),
          ...(options.cursor ? { cursor: options.cursor } : {}),
        },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function getDeliveryGroup(
  ws: string,
  groupId: string,
): Promise<DeliveryGroupWrite> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/delivery-groups/{group_id}",
    {
      params: { path: { ws, group_id: groupId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

/**
 * Lookup the active delivery group that contains `prKey`.
 *
 * Thin facade over FleetDB GetByPR. 404 means not in an active group — do not
 * page all delivery groups client-side as a membership fallback.
 */
export async function getDeliveryGroupByPr(
  ws: string,
  prKey: string,
): Promise<DeliveryGroupWrite> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/pull-request-delivery-groups/{pr_key}",
    {
      params: { path: { ws, pr_key: prKey } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function previewDeliveryGroup(
  ws: string,
  groupId: string,
): Promise<DeliveryGroupPreview> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/delivery-groups/{group_id}/preview",
    {
      params: { path: { ws, group_id: groupId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function createDeliveryGroup(
  ws: string,
  body: DeliveryGroupCreateRequest,
  intentKey: string,
): Promise<DeliveryGroupWrite> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/delivery-groups",
    {
      params: {
        path: { ws },
        header: { "X-Idempotency-Key": intentKey },
      },
      body,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function updateDeliveryGroup(
  ws: string,
  groupId: string,
  body: DeliveryGroupUpdateRequest,
  revision: number,
  intentKey: string,
): Promise<DeliveryGroupWrite> {
  const { data, error, response } = await api.PATCH(
    "/api/workspaces/{ws}/delivery-groups/{group_id}",
    {
      params: {
        path: { ws, group_id: groupId },
        header: {
          "If-Match": String(revision),
          "X-Idempotency-Key": intentKey,
        },
      },
      body,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function setDeliveryGroupMembers(
  ws: string,
  groupId: string,
  body: DeliveryGroupSetMembersRequest,
  revision: number,
  intentKey: string,
): Promise<DeliveryGroupWrite> {
  const { data, error, response } = await api.PUT(
    "/api/workspaces/{ws}/delivery-groups/{group_id}/members",
    {
      params: {
        path: { ws, group_id: groupId },
        header: {
          "If-Match": String(revision),
          "X-Idempotency-Key": intentKey,
        },
      },
      body,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function archiveDeliveryGroup(
  ws: string,
  groupId: string,
  revision: number,
  intentKey: string,
): Promise<DeliveryGroupWrite> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/delivery-groups/{group_id}/archive",
    {
      params: {
        path: { ws, group_id: groupId },
        header: {
          "If-Match": String(revision),
          "X-Idempotency-Key": intentKey,
        },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}
