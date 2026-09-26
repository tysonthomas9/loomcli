import { useState } from "react";

import { useSkill, useSkillsActions } from "@/hooks";
import { explorerRefKey, type SkillsExplorerRef } from "@/utils/explorerRefs";
import { parseSkillPath, validateSkillDescription } from "@/utils/skillsPaths";

import styles from "../FileExplorer.module.css";

export function SkillMetadataBar({
  workspaceId,
  refInfo,
  path,
  onDelete,
}: {
  workspaceId: string;
  refInfo: SkillsExplorerRef;
  path: string;
  onDelete: (name: string) => void;
}) {
  const parsed = parseSkillPath(path);
  const catalog = useSkill(workspaceId, refInfo, parsed?.skill ?? null);
  const actions = useSkillsActions(workspaceId);
  const [editing, setEditing] = useState(false);
  const [description, setDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const skill = catalog.skill;
  if (!skill || !parsed) return null;
  const canEdit = actions.canEdit(refInfo.group);
  const key = explorerRefKey(refInfo);
  const shadowed = catalog.shadowedByRef[key]?.has(skill.name) ?? false;
  const shadows = catalog.shadowsByRef[key]?.has(skill.name) ?? false;

  const beginEdit = () => {
    setDescription(skill.description);
    setError(null);
    setEditing(true);
  };
  const saveDescription = async () => {
    const validation = validateSkillDescription(description);
    if (validation) {
      setError(validation);
      return;
    }
    try {
      await actions.updateMetadata(refInfo.group, skill.name, description);
      setEditing(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  return (
    <div className={styles.skillMetadataBar}>
      <div className={styles.skillMetadataMain}>
        <strong>{skill.name}</strong>
        <span className={styles.skillScopeChip}>
          {refInfo.group.kind === "workspace"
            ? "Workspace"
            : `Role: ${refInfo.group.role}`}
        </span>
        {shadowed && <span className={styles.nodeAnnotation}>overridden</span>}
        {shadows && (
          <span className={styles.nodeAnnotation}>overrides workspace</span>
        )}
        {editing ? (
          <span className={styles.skillDescriptionEditor}>
            <input
              value={description}
              aria-label="Skill description"
              onChange={(event) => setDescription(event.target.value)}
            />
            <button type="button" onClick={() => void saveDescription()}>
              Save description
            </button>
            <button type="button" onClick={() => setEditing(false)}>
              Cancel
            </button>
          </span>
        ) : (
          <span className={styles.skillDescription}>{skill.description}</span>
        )}
      </div>
      <div className={styles.skillProvenance}>
        <span>Created by {skill.created_by || "unknown"}</span>
        <span>Source {skill.source || "unknown"}</span>
        <span>Updated {new Date(skill.updated_at).toLocaleString()}</span>
        {canEdit ? (
          <>
            <button type="button" onClick={beginEdit}>
              Edit description
            </button>
            <button type="button" onClick={() => onDelete(skill.name)}>
              Delete skill
            </button>
          </>
        ) : (
          <span title="Use `loom skill update` to edit workspace skills">
            Read-only · use `loom skill update`
          </span>
        )}
      </div>
      {error && <div className={styles.dialogError}>{error}</div>}
      <div className={styles.skillConcurrencyHint}>
        Multi-file saves are serialized in this browser. Concurrent external
        saves can still rewrite a sibling file; review refreshed siblings after
        saving.
      </div>
    </div>
  );
}
