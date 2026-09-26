/**
 * Terminal tab metadata helpers.
 *
 * Lives in utils (not next to the terminal components) so non-component
 * consumers — hooks in particular, which may not import from components — can
 * classify server tab metadata. Re-exported from
 * components/TerminalView/tabs/terminalTabUtils to preserve the tabs barrel
 * surface.
 */

/**
 * True when persisted metadata describes an agent harness PTY.
 * The session-name prefix is a fallback for legacy sessions persisted before
 * kind/agent_id existed; the user-editable label is deliberately NOT
 * consulted, so renaming a plain tab to "agent-…" can't reclassify it.
 */
export function isAgentMetadata(meta: {
  kind?: string;
  agent_id?: string;
  session_name?: string;
}): boolean {
  return (
    meta.kind === "agent" ||
    (meta.agent_id != null && meta.agent_id !== "") ||
    (meta.session_name?.startsWith("agent-") ?? false)
  );
}
