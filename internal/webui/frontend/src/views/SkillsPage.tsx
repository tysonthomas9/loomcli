/**
 * SkillsPage - the Skills section of a workspace.
 *
 * A straight port, not a second implementation: this is the same
 * WorkspaceFileBrowser the Files section uses, in "skills" mode, which emits
 * only the skills roots. Tree, editor, tabs, saving, capability gating and
 * dialogs all come along unchanged, so the two sections cannot drift apart.
 *
 * The section keeps its own tab set (see skillsFileBrowserTabsStorageKey) —
 * it is a separate destination in the nav rail, so a file opened here should
 * not turn up in the Files section's tabs.
 */

import { lazy, Suspense } from "react";

import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView } from "@/hooks";

import styles from "./SkillsPage.module.css";

const WorkspaceFileBrowser = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
    default: m.WorkspaceFileBrowser,
  })),
);

export function SkillsPage() {
  const { view: activeView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <div className={styles.page}>
        <Suspense fallback={<LoadingSkeleton.FileExplorer />}>
          <WorkspaceFileBrowser mode="skills" />
        </Suspense>
      </div>
    </ErrorBoundary>
  );
}
