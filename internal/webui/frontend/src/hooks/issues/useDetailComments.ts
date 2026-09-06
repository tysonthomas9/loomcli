import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Comment } from "@/types";

/** Confirmed local writes remain an overlay until an authoritative read includes
 * them. This is ordinary UI state, not certified recovery coverage. */
export function useDetailComments(
  workspaceId: string,
  issueId: string | null,
  base: Comment[] | undefined,
) {
  const owner = useMemo(
    () => ({ workspaceId, issueId }),
    [workspaceId, issueId],
  );
  const committed = useRef<typeof owner | null>(null);
  const [overlay, setOverlay] = useState<{
    owner: typeof owner;
    rows: Comment[];
  }>({ owner, rows: [] });
  useLayoutEffect(() => {
    committed.current = owner;
    return () => {
      if (committed.current === owner) committed.current = null;
    };
  }, [owner]);
  // Retire only confirmed rows actually observed in an authoritative response.
  useLayoutEffect(() => {
    setOverlay((previous) => {
      if (previous.owner !== owner) return { owner, rows: [] };
      const rows = previous.rows.filter(
        (row) => !base?.some((item) => item.id === row.id),
      );
      return rows.length === previous.rows.length ? previous : { owner, rows };
    });
  }, [base, owner, overlay]);
  const comments = useMemo(() => {
    const rows = overlay.owner === owner ? overlay.rows : [];
    if (!base && rows.length === 0) return undefined;
    const byId = new Map((base ?? []).map((row) => [row.id, row]));
    for (const row of rows) if (!byId.has(row.id)) byId.set(row.id, row);
    return [...byId.values()];
  }, [base, overlay, owner]);
  const add = useCallback(
    (comment: Comment): boolean => {
      if (
        committed.current !== owner ||
        !issueId ||
        comment.issue_id !== issueId
      )
        return false;
      setOverlay((previous) => {
        if (committed.current !== owner) return previous;
        const rows = previous.owner === owner ? previous.rows : [];
        return rows.some((row) => row.id === comment.id)
          ? previous
          : { owner, rows: [...rows, comment] };
      });
      return true;
    },
    [owner, issueId],
  );
  return { comments, add };
}
