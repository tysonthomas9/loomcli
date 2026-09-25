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
  type KeyboardEvent as ReactKeyboardEvent,
  type SetStateAction,
} from "react";

import type { GitPullRequest } from "@/api/workspace/pullRequests";
import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import type {
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
  formatObservedAtLine,
  readinessDisplay,
  shortPrKey,
} from "@/utils/pullRequest/readinessDisplay";
import type { Issue } from "@/types";
import { useAuth } from "@/contexts/AuthContext";
import { MergePreviewDialog } from "./MergePreviewDialog";
import { ReadinessBadge } from "./ReadinessBadge";
import styles from "./StackedPRWorkspace.module.css";

export interface StackedPRWorkspaceProps {
  issues: Issue[];
  pullRequests: GitPullRequest[];
  deliveryGroups: DeliveryGroupView[];
  deliveryGroupsHasMore?: boolean;
  standaloneContinuation?: StandalonePRContinuation;
  warnings: string[];
  loading: boolean;
  error: Error | null;
  onOpenReview: (args: { issueId?: string; reviewPr?: string }) => void;
  onRefetch?: () => Promise<void>;
}

const TABS: { id: QueueTab; label: string }[] = [
  { id: "all", label: "All" },
  { id: "review", label: "Needs review" },
  { id: "ready", label: "Ready" },
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
  warnings,
  loading,
  error,
  onOpenReview,
  onRefetch,
}: StackedPRWorkspaceProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const { user } = useAuth();
  const searchId = useId();
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const [tab, setTab] = useState<QueueTab>("all");
  const [mode, setMode] = useState<QueueMode>("queue");
  const [view, setView] = useState<QueueViewMode>("path");
  const [query, setQuery] = useState("");
  const [railQuery, setRailQuery] = useState("");
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

  // Auth exposes display name/email only — no verified GitHub login on the
  // session/JWT. Mine matches author_login/assignee against that best-available
  // identity; full GitHub-login parity needs an auth-surface field beyond this UI.
  const mineIdentity = user?.name?.trim() || null;

  const filters = useMemo(
    () => ({
      tab,
      query,
      repos: selectedRepos,
      epics: selectedEpics,
      kinds,
      mine,
      mineIdentity,
    }),
    [tab, query, selectedRepos, selectedEpics, kinds, mine, mineIdentity],
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
          mineIdentity,
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
      mineIdentity,
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

  const filteredRepoOptions = useMemo(() => {
    const q = railQuery.trim().toLowerCase();
    if (!q) return repoOptions;
    return repoOptions.filter(([name]) => name.toLowerCase().includes(q));
  }, [repoOptions, railQuery]);

  const filteredEpicOptions = useMemo(() => {
    const q = railQuery.trim().toLowerCase();
    if (!q) return epicOptions;
    return epicOptions.filter(([name]) => name.toLowerCase().includes(q));
  }, [epicOptions, railQuery]);

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
    () => {
      setMobileDetail(false);
    },
    mobileDetail && !previewGroupId && !helpOpen,
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
      const target = event.target as HTMLElement | null;
      const tag = target?.tagName;
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
      if (previewGroupId || helpOpen) return;

      const k = event.key;
      if (k === "/" || (k === "s" && !event.metaKey && !event.ctrlKey)) {
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
        if (next) setSelectedKey(next);
        return;
      }
      if (k === "k" || k === "ArrowUp") {
        event.preventDefault();
        const idx = selectedKey
          ? flatKeys.indexOf(selectedKey)
          : flatKeys.length;
        const next = flatKeys[Math.max(0, idx - 1)];
        if (next) setSelectedKey(next);
      }
    },
    [
      previewGroupId,
      helpOpen,
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

  const renderMemberRow = (
    prKey: string,
    step: number | null,
    opts: {
      dimmed?: boolean;
      group?: DeliveryGroupView;
      crossFrom?: string;
      titleOverride?: string;
      prOverride?: GitPullRequest;
    } = {},
  ): JSX.Element => {
    const pr = opts.prOverride ?? prByKey.get(prKey);
    const view = readinessFor(prKey);
    const display = readinessDisplay(view);
    const title = opts.titleOverride ?? pr?.title ?? shortPrKey(prKey);
    const selected = selectedKey === prKey;
    const onActivate = () => {
      const narrow =
        typeof window.matchMedia === "function" &&
        window.matchMedia("(max-width: 900px)").matches;
      selectKey(prKey, narrow);
    };
    const onKey = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        onActivate();
      }
    };
    return (
      <li key={prKey} className={styles.pathItem}>
        {opts.crossFrom ? (
          <div className={styles.cross} aria-hidden="true">
            Delivery order crosses {opts.crossFrom} →{" "}
            {canonicalRepoIdentity({
              repo_name: pr?.repo_name,
              source_repo: pr?.source_repo,
            })}{" "}
            (not a branch link)
          </div>
        ) : null}
        <button
          type="button"
          className={styles.row}
          data-current={selected || undefined}
          data-dimmed={opts.dimmed || undefined}
          aria-current={selected ? "true" : undefined}
          aria-label={`Review ${title}`}
          onClick={onActivate}
          onKeyDown={onKey}
          data-testid={`pr-row-${shortPrKey(prKey)}`}
        >
          {step != null ? (
            <span
              className={styles.step}
              data-status={display.key}
              aria-hidden="true"
            >
              {step}
            </span>
          ) : (
            <span className={styles.stepSolo} aria-hidden="true">
              ·
            </span>
          )}
          <span className={styles.rowMain}>
            <span className={styles.rowTitle}>
              <code className={styles.key}>{shortPrKey(prKey)}</code>
              <span className={styles.titleText}>{title}</span>
            </span>
            <span className={styles.rowSub}>
              <span className={styles.chip}>
                {canonicalRepoIdentity({
                  repo_name: pr?.repo_name,
                  source_repo: pr?.source_repo,
                })}
              </span>
              {pr?.head_ref_name ? (
                <span className={styles.chipMono}>
                  {pr.head_ref_name} → {pr.base_ref_name}
                </span>
              ) : null}
            </span>
          </span>
          <ReadinessBadge display={display} />
        </button>
      </li>
    );
  };

  const renderPathView = (): JSX.Element => {
    const groups = visible.filter((v) => v.item.kind === "group");
    const solos = visible.filter((v) => v.item.kind === "standalone");
    return (
      <div className={styles.queue} data-testid="stacked-pr-path">
        {kinds.has("group") && (
          <>
            <h2 className={styles.sectionH}>
              Delivery groups
              <span className={styles.hint}>
                numbered path · may cross registered repos
              </span>
            </h2>
            {groups.length === 0 ? (
              <div className={styles.empty}>No delivery groups match.</div>
            ) : (
              groups.map(({ item, memberDimmed }) => {
                if (item.kind !== "group") return null;
                const g = item.group;
                return (
                  <article
                    key={g.id}
                    className={styles.group}
                    data-testid={`delivery-group-${g.id}`}
                  >
                    <header className={styles.groupHead}>
                      <span className={styles.groupTitle}>{g.title}</span>
                      <span className={styles.meta}>
                        <span className={styles.chip}>rev {g.revision}</span>
                        {g.epic_id ? (
                          <span className={styles.chip}>{g.epic_id}</span>
                        ) : null}
                        {g.inconsistent ? (
                          <span className={styles.warnChip}>inconsistent</span>
                        ) : null}
                      </span>
                      <span className={styles.groupActions}>
                        <button
                          type="button"
                          className={styles.btn}
                          onClick={() => setPreviewGroupId(g.id)}
                        >
                          Ordered preview
                        </button>
                      </span>
                    </header>
                    <ol className={styles.path} aria-label={`${g.title} path`}>
                      {g.members.map((m, i) => {
                        const prev = i > 0 ? g.members[i - 1] : undefined;
                        const cross =
                          prev && memberRepo(prev) !== memberRepo(m)
                            ? memberRepo(prev)
                            : undefined;
                        return renderMemberRow(m.pr_key, i + 1, {
                          dimmed: memberDimmed.get(m.pr_key) === true,
                          group: g,
                          ...(cross ? { crossFrom: cross } : {}),
                        });
                      })}
                    </ol>
                  </article>
                );
              })
            )}
          </>
        )}
        {kinds.has("standalone") && (
          <>
            <h2 className={styles.sectionH}>
              Standalone PRs
              <span className={styles.hint}>
                not in any delivery group · includes PRs opened outside Loom
              </span>
            </h2>
            {!membershipComplete && (
              <p className={styles.warnBanner} role="status">
                Active-group membership is incomplete or unverified. Rows below
                are not confirmed standalone.
              </p>
            )}
            {solos.length === 0 ? (
              <div className={styles.empty}>No standalone PRs match.</div>
            ) : (
              <div className={styles.group}>
                <ul
                  className={styles.path}
                  aria-label="Standalone pull requests"
                >
                  {solos.map(({ item }) =>
                    item.kind === "standalone"
                      ? renderMemberRow(item.prKey, null, {
                          dimmed: false,
                          titleOverride: item.pr.title,
                          prOverride: item.pr,
                        })
                      : null,
                  )}
                </ul>
              </div>
            )}
          </>
        )}
      </div>
    );
  };

  const renderListView = (): JSX.Element => (
    <div className={styles.listWrap} data-testid="stacked-pr-list">
      <table className={styles.listTable}>
        <thead>
          <tr>
            <th>PR</th>
            <th>Title</th>
            <th>Repo</th>
            <th>Status</th>
            <th>Kind</th>
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
                  <td>
                    {canonicalRepoIdentity({
                      repo_name: item.pr.repo_name,
                      source_repo: item.pr.source_repo,
                    })}
                  </td>
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
              const pr = prByKey.get(m.pr_key);
              const d = readinessDisplay(readinessFor(m.pr_key));
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
                  <td>{pr?.title ?? shortPrKey(m.pr_key)}</td>
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
    <div data-testid="stacked-pr-history">
      <h2 className={styles.sectionH}>
        History
        <span className={styles.hint}>
          derived from durable group fields and merged standalone PRs — not a
          full audit log
        </span>
      </h2>
      {history.length === 0 ? (
        <div className={styles.empty}>No history matches.</div>
      ) : (
        <ol className={styles.timeline}>
          {history.map((h) => (
            <li key={h.id}>
              <time dateTime={h.at}>
                {new Date(h.at).toISOString().slice(0, 16).replace("T", " ")}{" "}
                UTC
              </time>
              <div>
                {h.text}
                <div className={styles.timelineSrc}>{h.source}</div>
              </div>
            </li>
          ))}
        </ol>
      )}
    </div>
  );

  const detailView = readinessFor(selectedKey ?? "");
  const detailDisplay = readinessDisplay(detailView);

  const renderDetail = (): JSX.Element => {
    if (!selectedKey) {
      return (
        <aside className={styles.detail} aria-label="Selected PR">
          <p className={styles.detailEmpty}>Select a pull request.</p>
        </aside>
      );
    }
    const pr = selectedStandalone?.pr ?? prByKey.get(selectedKey) ?? null;
    const title = pr?.title ?? shortPrKey(selectedKey);
    return (
      <aside
        className={styles.detail}
        data-mobile-open={mobileDetail || undefined}
        aria-label="Selected PR summary"
        data-testid="selected-pr-detail"
      >
        <header className={styles.detailHead}>
          <span>Selected PR</span>
          <button
            type="button"
            className={styles.detailClose}
            aria-label="Close details"
            onClick={() => {
              setMobileDetail(false);
              setDetailOpen(false);
            }}
          >
            ✕
          </button>
        </header>
        <div className={styles.detailBody}>
          <h2>{title}</h2>
          <code className={styles.key}>{shortPrKey(selectedKey)}</code>
          <ReadinessBadge display={detailDisplay} />
          <p className={styles.evidenceLine}>
            {formatObservedAtLine(detailDisplay)}
          </p>
          <dl className={styles.kv}>
            <dt>Repo</dt>
            <dd>
              {canonicalRepoIdentity({
                repo_name: pr?.repo_name,
                source_repo: pr?.source_repo,
              }) || "—"}
            </dd>
            <dt>Branches</dt>
            <dd>
              {pr?.head_ref_name
                ? `${pr.head_ref_name} → ${pr.base_ref_name}`
                : "—"}
            </dd>
            {pr &&
            (pr.changed_files != null ||
              pr.additions != null ||
              pr.deletions != null) ? (
              <>
                <dt>Changes</dt>
                <dd data-testid="selected-pr-changes-summary">
                  {pr.changed_files != null
                    ? `${pr.changed_files} file${pr.changed_files === 1 ? "" : "s"}`
                    : "files unknown"}
                  {pr.additions != null || pr.deletions != null
                    ? ` · +${pr.additions ?? "?"} / −${pr.deletions ?? "?"}`
                    : ""}
                </dd>
              </>
            ) : null}
            {selectedStandalone?.issueId ||
            issueByPrKey.get(selectedKey)?.id ? (
              <>
                <dt>Task</dt>
                <dd>
                  {selectedStandalone?.issueId ??
                    issueByPrKey.get(selectedKey)?.id}
                </dd>
              </>
            ) : null}
          </dl>

          {selectedGroupMember ? (
            <div className={styles.box}>
              <h3>Delivery group</h3>
              <p>
                Step {selectedGroupMember.index + 1} of{" "}
                {selectedGroupMember.group.members.length} in “
                {selectedGroupMember.group.title}”
              </p>
              {selectedGroupMember.prev ? (
                <p className={styles.muted}>
                  Delivers after {shortPrKey(selectedGroupMember.prev.pr_key)}
                  {memberRepo(selectedGroupMember.prev) !==
                  memberRepo(selectedGroupMember.member)
                    ? " — a cross-repo delivery dependency, not branch ancestry."
                    : "."}
                </p>
              ) : (
                <p className={styles.muted}>
                  First step in the delivery order.
                </p>
              )}
              <div className={styles.btns}>
                <button
                  type="button"
                  className={styles.btn}
                  onClick={() =>
                    setPreviewGroupId(selectedGroupMember.group.id)
                  }
                >
                  Ordered preview
                </button>
                <button
                  type="button"
                  className={styles.btn}
                  disabled={membersApi.saving}
                  onClick={() =>
                    void removeMember(selectedGroupMember.group, selectedKey)
                  }
                >
                  Remove from group
                </button>
              </div>
            </div>
          ) : (
            <div className={styles.box}>
              <h3>Standalone</h3>
              <p className={styles.muted}>
                Not in a delivery group. Loom never infers membership from
                branches, epics, labels, or GitHub stacks.
              </p>
              {selectedStandalone?.membershipUnverified ? (
                <p className={styles.warnBanner} role="status">
                  Membership unverified — not confirmed standalone.
                </p>
              ) : null}
              {deliveryGroups.length > 0 && selectedStandalone ? (
                <div className={styles.btns}>
                  <label className={styles.field}>
                    Add to group
                    <select
                      aria-label="Add to delivery group"
                      defaultValue=""
                      onChange={(e) => {
                        const id = e.target.value;
                        e.target.value = "";
                        const g = deliveryGroups.find((x) => x.id === id);
                        if (g && selectedStandalone) {
                          void addStandaloneToGroup(g, selectedStandalone);
                        }
                      }}
                    >
                      <option value="" disabled>
                        Choose group…
                      </option>
                      {deliveryGroups
                        .filter((g) => g.state === "active")
                        .map((g) => (
                          <option key={g.id} value={g.id}>
                            {g.title}
                          </option>
                        ))}
                    </select>
                  </label>
                </div>
              ) : null}
            </div>
          )}

          <div className={styles.btns}>
            <button
              type="button"
              className={styles.btnPrimary}
              onClick={() => openReviewForKey(selectedKey)}
            >
              Open review
            </button>
            <button
              type="button"
              className={styles.btn}
              onClick={() => void loadReadinessFor([selectedKey], true)}
            >
              Refresh evidence
            </button>
          </div>
        </div>
      </aside>
    );
  };

  return (
    <div className={styles.page} data-testid="stacked-pr-workspace">
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>Pull Requests</h1>
          <p className={styles.subtitle}>
            {loading && items.length === 0 ? (
              <>Loading pull requests…</>
            ) : (
              <>
                <strong>{openCount}</strong> open ·{" "}
                <strong>{groupCount}</strong> delivery groups ·{" "}
                <strong>{standaloneCount}</strong> standalone ·{" "}
                <strong>{notCurrentCount}</strong> with evidence not current
                {deliveryGroupsHasMore ? " · more groups available" : ""}
                {!membershipComplete ? " · membership incomplete" : ""}
              </>
            )}
          </p>
        </div>
        <div className={styles.headerActions}>
          <div
            className={styles.seg}
            role="group"
            aria-label="Queue or history"
          >
            <button
              type="button"
              aria-pressed={mode === "queue"}
              onClick={() => setMode("queue")}
            >
              Queue
            </button>
            <button
              type="button"
              aria-pressed={mode === "history"}
              onClick={() => setMode("history")}
            >
              History
            </button>
          </div>
          <div className={styles.seg} role="group" aria-label="Path or list">
            <button
              type="button"
              aria-pressed={view === "path"}
              disabled={mode === "history"}
              onClick={() => setView("path")}
            >
              Path
            </button>
            <button
              type="button"
              aria-pressed={view === "list"}
              disabled={mode === "history"}
              onClick={() => setView("list")}
            >
              List
            </button>
          </div>
          <button
            type="button"
            className={styles.btn}
            aria-pressed={detailOpen}
            onClick={() => setDetailOpen((v) => !v)}
          >
            Summary
          </button>
          <button
            type="button"
            className={styles.btn}
            onClick={() => setHelpOpen(true)}
            aria-label="Keyboard shortcuts"
          >
            ?
          </button>
        </div>
      </header>

      {githubWarning && (
        <p
          className={styles.warnBanner}
          role="status"
          data-testid="prs-github-warning"
        >
          {githubWarning}. Loom-backed delivery groups are still shown.
        </p>
      )}
      {writeBanner && (
        <p
          className={styles.warnBanner}
          role="alert"
          data-testid="dg-write-error"
        >
          {writeBanner}
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

      <div className={styles.layout} data-no-summary={!detailOpen || undefined}>
        <aside className={styles.rail} aria-label="Pull request filters">
          <label className={styles.search}>
            <span aria-hidden="true">⌕</span>
            <input
              type="search"
              value={railQuery}
              onChange={(e) => setRailQuery(e.target.value)}
              placeholder="Filter repos & epics…"
              aria-label="Filter repositories and epics"
            />
          </label>

          <section>
            <h2 className={styles.railH}>Kind</h2>
            {(
              [
                ["group", "Delivery groups"],
                ["standalone", "Standalone PRs"],
              ] as const
            ).map(([k, label]) => (
              <label key={k} className={styles.check}>
                <input
                  type="checkbox"
                  checked={kinds.has(k)}
                  onChange={() => {
                    setKinds((prev) => {
                      const next = new Set(prev);
                      if (next.has(k)) next.delete(k);
                      else next.add(k);
                      if (next.size === 0) next.add(k);
                      return next;
                    });
                  }}
                />
                {label}
                <span className={styles.count}>
                  {k === "group" ? groupCount : standaloneCount}
                </span>
              </label>
            ))}
            <label className={styles.check}>
              <input
                type="checkbox"
                checked={mine}
                onChange={() => setMine((v) => !v)}
                aria-label={
                  mineIdentity
                    ? `Mine filter for ${mineIdentity}`
                    : "Mine filter unavailable without signed-in identity"
                }
              />
              Mine
              <span
                className={styles.count}
                title="Auth display name (no verified GitHub login on session)"
              >
                {mineIdentity ?? "—"}
              </span>
            </label>
          </section>

          <section>
            <header className={styles.railHead}>
              <h2 className={styles.railH}>Repos</h2>
              {selectedRepos.size > 0 && (
                <button
                  type="button"
                  onClick={() => setSelectedRepos(new Set())}
                >
                  Clear
                </button>
              )}
            </header>
            {filteredRepoOptions.map(([repo, count]) => (
              <label key={repo} className={styles.check}>
                <input
                  type="checkbox"
                  checked={selectedRepos.has(repo)}
                  onChange={() => toggleSet(repo, setSelectedRepos)}
                />
                {repo}
                <span className={styles.count}>{count}</span>
              </label>
            ))}
          </section>

          <section>
            <header className={styles.railHead}>
              <h2 className={styles.railH}>Epics</h2>
              {selectedEpics.size > 0 && (
                <button
                  type="button"
                  onClick={() => setSelectedEpics(new Set())}
                >
                  Clear
                </button>
              )}
            </header>
            {filteredEpicOptions.map(([epic, count]) => (
              <label key={epic} className={styles.check}>
                <input
                  type="checkbox"
                  checked={selectedEpics.has(epic)}
                  onChange={() => toggleSet(epic, setSelectedEpics)}
                />
                <span className={styles.epicLabel} title={epic}>
                  {epic}
                </span>
                <span className={styles.count}>{count}</span>
              </label>
            ))}
          </section>
        </aside>

        <div className={styles.main} ref={listRef}>
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
              <span aria-hidden="true">⌕</span>
              <input
                id={searchId}
                ref={searchRef}
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search PRs, groups, branches…"
                aria-label="Search pull requests"
              />
            </label>
            <span className={styles.countLabel}>
              {mode === "history"
                ? `${history.length} events`
                : `${visible.length} shown`}
            </span>
          </div>
          <div className={styles.scroll}>
            {mode === "history"
              ? renderHistory()
              : view === "list"
                ? renderListView()
                : renderPathView()}
          </div>
        </div>

        {detailOpen ? renderDetail() : null}
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

      {helpOpen ? (
        <div
          className={styles.helpOverlay}
          role="dialog"
          aria-modal="true"
          aria-label="Stacked PR shortcuts"
          onClick={() => setHelpOpen(false)}
        >
          <div className={styles.help} onClick={(e) => e.stopPropagation()}>
            <h2>Shortcuts</h2>
            <ul>
              <li>
                <kbd>/</kbd> or <kbd>s</kbd> Focus search
              </li>
              <li>
                <kbd>j</kbd> / <kbd>k</kbd> Next / previous PR
              </li>
              <li>
                <kbd>o</kbd> Open review
              </li>
              <li>
                <kbd>m</kbd> Ordered merge preview (group member)
              </li>
              <li>
                <kbd>v</kbd> Toggle Path / List
              </li>
              <li>
                <kbd>h</kbd> Toggle History
              </li>
              <li>
                <kbd>?</kbd> This help
              </li>
              <li>
                <kbd>Esc</kbd> Close dialog / detail
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
