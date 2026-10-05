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
  const [position, setPosition] = useState({
    top: 0,
    left: 0,
    above: false,
  });
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
      above: false,
    });
  }, []);

  // A tooltip that would run past the right edge (the mobile bottom rail)
  // goes above its anchor instead, kept inside the viewport.
  useLayoutEffect(() => {
    const tip = tooltipEl.current;
    const anchor = anchorEl.current;
    if (!visible || position.above || !tip || !anchor) return;
    const vw = window.innerWidth;
    const width = tip.getBoundingClientRect().width;
    if (position.left + width <= vw - 4) return;
    const rect = anchor.getBoundingClientRect();
    const centered = rect.left + rect.width / 2 - width / 2;
    setPosition({
      top: rect.top - 8,
      left: Math.max(4, Math.min(centered, vw - width - 4)),
      above: true,
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
          data-placement={position.above ? "top" : undefined}
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
