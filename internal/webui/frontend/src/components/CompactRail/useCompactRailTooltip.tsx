import { createPortal } from "react-dom";
import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactPortal,
} from "react";

import tooltipStyles from "./CompactRailTooltip.module.css";

export function useCompactRailTooltip(label: string): {
  anchorRef: (node: HTMLElement | null) => void;
  tooltipProps: {
    onMouseEnter: () => void;
    onMouseLeave: () => void;
    onFocus: () => void;
    onBlur: () => void;
  };
  tooltipPortal: ReactPortal | null;
  tooltipId: string;
  visible: boolean;
} {
  const [visible, setVisible] = useState(false);
  const [position, setPosition] = useState({ top: 0, left: 0 });
  const anchorEl = useRef<HTMLElement | null>(null);
  const tooltipId = useId();

  const anchorRef = useCallback((node: HTMLElement | null) => {
    anchorEl.current = node;
  }, []);

  const updatePosition = useCallback(() => {
    const el = anchorEl.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    setPosition({
      top: rect.top + rect.height / 2,
      left: rect.right + 8,
    });
  }, []);

  const show = useCallback(() => {
    updatePosition();
    setVisible(true);
  }, [updatePosition]);

  const hide = useCallback(() => {
    setVisible(false);
  }, []);

  useEffect(() => {
    if (!visible) return;
    const reposition = () => updatePosition();
    window.addEventListener("scroll", reposition, true);
    window.addEventListener("resize", reposition);
    return () => {
      window.removeEventListener("scroll", reposition, true);
      window.removeEventListener("resize", reposition);
    };
  }, [visible, updatePosition]);

  const tooltipPortal = visible
    ? createPortal(
        <span
          id={tooltipId}
          className={tooltipStyles.tooltipPortal}
          role="tooltip"
          style={{
            top: `${position.top}px`,
            left: `${position.left}px`,
          }}
        >
          {label}
        </span>,
        document.body,
      )
    : null;

  return {
    anchorRef,
    tooltipProps: {
      onMouseEnter: show,
      onMouseLeave: hide,
      onFocus: show,
      onBlur: hide,
    },
    tooltipPortal,
    tooltipId,
    visible,
  };
}
