// Ported from T3 Code apps/web/src/components/chat/CompactComposerControlsMenu.tsx
// at commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: holds only the traits (effort) content;
// T3's plan/build mode and access (runtime mode) groups are left out (plan/
// build is excluded from the port; runtime mode is not in this ticket); a
// CSS-module menu in place of Base UI's Menu.

import { memo, type ReactNode } from "react";
import { useComposerPopover } from "./ComposerControl";
import styles from "./ModelPicker.module.css";

/**
 * The narrow composer's "More composer controls" menu: the traits menu
 * content, behind an ellipsis button. children is a render function that
 * gets close, so a pick closes the menu.
 */
export const CompactComposerControlsMenu = memo(
  function CompactComposerControlsMenu(props: {
    disabledReason?: string | null;
    children: (close: () => void) => ReactNode;
  }) {
    const { open, setOpen, rootRef } = useComposerPopover();
    const disabled = Boolean(props.disabledReason);
    return (
      <div className={styles.popoverRoot} ref={rootRef}>
        <button
          type="button"
          className={styles.compactButton}
          aria-label="More composer controls"
          aria-haspopup="menu"
          aria-expanded={open}
          disabled={disabled}
          title={props.disabledReason ?? undefined}
          onClick={() => setOpen(!open)}
        >
          <svg aria-hidden viewBox="0 0 24 24" fill="currentColor">
            <circle cx="5" cy="12" r="1.8" />
            <circle cx="12" cy="12" r="1.8" />
            <circle cx="19" cy="12" r="1.8" />
          </svg>
        </button>
        {open && !disabled && (
          <div className={styles.menu} role="menu">
            {props.children(() => setOpen(false))}
          </div>
        )}
      </div>
    );
  },
);
