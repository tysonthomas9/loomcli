import type { CheckoutRef } from "@/utils/fileExplorerRefs";

import { RepairCheckoutConfirmDialog } from "../FileExplorerDialogs";
import styles from "../FileExplorer.module.css";
import type {
  CheckoutRepairMenuState,
  RepairConfirmState,
} from "../workspaceFileBrowserTypes";

export function CheckoutRepairOverlays({
  canWrite,
  menu,
  confirm,
  onCloseMenu,
  onCloseConfirm,
  onRepair,
}: {
  canWrite: boolean;
  menu: CheckoutRepairMenuState | null;
  confirm: RepairConfirmState | null;
  onCloseMenu: () => void;
  onCloseConfirm: () => void;
  onRepair: (ref: CheckoutRef, label: string, force?: boolean) => void;
}) {
  if (!canWrite) return null;
  return (
    <>
      {menu && (
        <div
          className={styles.contextMenu}
          style={{ left: menu.x, top: menu.y }}
          role="menu"
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              onCloseMenu();
              onRepair(menu.ref, menu.label);
            }}
          >
            Repair checkout
          </button>
        </div>
      )}
      {confirm && (
        <RepairCheckoutConfirmDialog
          label={confirm.label}
          onCancel={onCloseConfirm}
          onConfirm={() => {
            onCloseConfirm();
            onRepair(confirm.ref, confirm.label, true);
          }}
        />
      )}
    </>
  );
}
