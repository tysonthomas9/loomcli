import type { SkillsExplorerRef } from "@/utils/explorerRefs";
import {
  useFileBrowserStoreInstance,
  useFileDocumentRegistry,
  useSkillsActions,
  useToast,
  useWorkspaceContext,
} from "@/hooks";
import { SKILL_MD } from "@/utils/skillsPaths";

import styles from "../FileExplorer.module.css";
import type {
  DeleteSkillState,
  SkillGroupMenuState,
} from "../workspaceFileBrowserTypes";
import { DeleteSkillConfirmDialog, NewSkillDialog } from "./NewSkillDialog";

export function SkillsBrowserOverlays({
  menu,
  newSkillRef,
  deleteSkill,
  canEdit,
  onChooseNew,
  onCloseMenu,
  onCancelNew,
  onCancelDelete,
  onSkillCreated,
  onSkillDeleted,
}: {
  menu: SkillGroupMenuState | null;
  newSkillRef: SkillsExplorerRef | null;
  deleteSkill: DeleteSkillState | null;
  canEdit: (ref: SkillsExplorerRef) => boolean;
  onChooseNew: (ref: SkillsExplorerRef) => void;
  onCloseMenu: () => void;
  onCancelNew: () => void;
  onCancelDelete: () => void;
  onSkillCreated: (ref: SkillsExplorerRef, path: string) => void;
  onSkillDeleted: () => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const actions = useSkillsActions(workspaceId);
  const registry = useFileDocumentRegistry();
  const store = useFileBrowserStoreInstance();
  const { showToast } = useToast();

  const create = async (
    ref: SkillsExplorerRef,
    input: { name: string; description: string },
  ) => {
    try {
      await actions.createSkill(ref.group, input);
      onSkillCreated(ref, `${input.name}/${SKILL_MD}`);
    } catch (error) {
      showToast(error instanceof Error ? error.message : String(error), {
        type: "error",
      });
      throw error;
    }
  };
  const remove = (ref: SkillsExplorerRef, name: string) => {
    void actions
      .deleteSkill(ref.group, name)
      .then(() => {
        registry.resetPathPrefix(workspaceId, ref, name);
        store.getState().closePathPrefix(ref, name);
        onSkillDeleted();
        showToast("Skill deleted", { type: "success" });
      })
      .catch((error) =>
        showToast(error instanceof Error ? error.message : String(error), {
          type: "error",
        }),
      );
  };

  return (
    <>
      {menu && canEdit(menu.ref) && (
        <div
          className={styles.contextMenu}
          style={{ left: menu.x, top: menu.y }}
          role="menu"
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              onChooseNew(menu.ref);
              onCloseMenu();
            }}
          >
            New skill
          </button>
        </div>
      )}
      {newSkillRef && canEdit(newSkillRef) && (
        <NewSkillDialog
          group={newSkillRef.group}
          onCancel={onCancelNew}
          onConfirm={(input) => create(newSkillRef, input)}
        />
      )}
      {deleteSkill && canEdit(deleteSkill.ref) && (
        <DeleteSkillConfirmDialog
          name={deleteSkill.name}
          onCancel={onCancelDelete}
          onConfirm={() => remove(deleteSkill.ref, deleteSkill.name)}
        />
      )}
    </>
  );
}
