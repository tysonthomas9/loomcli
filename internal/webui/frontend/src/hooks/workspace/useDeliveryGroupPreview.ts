/**
 * Load a read-only ordered merge preview for one delivery group.
 * Never mutates GitHub.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import {
  previewDeliveryGroup,
  type DeliveryGroupPreview,
} from "@/api/workspace/deliveryGroups";
import { useWorkspaceContext } from "./useWorkspaceContext";

export interface UseDeliveryGroupPreviewReturn {
  preview: DeliveryGroupPreview | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  clear: () => void;
}

export function useDeliveryGroupPreview(
  groupId: string | null,
  enabled: boolean,
): UseDeliveryGroupPreviewReturn {
  const { workspaceId } = useWorkspaceContext();
  const [preview, setPreview] = useState<DeliveryGroupPreview | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const seqRef = useRef(0);

  const clear = useCallback(() => {
    seqRef.current++;
    setPreview(null);
    setError(null);
    setLoading(false);
  }, []);

  const refresh = useCallback(async () => {
    if (!groupId || !enabled) return;
    const seq = ++seqRef.current;
    setLoading(true);
    setError(null);
    try {
      const next = await previewDeliveryGroup(workspaceId, groupId);
      if (seq === seqRef.current) {
        setPreview(next);
      }
    } catch (err) {
      if (seq === seqRef.current) {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      if (seq === seqRef.current) setLoading(false);
    }
  }, [workspaceId, groupId, enabled]);

  useEffect(() => {
    if (!groupId || !enabled) {
      clear();
      return;
    }
    void refresh();
    return () => {
      seqRef.current++;
    };
  }, [groupId, enabled, refresh, clear]);

  return { preview, loading, error, refresh, clear };
}
