/**
 * Stacked PR workspace — delivery groups, standalone PRs, filters, Path/List,
 * history, selected-PR detail, and read-only ordered merge preview.
 *
 * Preserves navigation into existing PRReviewWorkspace via onOpenReview.
 * No merge execution, queue submission, retargeting, or simulated Undo.
 */

import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";

import type { GitPullRequest } from "@/api/workspace/pullRequests";
import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import type {
  GitHubViewerIdentity,
  PullRequestReadinessView,
  StandalonePRContinuation,
} from "@/api/workspace/pullRequests";
import { fetchPullRequestReadiness } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";
import { useDeliveryGroupPreview } from "@/hooks/workspace/useDeliveryGroupPreview";
import { useDeliveryGroupMembers } from "@/hooks/workspace/useDeliveryGroupMembers";
import { useRegisterEscapeLayer, LAYER_CONFIRM_DIALOG } from "@/hooks";
import { isPRUrl, prKeyFromRef } from "@/utils/issue";
import {
  buildHistoryEntries,
  buildLoomOnlyReviewItems,
  buildPrByKey,
  buildReadinessByKey,
  buildWorkspaceItems,
  canonicalRepoIdentity,
  computeTabCounts,
  epicOptionsFromItems,
  matchDeliveryGroup,
  matchStandalone,
  memberRepo,
  repoOptionsFromItems,
  statusKeyForItem,
  type QueueKind,
  type QueueMode,
  type QueueTab,
  type QueueViewMode,
  type StandalonePRItem,
  type WorkspaceItem,
} from "@/utils/pullRequest/stackedPrModel";
import {
  readinessDisplay,
  shortPrKey,
} from "@/utils/pullRequest/readinessDisplay";
import type { Issue } from "@/types";
import {
  checkCountLabel,
  relativeAge,
  repoBasename,
  toneForKey,
} from "@/utils/pullRequest/stackedPrPresentation";
import { useAuth } from "@/contexts/AuthContext";
import { useRouteChrome } from "@/contexts/RouteChromeContext";
import { Icon } from "./Icon";
import { MergePreviewDialog } from "./MergePreviewDialog";
import { PRRow, type PathNodeState } from "./PRRow";
import { ReadinessBadge } from "./ReadinessBadge";
import { SummaryPanel, type GroupContext } from "./SummaryPanel";
import { WorkspaceNav } from "./WorkspaceNav";
import { WorkspaceTopbar } from "./WorkspaceTopbar";
import styles from "./StackedPRWorkspace.module.css";

export interface StackedPRWorkspaceProps {
  issues: Issue[];
  pullRequests: GitPullRequest[];
  deliveryGroups: DeliveryGroupView[];
  deliveryGroupsHasMore?: boolean;
  standaloneContinuation?: StandalonePRContinuation;
  /** Verified GitHub viewer from usePullRequests — never Auth display name. */
  githubViewer?: GitHubViewerIdentity;
  warnings: string[];
  loading: boolean;
  error: Error | null;
  onOpenReview: (args: { issueId?: string; reviewPr?: string }) => void;
  onRefetch?: () => Promise<void>;
}

const TABS: { id: QueueTab; label: string }[] = [
  { id: "all", label: "All" },
  { id: "review", label: "Needs review" },
  { id: "ready", label: "Ready to merge" },
  { id: "attention", label: "Needs attention" },
  { id: "merged", label: "Merged" },
];

function toggleSet(
  value: string,
  setSelection: Dispatch<SetStateAction<Set<string>>>,
): void {
  setSelection((current) => {
    const next = new Set(current);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    return next;
  });
}

function prReviewRef(pr: GitPullRequest): string | null {
  return pr.repo_name && pr.number ? `${pr.repo_name}#${pr.number}` : null;
}

export function StackedPRWorkspace({
  issues,
  pullRequests,
  deliveryGroups,
  deliveryGroupsHasMore = false,
  standaloneContinuation,
  githubViewer,
  warnings,
  loading,
  error,
  onOpenReview,
  onRefetch,
}: StackedPRWorkspaceProps): JSX.Element {
  const { workspaceId, workspace } = useWorkspaceContext();
  const { user } = useAuth();
  // /prs owns the app chrome: one 212px nav, one 59px breadcrumb.
  const chrome = useRouteChrome();
  const searchId = useId();
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  /** Set by j/k so only keyboard moves scroll the selection into view. */
  const revealSelectionRef = useRef(false);

  const [tab, setTab] = useState<QueueTab>("all");
  const [mode, setMode] = useState<QueueMode>("queue");
  const [view, setView] = useState<QueueViewMode>("path");
  const [query, setQuery] = useState("");
  /** User expand/collapse choices per delivery group id. */
  const [expandedOverride, setExpandedOverride] = useState<
    Map<string, boolean>
  >(() => new Map());
  const [guideOpen, setGuideOpen] = useState(false);
  const [selectedRepos, setSelectedRepos] = useState<Set<string>>(new Set());
  const [selectedEpics, setSelectedEpics] = useState<Set<string>>(new Set());
  const [kinds, setKinds] = useState<Set<QueueKind>>(
    () => new Set(["group", "standalone"]),
  );
  const [mine, setMine] = useState(false);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [detailOpen, setDetailOpen] = useState(true);
  const [mobileDetail, setMobileDetail] = useState(false);
  const [previewGroupId, setPreviewGroupId] = useState<string | null>(null);
  const [extraReadiness, setExtraReadiness] = useState<
    Map<string, PullRequestReadinessView>
  >(() => new Map());
  const [writeBanner, setWriteBanner] = useState<string | null>(null);
  const [helpOpen, setHelpOpen] = useState(false);

  const membershipComplete = standaloneContinuation?.complete !== false;

  const issueByPrKey = useMemo(() => {
    const map = new Map<
      string,
      {
        id: string;
        title?: string;
        status?: string;
        assignee?: string;
        epicId?: string;
        epicTitle?: string;
        prKey?: string | null;
        inReviewQueue?: boolean;
      }
    >();
    for (const issue of issues) {
      const key = prKeyFromRef(issue.external_ref);
      const inReviewQueue =
        issue.status === "review" || isPRUrl(issue.external_ref);
      if (key) {
        map.set(key, {
          id: issue.id,
          title: issue.title,
          ...(issue.status ? { status: issue.status } : {}),
          ...(issue.assignee ? { assignee: issue.assignee } : {}),
          ...(issue.parent ? { epicId: issue.parent } : {}),
          ...(issue.parent_title ? { epicTitle: issue.parent_title } : {}),
          prKey: key,
          inReviewQueue,
        });
      }
    }
    return map;
  }, [issues]);

  const loomOnlyMeta = useMemo(() => {
    return issues
      .filter(
        (issue) =>
          (issue.status === "review" || isPRUrl(issue.external_ref)) &&
          !prKeyFromRef(issue.external_ref),
      )
      .map((issue) => ({
        id: issue.id,
        title: issue.title,
        ...(issue.status ? { status: issue.status } : {}),
        ...(issue.assignee ? { assignee: issue.assignee } : {}),
        ...(issue.parent ? { epicId: issue.parent } : {}),
        ...(issue.parent_title ? { epicTitle: issue.parent_title } : {}),
        prKey: null as string | null,
        inReviewQueue: true,
      }));
  }, [issues]);

  const groupReadiness = useMemo(
    () => buildReadinessByKey(deliveryGroups, [...extraReadiness.values()]),
    [deliveryGroups, extraReadiness],
  );

  const items = useMemo(() => {
    const base = buildWorkspaceItems({
      deliveryGroups,
      pullRequests,
      issueByPrKey,
      membershipComplete,
      readinessByKey: groupReadiness,
    });
    const covered = new Set(
      base
        .filter((i): i is StandalonePRItem => i.kind === "standalone")
        .map((i) => i.prKey),
    );
    // Also cover PR keys that are only in groups.
    for (const g of deliveryGroups) {
      for (const m of g.members) covered.add(m.pr_key);
    }
    const loomOnly = buildLoomOnlyReviewItems(loomOnlyMeta, covered);
    return [...base, ...loomOnly];
  }, [
    deliveryGroups,
    pullRequests,
    issueByPrKey,
    membershipComplete,
    groupReadiness,
    loomOnlyMeta,
  ]);

  const prByKey = useMemo(() => {
    const map = buildPrByKey(pullRequests);
    for (const g of deliveryGroups) {
      for (const m of g.members) {
        if (map.has(m.pr_key)) continue;
        // Stub for grouped PRs absent from the standalone list.
        map.set(m.pr_key, {
          number: m.pr_number,
          pr_key: m.pr_key,
          title: shortPrKey(m.pr_key),
          url: `https://github.com/${shortPrKey(m.pr_key).replace("#", "/pull/")}`,
          state: "OPEN",
          is_draft: false,
          head_ref_name: "",
          base_ref_name: "",
          repo_name: m.repo_name.includes("/")
            ? m.repo_name
            : `unknown/${m.repo_name}`,
          source_repo: memberRepo(m),
        });
      }
    }
    return map;
  }, [pullRequests, deliveryGroups]);

  // GitHub author Mine uses verified viewer.login only. Loom owner/assignee
  // Mine uses email (preferred) or user id — never Better Auth display name.
  const githubLogin =
    githubViewer?.status === "available"
      ? githubViewer.login?.trim() || null
      : null;
  const loomActor = user?.email?.trim() || user?.id?.trim() || null;
  const githubIdentityMissing =
    githubViewer != null && githubViewer.status !== "available";

  const filters = useMemo(
    () => ({
      tab,
      query,
      repos: selectedRepos,
      epics: selectedEpics,
      kinds,
      mine,
      githubLogin,
      loomActor,
    }),
    [
      tab,
      query,
      selectedRepos,
      selectedEpics,
      kinds,
      mine,
      githubLogin,
      loomActor,
    ],
  );

  const tabCounts = useMemo(
    () =>
      computeTabCounts(
        items,
        {
          query,
          repos: selectedRepos,
          epics: selectedEpics,
          kinds,
          mine,
          githubLogin,
          loomActor,
        },
        groupReadiness,
        prByKey,
      ),
    [
      items,
      query,
      selectedRepos,
      selectedEpics,
      kinds,
      mine,
      githubLogin,
      loomActor,
      groupReadiness,
      prByKey,
    ],
  );

  const visible = useMemo(() => {
    const out: Array<{
      item: WorkspaceItem;
      dimmed: boolean;
      memberDimmed: ReadonlyMap<string, boolean>;
    }> = [];
    for (const item of items) {
      if (item.kind === "standalone") {
        const m = matchStandalone(item, filters, groupReadiness);
        if (m.matches) {
          out.push({ item, dimmed: m.dimmed, memberDimmed: new Map() });
        }
      } else {
        const m = matchDeliveryGroup(item, filters, groupReadiness, prByKey);
        if (m.matches) {
          out.push({ item, dimmed: m.dimmed, memberDimmed: m.memberDimmed });
        }
      }
    }
    return out;
  }, [items, filters, groupReadiness, prByKey]);

  const flatKeys = useMemo(() => {
    const keys: string[] = [];
    for (const { item } of visible) {
      if (item.kind === "standalone") keys.push(item.prKey);
      else {
        for (const m of item.group.members) keys.push(m.pr_key);
      }
    }
    return keys;
  }, [visible]);

  const selectedStandalone = useMemo((): StandalonePRItem | null => {
    if (!selectedKey) return null;
    for (const { item } of visible) {
      if (item.kind === "standalone" && item.prKey === selectedKey) return item;
    }
    for (const item of items) {
      if (item.kind === "standalone" && item.prKey === selectedKey) return item;
    }
    return null;
  }, [selectedKey, visible, items]);

  const selectedGroupMember = useMemo(() => {
    if (!selectedKey) return null;
    for (const item of items) {
      if (item.kind !== "group") continue;
      const idx = item.group.members.findIndex((m) => m.pr_key === selectedKey);
      if (idx >= 0) {
        return {
          group: item.group,
          member: item.group.members[idx]!,
          index: idx,
          prev: idx > 0 ? item.group.members[idx - 1] : undefined,
        };
      }
    }
    return null;
  }, [selectedKey, items]);

  const history = useMemo(() => {
    const standalone = items.filter(
      (i): i is StandalonePRItem => i.kind === "standalone",
    );
    return buildHistoryEntries({ deliveryGroups, standalone }).filter((h) => {
      if (!query.trim()) return true;
      return h.searchText.toLowerCase().includes(query.trim().toLowerCase());
    });
  }, [deliveryGroups, items, query]);

  const repoOptions = useMemo(
    () => repoOptionsFromItems(items, prByKey),
    [items, prByKey],
  );
  const epicOptions = useMemo(() => epicOptionsFromItems(items), [items]);

  const githubWarning = error
    ? `GitHub metadata unavailable: ${error.message}`
    : warnings.length > 0
      ? `${warnings[0]}${warnings.length > 1 ? ` (+${warnings.length - 1} more)` : ""}`
      : null;

  const openCount = useMemo(
    () =>
      items.filter((i) => {
        if (i.kind === "standalone") {
          return i.pr.state === "OPEN" && !i.pr.is_draft;
        }
        return i.group.members.some((m) => {
          const pr = prByKey.get(m.pr_key);
          return !pr || (pr.state === "OPEN" && !pr.is_draft);
        });
      }).length,
    [items, prByKey],
  );

  const groupCount = items.filter((i) => i.kind === "group").length;
  const standaloneCount = items.filter((i) => i.kind === "standalone").length;
  const notCurrentCount = useMemo(() => {
    let n = 0;
    for (const item of items) {
      const key = statusKeyForItem(item, groupReadiness);
      if (key === "stale" || key === "unknown") n += 1;
    }
    return n;
  }, [items, groupReadiness]);

  const preview = useDeliveryGroupPreview(
    previewGroupId,
    Boolean(previewGroupId),
  );
  const membersApi = useDeliveryGroupMembers();

  useRegisterEscapeLayer(
    LAYER_CONFIRM_DIALOG,
    () => setHelpOpen(false),
    helpOpen && !previewGroupId,
  );

  useRegisterEscapeLayer(
    LAYER_CONFIRM_DIALOG,
    () => setGuideOpen(false),
    guideOpen && !previewGroupId,
  );

  useRegisterEscapeLayer(
    LAYER_CONFIRM_DIALOG,
    () => {
      setMobileDetail(false);
    },
    mobileDetail && !previewGroupId && !helpOpen && !guideOpen,
  );

  // Select first visible PR when none selected.
  useEffect(() => {
    if (selectedKey && flatKeys.includes(selectedKey)) return;
    if (flatKeys[0]) setSelectedKey(flatKeys[0]);
  }, [flatKeys, selectedKey]);

  const loadReadinessFor = useCallback(
    async (prKeys: string[], force = false) => {
      if (prKeys.length === 0) return;
      try {
        const list = await fetchPullRequestReadiness(workspaceId, prKeys, {
          force,
        });
        setExtraReadiness((prev) => {
          const next = new Map(prev);
          for (const row of list.pull_requests) {
            next.set(row.pr_key, row);
          }
          return next;
        });
      } catch {
        // Keep prior evidence; banner via githubWarning path on list errors.
      }
    },
    [workspaceId],
  );

  useEffect(() => {
    if (!selectedKey) return;
    void loadReadinessFor([selectedKey]);
  }, [selectedKey, loadReadinessFor]);

  // Keyboard moves keep the selected row visible inside the list scroller.
  // Initial auto-selection and clicks never scroll, so the title and
  // toolbar stay in view on load.
  useEffect(() => {
    if (!selectedKey || !revealSelectionRef.current) return;
    revealSelectionRef.current = false;
    const row = listRef.current?.querySelector<HTMLElement>(
      `[data-testid="pr-row-${CSS.escape(selectedKey)}"]`,
    );
    row?.scrollIntoView({ block: "nearest" });
  }, [selectedKey]);

  // Keyboard/selection moving into a collapsed group re-expands it.
  useEffect(() => {
    if (!selectedKey) return;
    const owner = deliveryGroups.find((g) =>
      g.members.some((m) => m.pr_key === selectedKey),
    );
    if (!owner || expandedOverride.get(owner.id) !== false) return;
    setExpandedOverride((prev) => {
      const next = new Map(prev);
      next.delete(owner.id);
      return next;
    });
    // Only react to selection changes, not to manual collapse.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedKey]);

  const selectKey = useCallback((key: string, mobile = false) => {
    setSelectedKey(key);
    if (mobile) setMobileDetail(true);
  }, []);

  const openReviewForKey = useCallback(
    (key: string) => {
      const standalone = items.find(
        (i): i is StandalonePRItem =>
          i.kind === "standalone" && i.prKey === key,
      );
      if (standalone?.issueId) {
        onOpenReview({ issueId: standalone.issueId });
        return;
      }
      if (standalone) {
        const ref = prReviewRef(standalone.pr);
        if (ref) onOpenReview({ reviewPr: ref });
        return;
      }
      const member =
        selectedGroupMember?.member.pr_key === key
          ? selectedGroupMember
          : (() => {
              for (const item of items) {
                if (item.kind !== "group") continue;
                const idx = item.group.members.findIndex(
                  (m) => m.pr_key === key,
                );
                if (idx >= 0) {
                  return {
                    group: item.group,
                    member: item.group.members[idx]!,
                    index: idx,
                  };
                }
              }
              return null;
            })();
      if (member) {
        const link = issueByPrKey.get(key);
        if (link?.id) {
          onOpenReview({ issueId: link.id });
          return;
        }
        const short = shortPrKey(key);
        onOpenReview({ reviewPr: short });
      }
    },
    [items, onOpenReview, selectedGroupMember, issueByPrKey],
  );

  const handleKeyDown = useCallback(
    (event: KeyboardEvent) => {
      // Never steal browser/OS chords (Cmd+V, Ctrl+H, ...) or keys another
      // handler already consumed.
      if (
        event.defaultPrevented ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey
      ) {
        return;
      }
      const target = event.target as HTMLElement | null;
      const tag = target?.tagName;
      // Enter/Space on a focused control activate that control, not the
      // selected PR's review.
      if (
        (event.key === "Enter" || event.key === " ") &&
        target?.closest(
          "button, a[href], summary, [role='button'], [role='tab'], [role='link'], [role='menuitem']",
        )
      ) {
        return;
      }
      if (
        tag === "INPUT" ||
        tag === "TEXTAREA" ||
        tag === "SELECT" ||
        target?.isContentEditable
      ) {
        if (event.key === "Escape") {
          (target as HTMLElement).blur();
        }
        return;
      }
      if (previewGroupId || helpOpen || guideOpen) return;

      const k = event.key;
      if (k === "/" || k === "s") {
        event.preventDefault();
        searchRef.current?.focus();
        return;
      }
      if (k === "?" || (k === "/" && event.shiftKey)) {
        event.preventDefault();
        setHelpOpen(true);
        return;
      }
      if (k === "h") {
        event.preventDefault();
        setMode((m) => (m === "queue" ? "history" : "queue"));
        return;
      }
      if (k === "v") {
        event.preventDefault();
        setView((v) => (v === "path" ? "list" : "path"));
        return;
      }
      if (k === "m") {
        event.preventDefault();
        const gid =
          selectedGroupMember?.group.id ??
          (selectedStandalone
            ? null
            : items.find(
                  (i) =>
                    i.kind === "group" &&
                    i.group.members.some((m) => m.pr_key === selectedKey),
                )?.kind === "group"
              ? (
                  items.find(
                    (i) =>
                      i.kind === "group" &&
                      i.group.members.some((m) => m.pr_key === selectedKey),
                  ) as { group: DeliveryGroupView } | undefined
                )?.group.id
              : null);
        if (gid) setPreviewGroupId(gid);
        return;
      }
      if (k === "o" || k === "Enter") {
        if (selectedKey) {
          event.preventDefault();
          openReviewForKey(selectedKey);
        }
        return;
      }
      if (k === "j" || k === "ArrowDown") {
        event.preventDefault();
        const idx = selectedKey ? flatKeys.indexOf(selectedKey) : -1;
        const next = flatKeys[Math.min(flatKeys.length - 1, idx + 1)];
        if (next) {
          revealSelectionRef.current = true;
          setSelectedKey(next);
        }
        return;
      }
      if (k === "k" || k === "ArrowUp") {
        event.preventDefault();
        const idx = selectedKey
          ? flatKeys.indexOf(selectedKey)
          : flatKeys.length;
        const next = flatKeys[Math.max(0, idx - 1)];
        if (next) {
          revealSelectionRef.current = true;
          setSelectedKey(next);
        }
      }
    },
    [
      previewGroupId,
      helpOpen,
      guideOpen,
      selectedKey,
      flatKeys,
      openReviewForKey,
      selectedGroupMember,
      selectedStandalone,
      items,
    ],
  );

  useEffect(() => {
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [handleKeyDown]);

  const removeMember = async (
    group: DeliveryGroupView,
    prKey: string,
  ): Promise<void> => {
    setWriteBanner(null);
    const next = group.members
      .filter((m) => m.pr_key !== prKey)
      .map((m) => ({
        pr_key: m.pr_key,
        repo_name: m.repo_name,
        pr_number: m.pr_number,
        ...(m.github_node_id ? { github_node_id: m.github_node_id } : {}),
        source: m.source,
        ...(m.task_id ? { task_id: m.task_id } : {}),
        ...(m.lineage_stack_id ? { lineage_stack_id: m.lineage_stack_id } : {}),
      }));
    const { group: result, error: err } = await membersApi.setMembers(
      group.id,
      group.revision,
      next,
    );
    if (err) {
      setWriteBanner(
        err.kind === "stale_revision"
          ? `Stale revision — reload and retry. ${err.message}`
          : err.kind === "duplicate_membership"
            ? `Duplicate membership: ${err.message}`
            : `Write outcome unknown or failed (${err.kind}): ${err.message}. Persisted members were not erased.`,
      );
    }
    if (result || err?.group) {
      await onRefetch?.();
    }
  };

  const addStandaloneToGroup = async (
    group: DeliveryGroupView,
    item: StandalonePRItem,
  ): Promise<void> => {
    setWriteBanner(null);
    const next = [
      ...group.members.map((m) => ({
        pr_key: m.pr_key,
        repo_name: m.repo_name,
        pr_number: m.pr_number,
        ...(m.github_node_id ? { github_node_id: m.github_node_id } : {}),
        source: m.source,
        ...(m.task_id ? { task_id: m.task_id } : {}),
        ...(m.lineage_stack_id ? { lineage_stack_id: m.lineage_stack_id } : {}),
      })),
      {
        pr_key: item.prKey,
        repo_name: item.pr.repo_name,
        pr_number: item.pr.number,
        ...(item.pr.node_id ? { github_node_id: item.pr.node_id } : {}),
        source: "manual" as const,
        ...(item.issueId ? { task_id: item.issueId } : {}),
      },
    ];
    const { group: result, error: err } = await membersApi.setMembers(
      group.id,
      group.revision,
      next,
    );
    if (err) {
      setWriteBanner(
        err.kind === "stale_revision"
          ? `Stale revision — reload and retry. ${err.message}`
          : err.kind === "duplicate_membership"
            ? `Duplicate membership: ${err.message}`
            : `Write outcome unknown or failed (${err.kind}): ${err.message}. Persisted members were not erased.`,
      );
    }
    if (result || err?.group) {
      await onRefetch?.();
    }
  };

  const readinessFor = (prKey: string) =>
    groupReadiness.get(prKey) ?? extraReadiness.get(prKey);

  // Grouped PRs are excluded from the standalone list, so prByKey holds only
  // a stub for them. Fall back to the linked Loom task title and the
  // readiness snapshot's head ref — both real data — before the bare key.
  const realPrByKey = buildPrByKey(pullRequests);
  const titleFor = (prKey: string): string =>
    realPrByKey.get(prKey)?.title ??
    issueByPrKey.get(prKey)?.title ??
    shortPrKey(prKey);
  const branchFor = (prKey: string): string | null =>
    realPrByKey.get(prKey)?.head_ref_name ||
    readinessFor(prKey)?.snapshot?.head_ref ||
    null;

  const numberLabelFor = (prKey: string, pr: GitPullRequest | undefined) =>
    pr && pr.number > 0 ? `#${pr.number}` : shortPrKey(prKey);

  const repoFor = (pr: GitPullRequest | undefined): string =>
    canonicalRepoIdentity({
      repo_name: pr?.repo_name,
      source_repo: pr?.source_repo,
    });

  const isNarrow = () =>
    typeof window.matchMedia === "function" &&
    window.matchMedia("(max-width: 850px)").matches;

  const isGroupExpanded = (g: DeliveryGroupView, index: number): boolean => {
    const override = expandedOverride.get(g.id);
    if (override != null) return override;
    return index === 0 || g.members.some((m) => m.pr_key === selectedKey);
  };

  const toggleGroup = (g: DeliveryGroupView, expanded: boolean) => {
    setExpandedOverride((prev) => {
      const next = new Map(prev);
      next.set(g.id, !expanded);
      return next;
    });
  };

  const memberDisplay = (prKey: string) => {
    const pr = prByKey.get(prKey);
    const d = readinessDisplay(readinessFor(prKey));
    if (pr?.state === "MERGED" && d.key !== "merged") {
      return { ...d, key: "merged" as const, label: "Merged" };
    }
    return d;
  };

  const renderGroupCard = (
    g: DeliveryGroupView,
    index: number,
    memberDimmed: ReadonlyMap<string, boolean>,
  ): JSX.Element => {
    const expanded = isGroupExpanded(g, index);
    const displays = g.members.map((m) => memberDisplay(m.pr_key));
    const mergedCount = displays.filter((d) => d.key === "merged").length;
    const readyCount = displays.filter((d) => d.key === "ready").length;
    const blockedCount = displays.filter((d) => d.key === "blocked").length;
    const notCurrent = displays.filter(
      (d) => d.key === "stale" || d.key === "unknown",
    ).length;
    const repos = [...new Set(g.members.map((m) => memberRepo(m)))];
    const nextIdx = displays.findIndex((d) => d.key !== "merged");
    const nextMember = nextIdx >= 0 ? g.members[nextIdx] : undefined;
    const epicTitle =
      g.members
        .map((m) => issueByPrKey.get(m.pr_key)?.epicTitle)
        .find(Boolean) ?? g.epic_id;
    const updated = relativeAge(g.updated_at);
    const status =
      g.members.length === 0
        ? { text: "No members", tone: "bad" }
        : mergedCount === g.members.length
          ? { text: "All merged", tone: "good" }
          : readyCount > 0
            ? { text: `${readyCount} ready to merge`, tone: "good" }
            : blockedCount > 0
              ? { text: `${blockedCount} blocked`, tone: "bad" }
              : notCurrent > 0
                ? { text: "Evidence not current", tone: "bad" }
                : { text: "Waiting for review", tone: "" };
    const bodyId = `dg-body-${g.id}`;
    return (
      <article
        key={g.id}
        className={styles.stackCard}
        data-expanded={expanded || undefined}
        data-testid={`delivery-group-${g.id}`}
      >
        <button
          type="button"
          className={styles.stackHeader}
          aria-expanded={expanded}
          aria-controls={bodyId}
          onClick={() => toggleGroup(g, expanded)}
        >
          <span className={styles.stackIcon} data-tone={index % 4}>
            <Icon name="stack" />
          </span>
          <span className={styles.stackHeadingCopy}>
            <span className={styles.stackTitle}>{g.title}</span>
            <span className={styles.stackCaption}>
              <span>
                {g.members.length} PR{g.members.length === 1 ? "" : "s"}
              </span>
              <i aria-hidden="true" />
              <span>
                {repos.length} repo{repos.length === 1 ? "" : "s"}
              </span>
              {epicTitle ? (
                <>
                  <i aria-hidden="true" />
                  <span className={styles.captionEpic}>{epicTitle}</span>
                </>
              ) : null}
              {updated ? (
                <>
                  <i aria-hidden="true" />
                  <span>{updated}</span>
                </>
              ) : null}
            </span>
          </span>
          <span className={styles.stackHeadRight}>
            <span>
              <span className={styles.stackProgress} aria-hidden="true">
                {displays.map((d, i) => (
                  <i key={i} data-tone={toneForKey(d.key)} />
                ))}
              </span>
              <span className={styles.stackMiniStatus} data-tone={status.tone}>
                {status.text}
              </span>
            </span>
            <Icon name="chevron" className={styles.chevron} />
          </span>
        </button>
        {expanded ? (
          <div className={styles.stackBody} id={bodyId}>
            <div className={styles.pathCaption}>
              {repos.map((r) => (
                <span key={r} className={styles.targetPill} title={r}>
                  <Icon name="repo" />
                  {repoBasename(r)}
                </span>
              ))}
              <span className={styles.metaChip}>rev {g.revision}</span>
              {g.inconsistent ? (
                <span className={styles.warnChip}>inconsistent</span>
              ) : null}
              <span className={styles.order}>
                Merge order <Icon name="arrowDown" />
              </span>
            </div>
            <ol className={styles.prPath} aria-label={`${g.title} path`}>
              {g.members.map((m, i) => {
                const pr = prByKey.get(m.pr_key);
                const d = displays[i]!;
                const prev = i > 0 ? g.members[i - 1] : undefined;
                const prevMerged = i > 0 && displays[i - 1]!.key === "merged";
                const cross =
                  prev && memberRepo(prev) !== memberRepo(m)
                    ? `Delivery order crosses ${repoBasename(memberRepo(prev))} → ${repoBasename(memberRepo(m))} (not a branch link)`
                    : undefined;
                const nodeState: PathNodeState =
                  d.key === "merged"
                    ? "merged"
                    : i === nextIdx && d.key === "ready"
                      ? "next"
                      : "pending";
                const checks = checkCountLabel(readinessFor(m.pr_key));
                const meta = (
                  <>
                    {checks && d.key !== "merged" ? (
                      <span
                        className={styles.checks}
                        data-current={d.freshness === "fresh" || undefined}
                        title="Required checks passed (last observation)"
                      >
                        <Icon name="checkCircle" /> {checks}
                      </span>
                    ) : null}
                    {prev && !prevMerged && d.key !== "merged" ? (
                      <span>Needs {shortPrKey(prev.pr_key)} first</span>
                    ) : pr?.updated_at ? (
                      <span>{relativeAge(pr.updated_at)}</span>
                    ) : null}
                  </>
                );
                return (
                  <PRRow
                    key={m.pr_key}
                    testKey={shortPrKey(m.pr_key)}
                    numberLabel={numberLabelFor(m.pr_key, pr)}
                    title={titleFor(m.pr_key)}
                    repo={repoBasename(memberRepo(m))}
                    branch={branchFor(m.pr_key)}
                    display={d}
                    authorLogin={pr?.author_login}
                    meta={meta}
                    selected={selectedKey === m.pr_key}
                    dimmed={memberDimmed.get(m.pr_key) === true}
                    step={i + 1}
                    nodeState={nodeState}
                    crossNote={cross}
                    onActivate={() => selectKey(m.pr_key, isNarrow())}
                  />
                );
              })}
            </ol>
            <footer className={styles.stackFooter}>
              <Icon name="spark" />
              <span>
                {readyCount} PR{readyCount === 1 ? "" : "s"} can land now.{" "}
                {g.members.length - mergedCount - readyCount} stay in progress.
              </span>
              <button
                type="button"
                className={styles.textLink}
                onClick={() => setPreviewGroupId(g.id)}
              >
                View merge plan <Icon name="arrowRight" />
              </button>
            </footer>
          </div>
        ) : (
          <div className={styles.collapsedPreview}>
            <Icon name="branch" />
            <span>
              {mergedCount} merged
              {nextMember
                ? ` · Next up ${numberLabelFor(nextMember.pr_key, prByKey.get(nextMember.pr_key))}`
                : ""}
            </span>
            {nextMember ? (
              <ReadinessBadge display={displays[nextIdx]!} />
            ) : null}
          </div>
        )}
      </article>
    );
  };

  const renderStandaloneRow = (item: StandalonePRItem): JSX.Element => {
    const isLocal = item.prKey.startsWith("loom:");
    const d =
      item.pr.state === "MERGED"
        ? {
            ...readinessDisplay(readinessFor(item.prKey)),
            key: "merged" as const,
            label: "Merged",
          }
        : readinessDisplay(readinessFor(item.prKey));
    return (
      <PRRow
        key={item.prKey}
        testKey={shortPrKey(item.prKey)}
        numberLabel={numberLabelFor(item.prKey, item.pr)}
        title={item.pr.title}
        repo={isLocal ? "Loom task" : repoBasename(repoFor(item.pr))}
        branch={isLocal ? "no GitHub PR yet" : item.pr.head_ref_name || null}
        display={d}
        authorLogin={item.pr.author_login}
        meta={
          item.membershipUnverified ? (
            <span className={styles.warnText}>membership unverified</span>
          ) : item.pr.updated_at ? (
            <span>{relativeAge(item.pr.updated_at)}</span>
          ) : null
        }
        selected={selectedKey === item.prKey}
        step={null}
        onActivate={() => selectKey(item.prKey, isNarrow())}
      />
    );
  };

  const renderPathView = (): JSX.Element => {
    const groups = visible.filter((v) => v.item.kind === "group");
    const solos = visible.filter((v) => v.item.kind === "standalone");
    return (
      <div className={styles.stackList} data-testid="stacked-pr-path">
        {kinds.has("group") &&
          (groups.length === 0 ? (
            <div className={styles.emptyState}>
              <Icon name="stack" />
              <h3>No delivery groups match</h3>
              <p>
                Delivery groups are created explicitly in Loom — never inferred
                from branches or labels.
              </p>
            </div>
          ) : (
            groups.map(({ item, memberDimmed }, i) =>
              item.kind === "group"
                ? renderGroupCard(item.group, i, memberDimmed)
                : null,
            )
          ))}
        {kinds.has("standalone") && (
          <section aria-label="Standalone pull requests">
            <h2 className={styles.flatHeading}>
              <Icon name="pr" />
              Standalone PRs
              <span className={styles.hint}>
                not in any delivery group · includes PRs opened outside Loom
              </span>
            </h2>
            {!membershipComplete && (
              <p className={styles.warnBanner} role="status">
                <Icon name="warning" />
                Active-group membership is incomplete or unverified. Rows below
                are not confirmed standalone.
              </p>
            )}
            {solos.length === 0 ? (
              <div className={styles.emptySmall}>No standalone PRs match.</div>
            ) : (
              <ul className={styles.flatList}>
                {solos.map(({ item }) =>
                  item.kind === "standalone" ? renderStandaloneRow(item) : null,
                )}
              </ul>
            )}
          </section>
        )}
        <p className={styles.listHint}>
          <Icon name="keyboard" />
          <kbd className={styles.kbd}>J</kbd>
          <kbd className={styles.kbd}>K</kbd> to move between PRs ·{" "}
          <kbd className={styles.kbd}>/</kbd> to search
        </p>
      </div>
    );
  };

  const renderListView = (): JSX.Element => (
    <div className={styles.listWrap} data-testid="stacked-pr-list">
      <table className={styles.listTable}>
        <thead>
          <tr>
            <th scope="col">PR</th>
            <th scope="col">Title</th>
            <th scope="col">Repo</th>
            <th scope="col">Status</th>
            <th scope="col">Kind</th>
          </tr>
        </thead>
        <tbody>
          {visible.flatMap(({ item, memberDimmed }) => {
            if (item.kind === "standalone") {
              const d = readinessDisplay(readinessFor(item.prKey));
              return [
                <tr
                  key={item.prKey}
                  data-current={selectedKey === item.prKey || undefined}
                  onClick={() => selectKey(item.prKey)}
                >
                  <td>
                    <button type="button" className={styles.linkish}>
                      {shortPrKey(item.prKey)}
                    </button>
                  </td>
                  <td>{item.pr.title}</td>
                  <td>{repoFor(item.pr)}</td>
                  <td>
                    <ReadinessBadge display={d} compact />
                  </td>
                  <td>
                    {item.membershipUnverified ? "Unverified" : "Standalone"}
                  </td>
                </tr>,
              ];
            }
            return item.group.members.map((m, i) => {
              const d = memberDisplay(m.pr_key);
              return (
                <tr
                  key={m.pr_key}
                  data-current={selectedKey === m.pr_key || undefined}
                  data-dimmed={memberDimmed.get(m.pr_key) || undefined}
                  onClick={() => selectKey(m.pr_key)}
                >
                  <td>
                    <button type="button" className={styles.linkish}>
                      {i + 1}. {shortPrKey(m.pr_key)}
                    </button>
                  </td>
                  <td>{titleFor(m.pr_key)}</td>
                  <td>{memberRepo(m)}</td>
                  <td>
                    <ReadinessBadge display={d} compact />
                  </td>
                  <td>Group · {item.group.title}</td>
                </tr>
              );
            });
          })}
        </tbody>
      </table>
    </div>
  );

  const renderHistory = (): JSX.Element => (
    <div className={styles.stackList} data-testid="stacked-pr-history">
      <p className={styles.historyCaption}>
        <Icon name="history" />
        Derived from durable group fields and merged standalone PRs — not a full
        audit log.
      </p>
      {history.length === 0 ? (
        <div className={styles.emptyState}>
          <Icon name="history" />
          <h3>No merge history yet</h3>
          <p>Merged standalone PRs and group changes appear here.</p>
        </div>
      ) : (
        <ol className={styles.historyList}>
          {history.map((h) => (
            <li key={h.id} className={styles.historyCard}>
              <span className={styles.historyMark} aria-hidden="true">
                <Icon name="merge" />
              </span>
              <span className={styles.historyCopy}>
                <span className={styles.historyTitle}>{h.text}</span>
                <span className={styles.historyMeta}>
                  <time dateTime={h.at}>
                    {new Date(h.at)
                      .toISOString()
                      .slice(0, 16)
                      .replace("T", " ")}{" "}
                    UTC
                  </time>{" "}
                  · {h.source}
                </span>
              </span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );

  const selectedGroupContext = ((): GroupContext | null => {
    if (!selectedGroupMember) return null;
    const { group, index } = selectedGroupMember;
    const prev = index > 0 ? group.members[index - 1] : undefined;
    const next = group.members[index + 1];
    const member = group.members[index]!;
    return {
      group,
      index,
      prevKey: prev?.pr_key,
      prevMerged: prev ? memberDisplay(prev.pr_key).key === "merged" : false,
      prevCrossRepo: prev ? memberRepo(prev) !== memberRepo(member) : false,
      nextKey: next?.pr_key,
      nextRepo: next ? memberRepo(next) : undefined,
    };
  })();

  const renderDetail = (): JSX.Element => {
    if (!selectedKey) {
      return (
        <aside className={styles.detail} aria-label="Selected PR">
          <div className={styles.detailEmpty}>
            <Icon name="pr" />
            <p>Select a pull request to see its path to main.</p>
          </div>
        </aside>
      );
    }
    const pr = selectedStandalone?.pr ?? realPrByKey.get(selectedKey) ?? null;
    const isLocalOnly = selectedKey.startsWith("loom:");
    const view = readinessFor(selectedKey);
    const display = selectedGroupMember
      ? memberDisplay(selectedKey)
      : readinessDisplay(view);
    return (
      <SummaryPanel
        prKey={selectedKey}
        pr={pr}
        title={selectedStandalone?.pr.title ?? titleFor(selectedKey)}
        repo={
          isLocalOnly
            ? "Loom task"
            : selectedGroupMember
              ? memberRepo(selectedGroupMember.member)
              : repoFor(pr ?? undefined)
        }
        display={display}
        view={view}
        issueId={
          selectedStandalone?.issueId ?? issueByPrKey.get(selectedKey)?.id
        }
        isLocalOnly={isLocalOnly}
        groupContext={selectedGroupContext}
        membershipUnverified={Boolean(selectedStandalone?.membershipUnverified)}
        addableGroups={
          selectedStandalone
            ? deliveryGroups.filter((g) => g.state === "active")
            : []
        }
        saving={membersApi.saving}
        mobileOpen={mobileDetail}
        onClose={() => {
          setMobileDetail(false);
          setDetailOpen(false);
        }}
        onOpenReview={() => openReviewForKey(selectedKey)}
        onRefresh={() => void loadReadinessFor([selectedKey], true)}
        onPreview={(id) => setPreviewGroupId(id)}
        onRemove={(group) => void removeMember(group, selectedKey)}
        onAdd={(id) => {
          const g = deliveryGroups.find((x) => x.id === id);
          if (g && selectedStandalone) {
            void addStandaloneToGroup(g, selectedStandalone);
          }
        }}
      />
    );
  };

  // Header "Preview merge": the selected member's group, else the first
  // visible group. Standalone-only views have nothing to preview.
  const firstVisibleGroup = visible.find((v) => v.item.kind === "group")?.item;
  const previewTargetGroupId =
    selectedGroupMember?.group.id ??
    (firstVisibleGroup?.kind === "group" ? firstVisibleGroup.group.id : null);

  const repoCount = repoOptions.length;
  const workspaceName = workspace?.name || workspaceId || "Workspace";
  const userName = user?.name?.trim() || user?.email?.trim() || null;
  const userSub = user?.email && user.email !== userName ? user.email : null;
  const singleEpic = selectedEpics.size === 1 ? [...selectedEpics][0]! : "";
  const singleRepo = selectedRepos.size === 1 ? [...selectedRepos][0]! : "";
  const kindValue =
    kinds.size === 2 ? "all" : kinds.has("group") ? "group" : "standalone";
  const mineLabel = githubLogin
    ? `Mine filter for GitHub @${githubLogin}`
    : githubIdentityMissing
      ? "Mine filter — GitHub identity unavailable"
      : loomActor
        ? "Mine filter — GitHub login unavailable; Loom actor matching only"
        : "Mine filter — GitHub identity unavailable";

  return (
    <div className={styles.shell} data-testid="stacked-pr-workspace">
      <WorkspaceNav
        workspaceName={workspaceName}
        mode={mode}
        onModeChange={setMode}
        queueCount={flatKeys.length}
        historyCount={history.length}
        repoOptions={repoOptions}
        selectedRepos={selectedRepos}
        onToggleRepo={(repo) => toggleSet(repo, setSelectedRepos)}
        onClearRepos={() => setSelectedRepos(new Set())}
        onShowGuide={() => setGuideOpen(true)}
        onShowShortcuts={() => setHelpOpen(true)}
        userName={userName}
        userSub={userSub}
        chrome={chrome}
      />

      <div className={styles.mainShell}>
        <WorkspaceTopbar
          current={mode === "history" ? "Merge history" : "Pull requests"}
          workspaceName={workspaceName}
          workspaceId={workspaceId}
          chrome={chrome}
          onShowGuide={() => setGuideOpen(true)}
        />

        <div className={styles.scroller} data-testid="stacked-pr-scroll">
          <div className={styles.page}>
            <header className={styles.pageHeading}>
              <div>
                <h1 className={styles.title}>Pull requests</h1>
                <p className={styles.pageSub}>
                  {loading && items.length === 0 ? (
                    <>Loading pull requests…</>
                  ) : (
                    <>
                      <span>{openCount} open PRs</span>
                      <span className={styles.slash}>/</span>
                      <span>
                        {groupCount} delivery group{groupCount === 1 ? "" : "s"}
                      </span>
                      <span>
                        · {standaloneCount} standalone across {repoCount} repo
                        {repoCount === 1 ? "" : "s"}
                      </span>
                      {notCurrentCount > 0 ? (
                        <span className={styles.warnText}>
                          · {notCurrentCount} with evidence not current
                        </span>
                      ) : null}
                      {deliveryGroupsHasMore ? (
                        <span>· more groups available</span>
                      ) : null}
                      {!membershipComplete ? (
                        <span className={styles.warnText}>
                          · membership incomplete
                        </span>
                      ) : null}
                    </>
                  )}
                </p>
              </div>
              <div className={styles.headingActions}>
                <button
                  type="button"
                  className={styles.btn}
                  data-variant="ghost"
                  onClick={() => setGuideOpen(true)}
                >
                  <Icon name="map" /> How groups work
                </button>
                <button
                  type="button"
                  className={styles.btn}
                  disabled={!previewTargetGroupId}
                  title={
                    previewTargetGroupId
                      ? "Read-only ordered merge preview"
                      : "No delivery group to preview"
                  }
                  onClick={() =>
                    previewTargetGroupId &&
                    setPreviewGroupId(previewTargetGroupId)
                  }
                >
                  <Icon name="merge" /> Preview merge
                </button>
              </div>
            </header>

            {mode === "queue" && (
              <div
                className={styles.tabs}
                role="tablist"
                aria-label="Readiness tabs"
              >
                {TABS.map((t) => (
                  <button
                    key={t.id}
                    type="button"
                    role="tab"
                    aria-selected={tab === t.id}
                    className={styles.tab}
                    onClick={() => setTab(t.id)}
                  >
                    {t.label}
                    <span className={styles.tabCount}>{tabCounts[t.id]}</span>
                  </button>
                ))}
              </div>
            )}

            <div className={styles.toolbar}>
              <label className={styles.search} htmlFor={searchId}>
                <Icon name="search" />
                <input
                  id={searchId}
                  ref={searchRef}
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Search PRs, branches, or groups"
                  aria-label="Search pull requests"
                />
                <kbd className={styles.kbd} aria-hidden="true">
                  /
                </kbd>
              </label>
              <label className={styles.selectWrap}>
                <Icon name="stack" />
                <select
                  aria-label="Filter by epic"
                  value={singleEpic}
                  onChange={(e) =>
                    setSelectedEpics(
                      e.target.value ? new Set([e.target.value]) : new Set(),
                    )
                  }
                >
                  <option value="">
                    {selectedEpics.size > 1
                      ? `${selectedEpics.size} epics`
                      : "All epics"}
                  </option>
                  {epicOptions.map(([epic, count]) => (
                    <option key={epic} value={epic}>
                      {epic} ({count})
                    </option>
                  ))}
                </select>
              </label>
              <label className={styles.selectWrap}>
                <Icon name="repo" />
                <select
                  aria-label="Filter by repository"
                  value={singleRepo}
                  onChange={(e) =>
                    setSelectedRepos(
                      e.target.value ? new Set([e.target.value]) : new Set(),
                    )
                  }
                >
                  <option value="">
                    {selectedRepos.size > 1
                      ? `${selectedRepos.size} repos`
                      : "All repos"}
                  </option>
                  {repoOptions.map(([repo, count]) => (
                    <option key={repo} value={repo}>
                      {repo} ({count})
                    </option>
                  ))}
                </select>
              </label>
              <label className={styles.selectWrap}>
                <Icon name="pr" />
                <select
                  aria-label="Filter by kind"
                  value={kindValue}
                  onChange={(e) => {
                    const v = e.target.value;
                    setKinds(
                      new Set<QueueKind>(
                        v === "group"
                          ? ["group"]
                          : v === "standalone"
                            ? ["standalone"]
                            : ["group", "standalone"],
                      ),
                    );
                  }}
                >
                  <option value="all">Groups + standalone</option>
                  <option value="group">Delivery groups ({groupCount})</option>
                  <option value="standalone">
                    Standalone ({standaloneCount})
                  </option>
                </select>
              </label>
              <label
                className={styles.toggle}
                title={
                  githubLogin
                    ? `Verified GitHub login (@${githubLogin})`
                    : (githubViewer?.message ??
                      "GitHub identity unavailable — display name is never used as login")
                }
              >
                <input
                  type="checkbox"
                  className={styles.toggleInput}
                  checked={mine}
                  onChange={() => setMine((v) => !v)}
                  aria-label={mineLabel}
                />
                <span className={styles.toggleSwitch} aria-hidden="true" />
                Mine
                <span
                  className={styles.mineChip}
                  data-testid="mine-identity-chip"
                >
                  {githubLogin
                    ? `@${githubLogin}`
                    : githubViewer == null
                      ? "—"
                      : "unavailable"}
                </span>
              </label>
              <span className={styles.spacer} />
              <span className={styles.resultCount}>
                {mode === "history"
                  ? `${history.length} events`
                  : `${visible.length} shown`}
              </span>
              <div
                className={styles.viewSwitch}
                role="group"
                aria-label="Path or list"
              >
                <button
                  type="button"
                  aria-pressed={view === "path"}
                  aria-label="Path view"
                  title="Path view (v)"
                  disabled={mode === "history"}
                  onClick={() => setView("path")}
                >
                  <Icon name="map" />
                </button>
                <button
                  type="button"
                  aria-pressed={view === "list"}
                  aria-label="List view"
                  title="List view (v)"
                  disabled={mode === "history"}
                  onClick={() => setView("list")}
                >
                  <Icon name="list" />
                </button>
              </div>
              <button
                type="button"
                className={styles.btn}
                aria-pressed={detailOpen}
                onClick={() => {
                  // Narrow layouts only show the summary as an overlay.
                  if (isNarrow()) {
                    setDetailOpen(true);
                    setMobileDetail(Boolean(selectedKey));
                    return;
                  }
                  setDetailOpen((v) => !v);
                }}
              >
                <Icon name="panel" />{" "}
                {detailOpen ? "Hide summary" : "Show summary"}
              </button>
            </div>

            {mine && githubIdentityMissing ? (
              <p
                className={styles.infoBanner}
                data-testid="mine-viewer-unavailable"
                role="status"
              >
                GitHub identity unavailable
                {githubViewer?.message ? `: ${githubViewer.message}` : ""}.
                Author matching is paused; Loom owner/assignee matches still
                apply when signed in.
              </p>
            ) : null}
            {githubWarning && (
              <p
                className={styles.warnBanner}
                role="status"
                data-testid="prs-github-warning"
              >
                <Icon name="warning" />
                <span>
                  {githubWarning}. Loom-backed delivery groups are still shown.
                </span>
              </p>
            )}
            {writeBanner && (
              <p
                className={styles.warnBanner}
                role="alert"
                data-testid="dg-write-error"
              >
                <Icon name="warning" />
                <span>{writeBanner}</span>
              </p>
            )}
            {standaloneContinuation?.has_more && (
              <p className={styles.infoBanner} role="status">
                More standalone PRs exist beyond this page
                {standaloneContinuation.repos
                  .filter((r) => r.has_more)
                  .map((r) => ` (${r.source_repo || r.repo})`)
                  .join("")}
                .
              </p>
            )}

            {mode === "queue" && view === "path" ? (
              <div className={styles.legend} aria-hidden="true">
                <span>
                  <i data-tone="merged" /> Merged
                </span>
                <span>
                  <i data-tone="ready" /> Ready
                </span>
                <span>
                  <i data-tone="review" /> Waiting
                </span>
                <span>
                  <i data-tone="blocked" /> Blocked
                </span>
                <span>
                  <i data-tone="unknown" /> Stale / unknown
                </span>
              </div>
            ) : null}

            <div
              className={styles.workArea}
              data-no-summary={!detailOpen || undefined}
              ref={listRef}
            >
              <div className={styles.main}>
                {mode === "history"
                  ? renderHistory()
                  : view === "list"
                    ? renderListView()
                    : renderPathView()}
              </div>
              {detailOpen ? renderDetail() : null}
            </div>
            <p className={styles.footerNote}>
              Loom · Every change has a clear path to main.
            </p>
          </div>
        </div>
      </div>

      {mobileDetail && detailOpen ? (
        <div
          className={styles.mobileScrim}
          onClick={() => setMobileDetail(false)}
          aria-hidden="true"
        />
      ) : null}

      <MergePreviewDialog
        open={Boolean(previewGroupId)}
        title={
          deliveryGroups.find((g) => g.id === previewGroupId)?.title ??
          "Delivery group"
        }
        preview={preview.preview}
        loading={preview.loading}
        error={preview.error}
        onClose={() => {
          preview.clear();
          setPreviewGroupId(null);
        }}
      />

      {guideOpen ? (
        <div
          className={styles.helpOverlay}
          role="dialog"
          aria-modal="true"
          aria-label="How delivery groups work"
          onClick={() => setGuideOpen(false)}
        >
          <div className={styles.help} onClick={(e) => e.stopPropagation()}>
            <header className={styles.helpHead}>
              <Icon name="map" />
              <h2>How delivery groups work</h2>
            </header>
            <ol className={styles.guideSteps}>
              <li>
                <strong>Groups are explicit.</strong> A delivery group is an
                ordered list of PRs saved in Loom. Loom never infers membership
                from branches, epics, labels, or GitHub stacks.
              </li>
              <li>
                <strong>Order is the merge path.</strong> Steps can cross
                registered repos; a cross-repo step is a delivery dependency,
                not branch ancestry.
              </li>
              <li>
                <strong>Readiness is timestamped evidence.</strong> A PR is only
                shown Ready while its GitHub observation is fresh. Stale, aging,
                rate-limited, or unobserved evidence stays labeled.
              </li>
              <li>
                <strong>Merge preview is read-only.</strong> It shows which
                prefix is ready and the first blocker. It never merges, queues,
                or retargets.
              </li>
            </ol>
            <button
              type="button"
              className={styles.btn}
              onClick={() => setGuideOpen(false)}
            >
              Close
            </button>
          </div>
        </div>
      ) : null}

      {helpOpen ? (
        <div
          className={styles.helpOverlay}
          role="dialog"
          aria-modal="true"
          aria-label="Stacked PR shortcuts"
          onClick={() => setHelpOpen(false)}
        >
          <div className={styles.help} onClick={(e) => e.stopPropagation()}>
            <header className={styles.helpHead}>
              <Icon name="keyboard" />
              <h2>Shortcuts</h2>
            </header>
            <ul className={styles.shortcutList}>
              <li>
                Focus search <kbd>/</kbd>
              </li>
              <li>
                Next / previous PR <kbd>j</kbd> <kbd>k</kbd>
              </li>
              <li>
                Open review <kbd>o</kbd>
              </li>
              <li>
                Ordered merge preview (group member) <kbd>m</kbd>
              </li>
              <li>
                Toggle Path / List <kbd>v</kbd>
              </li>
              <li>
                Toggle merge history <kbd>h</kbd>
              </li>
              <li>
                This help <kbd>?</kbd>
              </li>
              <li>
                Close dialog / detail <kbd>Esc</kbd>
              </li>
            </ul>
            <button
              type="button"
              className={styles.btn}
              onClick={() => setHelpOpen(false)}
            >
              Close
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
