import { createPortal } from "react-dom";
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
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
  const [position, setPosition] = useState<{
    top: number;
    left: number;
    flip?: "top" | "bottom";
  }>({ top: 0, left: 0 });
  const anchorEl = useRef<HTMLElement | null>(null);
  const tooltipEl = useRef<HTMLSpanElement | null>(null);
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

  // A tooltip that would run past the right edge (the mobile bottom rail)
  // goes above its anchor instead, or below when there is no room above,
  // kept inside the viewport.
  useLayoutEffect(() => {
    const tip = tooltipEl.current;
    const anchor = anchorEl.current;
    if (!visible || position.flip || !tip || !anchor) return;
    const vw = window.innerWidth;
    const box = tip.getBoundingClientRect();
    const width = Math.min(box.width, vw - 8);
    if (position.left + width <= vw - 4) return;
    const rect = anchor.getBoundingClientRect();
    const centered = rect.left + rect.width / 2 - width / 2;
    const below = rect.top - 8 - box.height < 4;
    setPosition({
      top: below ? rect.bottom + 8 : rect.top - 8,
      left: Math.max(4, Math.min(centered, vw - width - 4)),
      flip: below ? "bottom" : "top",
    });
  }, [visible, position]);

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
          ref={tooltipEl}
          id={tooltipId}
          className={tooltipStyles.tooltipPortal}
          data-placement={position.flip}
          role="tooltip"
          style={{
            top: `${position.top}px`,
            left: `${position.left}px`,
            // Wraps only a label wider than the viewport (width: max-content).
            maxWidth: "calc(100vw - 8px)",
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
