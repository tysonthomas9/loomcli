import type {
  ComponentPropsWithoutRef,
  ElementType,
  MutableRefObject,
  ReactNode,
  Ref,
} from "react";
import { useCallback } from "react";

import tooltipStyles from "./CompactRailTooltip.module.css";
import { useCompactRailTooltip } from "./useCompactRailTooltip";

function assignRef<T>(ref: Ref<T> | undefined, value: T | null): void {
  if (!ref) return;
  if (typeof ref === "function") {
    ref(value);
    return;
  }
  (ref as MutableRefObject<T | null>).current = value;
}

type CompactRailHostProps<T extends ElementType> = {
  as?: T;
  label: string;
  children: ReactNode;
  className?: string | undefined;
  hostRef?: Ref<HTMLElement> | undefined;
} & Omit<
  ComponentPropsWithoutRef<T>,
  "as" | "label" | "children" | "className" | "hostRef"
>;

export function CompactRailHost<T extends ElementType = "span">({
  as,
  label,
  children,
  className,
  hostRef,
  onMouseEnter,
  onMouseLeave,
  onFocus,
  onBlur,
  ...rest
}: CompactRailHostProps<T>): JSX.Element {
  const Component = as ?? "span";
  const { anchorRef, tooltipProps, tooltipPortal, tooltipId, visible } =
    useCompactRailTooltip(label);

  const mergedRef = useCallback(
    (node: HTMLElement | null) => {
      anchorRef(node);
      assignRef(hostRef, node);
    },
    [anchorRef, hostRef],
  );

  return (
    <>
      <Component
        ref={mergedRef}
        className={[tooltipStyles.host, className].filter(Boolean).join(" ")}
        aria-label={label}
        aria-describedby={visible ? tooltipId : undefined}
        {...rest}
        onMouseEnter={(event) => {
          tooltipProps.onMouseEnter();
          onMouseEnter?.(event);
        }}
        onMouseLeave={(event) => {
          tooltipProps.onMouseLeave();
          onMouseLeave?.(event);
        }}
        onFocus={(event) => {
          tooltipProps.onFocus();
          onFocus?.(event);
        }}
        onBlur={(event) => {
          tooltipProps.onBlur();
          onBlur?.(event);
        }}
      >
        {children}
      </Component>
      {tooltipPortal}
    </>
  );
}

export { tooltipStyles as compactRailTooltipStyles };
