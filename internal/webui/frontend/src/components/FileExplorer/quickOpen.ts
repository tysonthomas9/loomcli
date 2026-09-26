import type { ExplorerRef } from "@/utils/explorerRefs";

export interface QuickOpenMatch {
  path: string;
  score: number;
  mruRank: number | null;
}

export interface QuickOpenItem {
  id: string;
  ref: ExplorerRef;
  path: string;
  checkoutLabel: string;
}

export interface QuickOpenItemMatch {
  item: QuickOpenItem;
  score: number;
  mruRank: number | null;
}

function basename(path: string): string {
  return path.split("/").pop() || path;
}

function subsequenceScore(candidate: string, query: string): number | null {
  const text = candidate.toLowerCase();
  const q = query.toLowerCase();
  let qi = 0;
  let score = 0;
  let streak = 0;
  let first = -1;

  for (let i = 0; i < text.length && qi < q.length; i += 1) {
    if (text[i] !== q[qi]) {
      streak = 0;
      continue;
    }
    if (first === -1) first = i;
    const boundary = i === 0 || "/_-.".includes(text[i - 1] ?? "");
    streak += 1;
    score += 12 + streak * 4 + (boundary ? 8 : 0);
    qi += 1;
  }

  if (qi !== q.length) return null;
  return (
    score + Math.max(0, 30 - first) + Math.max(0, 20 - candidate.length / 4)
  );
}

export function scoreQuickOpenPath(
  path: string,
  query: string,
  mruRank: number | null,
): QuickOpenMatch | null {
  const q = query.trim();
  const recencyScore = mruRank === null ? 0 : 2000 / (mruRank + 1);
  if (!q) {
    return { path, score: recencyScore, mruRank };
  }

  const nameScore = subsequenceScore(basename(path), q);
  const pathScore = subsequenceScore(path, q);
  const fuzzyScore =
    nameScore === null && pathScore === null
      ? null
      : Math.max(nameScore ?? 0, (pathScore ?? 0) * 0.75);
  if (fuzzyScore === null) return null;

  const recencyWeight = q.length <= 2 ? 1 : 0.2;
  return {
    path,
    score: fuzzyScore + recencyScore * recencyWeight,
    mruRank,
  };
}

export function rankQuickOpenItems(
  items: QuickOpenItem[],
  query: string,
  mruKeys: string[],
  limit = 80,
): QuickOpenItemMatch[] {
  const mruRanks = new Map<string, number>();
  mruKeys.forEach((key, index) => mruRanks.set(key, index));
  return items
    .map((item) => {
      const searchText = `${item.path} ${item.checkoutLabel}`;
      const match = scoreQuickOpenPath(
        searchText,
        query,
        mruRanks.get(item.id) ?? null,
      );
      return match
        ? { item, score: match.score, mruRank: match.mruRank }
        : null;
    })
    .filter((match): match is QuickOpenItemMatch => match !== null)
    .sort((a, b) => {
      if (b.score !== a.score) return b.score - a.score;
      if (a.mruRank !== null || b.mruRank !== null) {
        return (
          (a.mruRank ?? Number.POSITIVE_INFINITY) -
          (b.mruRank ?? Number.POSITIVE_INFINITY)
        );
      }
      return a.item.path.localeCompare(b.item.path);
    })
    .slice(0, limit);
}
