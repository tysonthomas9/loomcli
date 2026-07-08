import type { FileHistoryEntry } from "@/api/workspace";

export type HistoryTimelineNode =
  | {
      kind: "commit";
      key: string;
      entry: FileHistoryEntry;
    }
  | {
      kind: "save";
      key: string;
      entry: FileHistoryEntry;
    }
  | {
      kind: "save-cluster";
      key: string;
      entries: FileHistoryEntry[];
    };

function historyEntryKey(entry: FileHistoryEntry, index: number): string {
  return `${entry.kind}:${entry.id ?? entry.sha ?? entry.time}:${index}`;
}

function saveClusterKey(entries: FileHistoryEntry[], index: number): string {
  const first = entries[0];
  const last = entries[entries.length - 1];
  return `save-cluster:${first?.id ?? first?.time ?? index}:${last?.id ?? last?.time ?? index}:${index}`;
}

function flushSaveRun(
  nodes: HistoryTimelineNode[],
  saves: FileHistoryEntry[],
  runStartIndex: number,
): void {
  if (saves.length === 0) return;
  if (saves.length === 1) {
    nodes.push({
      kind: "save",
      key: historyEntryKey(saves[0]!, runStartIndex),
      entry: saves[0]!,
    });
    return;
  }
  nodes.push({
    kind: "save-cluster",
    key: saveClusterKey(saves, runStartIndex),
    entries: [...saves],
  });
}

export function buildHistoryTimeline(
  entries: FileHistoryEntry[],
): HistoryTimelineNode[] {
  const nodes: HistoryTimelineNode[] = [];
  let saveRun: FileHistoryEntry[] = [];
  let saveRunStart = 0;

  entries.forEach((entry, index) => {
    if (entry.kind === "save") {
      if (saveRun.length === 0) saveRunStart = index;
      saveRun.push(entry);
      return;
    }

    flushSaveRun(nodes, saveRun, saveRunStart);
    saveRun = [];
    nodes.push({
      kind: "commit",
      key: historyEntryKey(entry, index),
      entry,
    });
  });

  flushSaveRun(nodes, saveRun, saveRunStart);
  return nodes;
}

function parseTime(value: string): number | null {
  const time = new Date(value).getTime();
  return Number.isNaN(time) ? null : time;
}

export function saveClusterRangeLabel(entries: FileHistoryEntry[]): string {
  const times = entries
    .map((entry) => parseTime(entry.time))
    .filter((time): time is number => time !== null)
    .sort((a, b) => a - b);
  if (times.length === 0) return entries[0]?.time ?? "";

  const first = new Date(times[0]!);
  const last = new Date(times[times.length - 1]!);
  const formatter = new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
  const firstLabel = formatter.format(first);
  const lastLabel = formatter.format(last);
  return firstLabel === lastLabel ? firstLabel : `${firstLabel}-${lastLabel}`;
}
