import { useCallback, useState } from "react";

import { isDesktopRuntime, pickDesktopFolder } from "@/api/common";

export interface UseFolderPickerOptions {
  disabled?: boolean;
  onPick: (path: string) => void;
  onStart?: () => void;
  onError?: (message: string) => void;
}

export interface UseFolderPickerReturn {
  canBrowseFolders: boolean;
  isBrowsing: boolean;
  browseFolder: () => Promise<void>;
}

export function useFolderPicker({
  disabled = false,
  onPick,
  onStart,
  onError,
}: UseFolderPickerOptions): UseFolderPickerReturn {
  const [isBrowsing, setIsBrowsing] = useState(false);
  const canBrowseFolders = isDesktopRuntime();

  const browseFolder = useCallback(async () => {
    if (!canBrowseFolders || disabled || isBrowsing) return;

    setIsBrowsing(true);
    onStart?.();
    try {
      const selectedPath = await pickDesktopFolder();
      if (selectedPath) {
        onPick(selectedPath);
      }
    } catch (err) {
      onError?.(
        err instanceof Error ? err.message : "Failed to open folder picker",
      );
    } finally {
      setIsBrowsing(false);
    }
  }, [canBrowseFolders, disabled, isBrowsing, onError, onPick, onStart]);

  return { canBrowseFolders, isBrowsing, browseFolder };
}
