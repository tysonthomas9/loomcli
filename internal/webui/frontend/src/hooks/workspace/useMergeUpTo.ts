import { useCallback, useEffect, useState } from "react";

import {
  gitMergePreview,
  gitMergeUpTo,
  type MergeStackView,
} from "@/api/workspace/git";

export type { MergeStackView };

function useMergePolling(
  view: MergeStackView | null,
  refresh: () => Promise<void>,
) {
  useEffect(() => {
    if (!view?.phase || view.phase === "done" || view.phase === "blocked")
      return;
    const timer = window.setInterval(() => {
      void refresh();
    }, 5000);
    return () => window.clearInterval(timer);
  }, [view, refresh]);
}

function confirmMerge(view: MergeStackView): boolean {
  const summary = view.layers
    .map((layer) => `${layer.change} ${layer.head}`)
    .join("\n");
  return window.confirm(
    `Merge through ${view.target}? Confirm these exact heads:\n${summary}`,
  );
}

export function useMergeUpToForm(workspaceId: string, agentName: string) {
  const [stackId, setStackId] = useState("");
  const [target, setTarget] = useState("");
  const [view, setView] = useState<MergeStackView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = useCallback(async () => {
    if (!stackId || !target) return;
    setBusy(true);
    try {
      setView(await gitMergePreview(workspaceId, agentName, stackId, target));
      setError("");
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  }, [workspaceId, agentName, stackId, target]);
  useMergePolling(view, refresh);
  const submit = async () => {
    if (!view || view.phase || !confirmMerge(view)) return;
    setBusy(true);
    try {
      setView(await gitMergeUpTo(workspaceId, agentName, view));
      setError("");
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  };
  return {
    stackId,
    target,
    view,
    error,
    busy,
    setStackId,
    setTarget,
    refresh,
    submit,
    clear: () => setView(null),
  };
}
