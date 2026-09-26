/**
 * WorkspaceFileBrowser — the shared file browser, not "the Files explorer".
 *
 * Three sections mount it through this barrel, each in its own mode, and none
 * of them owns it:
 *   - views/FilesPage        → mode="workspace"
 *   - views/AgentsPage /
 *     components/AgentDetailPanel → mode="agent"
 *   - views/SkillsPage       → mode="skills"
 *
 * There is one browser implementation, not three: the tree, editor, tab bar,
 * saving, capability gating and dialogs are a single code path, so the sections
 * cannot drift into divergent implementations.
 *
 * `buildFileTreeSections` (treeRoots.ts) is the single owner of ONE decision —
 * which roots a section sees. It is not the only place that consults the mode,
 * and claiming otherwise would be a contract this file cannot keep: the browser
 * also branches on mode to pick the valid-ref universe, and `modeTabsStorageKey`
 * (fileExplorerLocalUtils.ts) owns the tab-storage key.
 *
 * Everything else the mode implies goes through `modeCapabilities` (treeRoots.ts),
 * a descriptor the browser derives once. `checkouts` covers everything
 * checkout-shaped — the lens, the checkout listing, branch diffs, git status,
 * search/replace, Quick Open indexing, and the /files/capabilities fetch that
 * gates writing to a checkout. `skills` covers the catalog and the skill
 * capabilities. A section loads and warns about exactly what it declares, so a
 * failure in one section's API cannot raise a notice in another's. Prefer
 * adding a capability there over testing the mode inline.
 *
 * Skills no longer appear in "workspace" or "agent" mode; they live only in the
 * Skills section. SkillsPage importing this barrel is therefore an import of a
 * shared component, not a dependency on the Files section: nothing under
 * components/FileExplorer/ is Files-specific, and views/SkillsPage has no edge
 * to views/FilesPage. The directory keeps its historical name; renaming ~20
 * files and an 1800-line component would move the same code to the same
 * relationships. See the skills-nav-section ticket 03 report for the full
 * argument.
 */
export { WorkspaceFileBrowser } from "./WorkspaceFileBrowser";
