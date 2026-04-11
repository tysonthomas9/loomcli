import { useState, useCallback } from "react";

import type {
  ContextMenuEvent,
  TerminalInstanceHandle,
} from "@/components/TerminalView/instances";

interface UseContextMenuActionsOptions {
  instanceRefs: React.MutableRefObject<Map<string, TerminalInstanceHandle>>;
  activeTabId: string;
  handleCopyNotify: () => void;
  handlePasteRequest: () => void;
}

interface UseContextMenuActionsReturn {
  contextMenu: ContextMenuEvent | null;
  handleContextMenu: (event: ContextMenuEvent) => void;
  handleContextMenuClose: () => void;
  handleContextMenuCopy: () => void;
  handleContextMenuPaste: () => void;
  handleContextMenuSelectAll: () => void;
}

export function useContextMenuActions({
  instanceRefs,
  activeTabId,
  handleCopyNotify,
  handlePasteRequest,
}: UseContextMenuActionsOptions): UseContextMenuActionsReturn {
  const [contextMenu, setContextMenu] = useState<ContextMenuEvent | null>(null);

  const handleContextMenu = useCallback((event: ContextMenuEvent) => {
    setContextMenu(event);
  }, []);

  const handleContextMenuClose = useCallback(() => {
    setContextMenu(null);
  }, []);

  const handleContextMenuCopy = useCallback(() => {
    const instance = instanceRefs.current.get(activeTabId);
    if (instance) {
      const sel = instance.getSelection();
      if (sel) {
        navigator.clipboard
          .writeText(sel)
          .then(() => handleCopyNotify())
          .catch(() => {});
      }
    }
    setContextMenu(null);
    instanceRefs.current.get(activeTabId)?.focus();
  }, [activeTabId, handleCopyNotify, instanceRefs]);

  const handleContextMenuPaste = useCallback(() => {
    setContextMenu(null);
    handlePasteRequest();
  }, [handlePasteRequest]);

  const handleContextMenuSelectAll = useCallback(() => {
    instanceRefs.current.get(activeTabId)?.selectAll();
    setContextMenu(null);
  }, [activeTabId, instanceRefs]);

  return {
    contextMenu,
    handleContextMenu,
    handleContextMenuClose,
    handleContextMenuCopy,
    handleContextMenuPaste,
    handleContextMenuSelectAll,
  };
}
