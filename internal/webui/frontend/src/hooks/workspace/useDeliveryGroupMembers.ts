/**
 * Delivery-group member writes using canonical revision + intent-key contract.
 * Surfaces stale revision, duplicate membership, and unknown outcomes without
 * erasing callers' persisted member lists on failure.
 */

import { useCallback, useState } from "react";

import {
  classifyDeliveryGroupWriteError,
  setDeliveryGroupMembers,
  type DeliveryGroupMemberInput,
  type DeliveryGroupView,
  type DeliveryGroupWriteError,
} from "@/api/workspace/deliveryGroups";
import { newDeliveryGroupIntentKey } from "@/utils/pullRequest/readinessDisplay";
import { useWorkspaceContext } from "./useWorkspaceContext";

export interface SetMembersResult {
  group: DeliveryGroupView | null;
  error: DeliveryGroupWriteError | null;
}

export interface UseDeliveryGroupMembersReturn {
  saving: boolean;
  lastError: DeliveryGroupWriteError | null;
  setMembers: (
    groupId: string,
    revision: number,
    members: DeliveryGroupMemberInput[],
  ) => Promise<SetMembersResult>;
  clearError: () => void;
}

export function useDeliveryGroupMembers(): UseDeliveryGroupMembersReturn {
  const { workspaceId } = useWorkspaceContext();
  const [saving, setSaving] = useState(false);
  const [lastError, setLastError] = useState<DeliveryGroupWriteError | null>(
    null,
  );

  const clearError = useCallback(() => setLastError(null), []);

  const setMembers = useCallback(
    async (
      groupId: string,
      revision: number,
      members: DeliveryGroupMemberInput[],
    ): Promise<SetMembersResult> => {
      setSaving(true);
      setLastError(null);
      try {
        const write = await setDeliveryGroupMembers(
          workspaceId,
          groupId,
          { members },
          revision,
          newDeliveryGroupIntentKey("dg-members"),
        );
        return { group: write.group, error: null };
      } catch (err) {
        const classified = classifyDeliveryGroupWriteError(err);
        setLastError(classified);
        return { group: classified.group ?? null, error: classified };
      } finally {
        setSaving(false);
      }
    },
    [workspaceId],
  );

  return { saving, lastError, setMembers, clearError };
}
