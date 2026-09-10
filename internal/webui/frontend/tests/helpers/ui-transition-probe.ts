import type { Page } from "@playwright/test";

const PROBE_KEY = "__loomUiTransitionProbe";

export interface TransitionScope {
  workspace: string;
  repository?: string | null;
  query?: string | null;
  mode?: string | null;
  selectedIssue?: string | null;
  generation?: number | string | null;
  sourceIncarnation?: string | null;
  recoveryEligible?: boolean;
}

export interface TransitionProbeOptions {
  root: string;
  protectedNodes?: string[];
  forbiddenWithinRoot?: string[];
  maxRecords?: number;
  scope: TransitionScope;
}

export interface TransitionViolation {
  at: number;
  phase: string;
  kind: string;
  selector?: string;
  detail: string;
}

export interface TransitionProbeReport {
  scope: TransitionScope;
  phases: Array<{ name: string; at: number }>;
  recordCount: number;
  violations: TransitionViolation[];
  retired: boolean;
}

interface BrowserProbeState extends TransitionProbeReport {
  rootSelector: string;
  root: Element;
  protected: Array<{ selector: string; node: Element }>;
  maxRecords: number;
  knownNodes: Set<Node>;
  observer: MutationObserver;
  process: (records: MutationRecord[]) => void;
  assertions: number;
  disposed: boolean;
  phase: string;
}

function failure(report: TransitionProbeReport): Error {
  return new Error(
    `UI transition invariant failed:\n${report.violations
      .map(
        (item) =>
          `${item.kind}${item.selector ? ` (${item.selector})` : ""}: ${item.detail}`,
      )
      .join("\n")}`,
  );
}

/** Observe a loaded, same-scope surface from a stable document ancestor. */
export async function observeTransition(
  page: Page,
  options: TransitionProbeOptions,
) {
  await page.evaluate(
    ({ options, key }) => {
      const registry = window as unknown as Record<string, BrowserProbeState>;
      const active = registry[key];
      if (active && !active.disposed)
        throw new Error("A UI transition probe is already active");
      const root = document.querySelector(options.root);
      if (!root) throw new Error(`Transition root not found: ${options.root}`);
      const forbidden = options.forbiddenWithinRoot ?? [];
      for (const selector of forbidden) {
        if (root.matches(selector) || root.querySelector(selector))
          throw new Error(
            `Transition root is not ready; forbidden node is already present: ${selector}`,
          );
      }
      const protectedNodes = (options.protectedNodes ?? []).map((selector) => {
        const matches = root.querySelectorAll(selector);
        if (matches.length !== 1)
          throw new Error(
            `Protected selector must match exactly once: ${selector} (${matches.length})`,
          );
        return { selector, node: matches[0] };
      });
      const knownNodes = new Set<Node>();
      const enroll = (node: Node) => {
        knownNodes.add(node);
        if (node instanceof Element)
          for (const child of node.querySelectorAll("*")) knownNodes.add(child);
      };
      enroll(root);
      let state!: BrowserProbeState;
      const violation = (kind: string, detail: string, selector?: string) => {
        state.violations.push({
          at: performance.now(),
          phase: state.phase,
          kind,
          ...(selector ? { selector } : {}),
          detail,
        });
      };
      const inspectForbidden = (node: Node) => {
        if (!(node instanceof Element)) return;
        for (const selector of forbidden) {
          if (node.matches(selector) || node.querySelector(selector))
            violation(
              "forbidden-node-inserted",
              "Forbidden node entered the enrolled transition subtree",
              selector,
            );
        }
      };
      const process = (records: MutationRecord[]) => {
        for (const record of records) {
          state.recordCount++;
          if (state.recordCount > state.maxRecords) {
            if (
              !state.violations.some((item) => item.kind === "record-overflow")
            )
              violation(
                "record-overflow",
                `Mutation record budget exceeded (${state.maxRecords})`,
              );
            continue;
          }
          if (state.retired) continue;
          // A removal of the root is recorded on its parent, which may be
          // outside the enrolled subtree. Inspect removals before target scope.
          for (const node of record.removedNodes) {
            if (
              node === root ||
              (node instanceof Element && node.contains(root))
            )
              violation(
                "root-removed",
                "The enrolled transition root was removed",
              );
            for (const item of protectedNodes) {
              if (
                node === item.node ||
                (node instanceof Element && node.contains(item.node))
              )
                violation(
                  "protected-node-removed",
                  "A protected node was removed during the same-scope transition",
                  item.selector,
                );
            }
            // A node can be appended and removed before observer delivery, or
            // constructed off-DOM inside a wrapper that is then appended. Its
            // current containment is irrelevant; the ordered record proves it
            // entered this enrolled history.
            if (knownNodes.has(record.target)) inspectForbidden(node);
          }
          if (!knownNodes.has(record.target)) continue;
          for (const node of record.addedNodes) {
            inspectForbidden(node);
            // Retain membership after detachment. Later records whose target is
            // inside a removed wrapper still belong to the observed history.
            enroll(node);
          }
        }
      };
      const observer = new MutationObserver(process);
      state = {
        scope: options.scope,
        rootSelector: options.root,
        root,
        protected: protectedNodes,
        maxRecords: options.maxRecords ?? 2_000,
        knownNodes,
        observer,
        process,
        phases: [{ name: "observing", at: performance.now() }],
        recordCount: 0,
        violations: [],
        retired: false,
        assertions: 0,
        disposed: false,
        phase: "observing",
      };
      registry[key] = state;
      observer.observe(document.body, { childList: true, subtree: true });
    },
    { options, key: PROBE_KEY },
  );

  const hostFailures: TransitionViolation[] = [];
  let ended = false;
  const addHostFailure = (kind: string, detail: string) => {
    if (!ended)
      hostFailures.push({
        at: performance.now(),
        phase: "browser",
        kind,
        detail,
      });
  };
  const onClose = () =>
    addHostFailure("page-closed", "Page closed during observation");
  const onCrash = () =>
    addHostFailure("page-crashed", "Page crashed during observation");
  const onFrameNavigated = (frame: ReturnType<Page["mainFrame"]>) => {
    if (frame === page.mainFrame())
      addHostFailure(
        "page-navigated",
        "Main frame navigated during observation",
      );
  };
  page.on("close", onClose);
  page.on("crash", onCrash);
  page.on("framenavigated", onFrameNavigated);

  const report = async (): Promise<TransitionProbeReport> => {
    const browserReport = await page.evaluate((key) => {
      const state = (window as unknown as Record<string, BrowserProbeState>)[
        key
      ];
      if (!state || state.disposed)
        throw new Error("UI transition probe is not active");
      state.process(state.observer.takeRecords());
      if (!state.retired) {
        if (document.querySelector(state.rootSelector) !== state.root)
          state.violations.push({
            at: performance.now(),
            phase: state.phase,
            kind: "root-identity-changed",
            detail: "The transition root no longer has its enrolled identity",
          });
        for (const item of state.protected) {
          if (state.root.querySelector(item.selector) !== item.node)
            state.violations.push({
              at: performance.now(),
              phase: state.phase,
              kind: "protected-node-identity-changed",
              selector: item.selector,
              detail:
                "A protected selector no longer resolves to its enrolled node",
            });
        }
      }
      return {
        scope: state.scope,
        phases: state.phases,
        recordCount: state.recordCount,
        violations: state.violations,
        retired: state.retired,
      };
    }, PROBE_KEY);
    return {
      ...browserReport,
      violations: [...browserReport.violations, ...hostFailures],
    };
  };

  const removeListeners = () => {
    page.off("close", onClose);
    page.off("crash", onCrash);
    page.off("framenavigated", onFrameNavigated);
  };

  return {
    async phase(name: string) {
      await page.evaluate(
        ({ key, name }) => {
          const state = (
            window as unknown as Record<string, BrowserProbeState>
          )[key];
          if (!state || state.disposed)
            throw new Error("UI transition probe is not active");
          state.process(state.observer.takeRecords());
          state.phase = name;
          state.phases.push({ name, at: performance.now() });
        },
        { key: PROBE_KEY, name },
      );
    },
    async retireScope() {
      await page.evaluate((key) => {
        const state = (window as unknown as Record<string, BrowserProbeState>)[
          key
        ];
        if (!state || state.disposed)
          throw new Error("UI transition probe is not active");
        state.process(state.observer.takeRecords());
        state.retired = true;
        state.phase = "scope-retired";
        state.phases.push({ name: state.phase, at: performance.now() });
      }, PROBE_KEY);
    },
    async assertSatisfied() {
      const value = await report();
      await page.evaluate((key) => {
        (window as unknown as Record<string, BrowserProbeState>)[key]
          .assertions++;
      }, PROBE_KEY);
      if (value.violations.length) throw failure(value);
      return value;
    },
    report,
    async dispose() {
      if (ended) return;
      const final = await page.evaluate((key) => {
        const state = (window as unknown as Record<string, BrowserProbeState>)[
          key
        ];
        state.process(state.observer.takeRecords());
        if (!state.retired) {
          if (document.querySelector(state.rootSelector) !== state.root)
            state.violations.push({
              at: performance.now(),
              phase: state.phase,
              kind: "root-identity-changed",
              detail: "The transition root no longer has its enrolled identity",
            });
          for (const item of state.protected) {
            if (state.root.querySelector(item.selector) !== item.node)
              state.violations.push({
                at: performance.now(),
                phase: state.phase,
                kind: "protected-node-identity-changed",
                selector: item.selector,
                detail:
                  "A protected selector no longer resolves to its enrolled node",
              });
          }
        }
        state.observer.disconnect();
        state.disposed = true;
        return {
          assertions: state.assertions,
          report: {
            scope: state.scope,
            phases: state.phases,
            recordCount: state.recordCount,
            violations: state.violations,
            retired: state.retired,
          },
        };
      }, PROBE_KEY);
      ended = true;
      removeListeners();
      if (!final.assertions)
        throw new Error("UI transition probe disposed before assertion");
      const finalReport = {
        ...final.report,
        violations: [...final.report.violations, ...hostFailures],
      };
      if (finalReport.violations.length) throw failure(finalReport);
    },
  };
}
