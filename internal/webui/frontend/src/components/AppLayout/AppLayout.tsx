/**
 * AppLayout component - top-level layout wrapper.
 * Provides a consistent structure with fixed header and main content area.
 */

import { useEffect, useRef } from "react";
import type { ReactNode } from "react";

import { LiveRegion } from "@/components/LiveRegion/LiveRegion";

import styles from "./AppLayout.module.css";

/**
 * Props for the AppLayout component.
 */
export interface AppLayoutProps {
  /** Main content to render in the content area */
  children: ReactNode;
  /** Optional left navigation rail */
  navRail?: ReactNode;
  /** Optional element to render in the header navigation area (center) */
  navigation?: ReactNode;
  /** Optional element to render in the header actions area (right) */
  actions?: ReactNode;
  /** Optional element to render in the left sidebar */
  sidebar?: ReactNode;
  /**
   * On a phone the sidebar is hidden; when this is set it shows as a drawer
   * instead (MOB2), opened by the control with aria-controls="agents-drawer".
   */
  sidebarOpen?: boolean;
  /** Close the phone drawer: Escape or a tap outside it. */
  onSidebarClose?: () => void;
  /**
   * Optional full-width notice rendered between the header and the content
   * (e.g. the claim-hold banner). Passed in rather than imported so this
   * component stays purely presentational — check:arch enforces that.
   */
  banner?: ReactNode;
  /** Application title displayed in header (defaults to "Loom") */
  title?: ReactNode;
  /** When set, the brand/title becomes a home button. */
  onTitleClick?: () => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * AppLayout provides the top-level structure for the application.
 * Includes a sticky header with title, navigation, and actions slots,
 * and a scrollable main content area.
 */
export function AppLayout({
  children,
  navRail,
  navigation,
  actions,
  sidebar,
  sidebarOpen = false,
  onSidebarClose,
  banner,
  title = "Loom",
  onTitleClick,
  className,
}: AppLayoutProps): JSX.Element {
  // Closing gives focus back to the control that opened the drawer.
  const close = (): void => {
    onSidebarClose?.();
    document
      .querySelector<HTMLElement>('[aria-controls="agents-drawer"]')
      ?.focus();
  };
  const closeRef = useRef(close);
  closeRef.current = close;
  useEffect(() => {
    if (!sidebarOpen) return;
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === "Escape") closeRef.current();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [sidebarOpen]);

  const rootClassName = className
    ? `${styles.appLayout} ${className}`
    : styles.appLayout;

  return (
    <div className={rootClassName}>
      <LiveRegion />
      <a href="#main-content" className={styles.skipLink}>
        Skip to main content
      </a>
      <header className={styles.header} role="banner">
        <div className={styles.headerContent}>
          <div className={styles.brand}>
            {onTitleClick ? (
              <button
                type="button"
                className={styles.brandButton}
                onClick={onTitleClick}
                aria-label="Go home"
              >
                <h1 className={styles.title}>{title}</h1>
              </button>
            ) : (
              <h1 className={styles.title}>{title}</h1>
            )}
          </div>
          {navigation && (
            <nav className={styles.navigation} aria-label="Main navigation">
              {navigation}
            </nav>
          )}
          {actions && <div className={styles.actions}>{actions}</div>}
        </div>
      </header>
      {banner}
      <div className={styles.contentWrapper}>
        {navRail}
        {sidebar && (
          <aside
            id="agents-drawer"
            className={styles.sidebarSlot}
            data-open={sidebarOpen || undefined}
            aria-label={sidebarOpen ? "Agents" : undefined}
          >
            {sidebar}
          </aside>
        )}
        {sidebar && sidebarOpen && (
          <button
            type="button"
            className={styles.drawerScrim}
            aria-label="Close agents"
            tabIndex={-1}
            onClick={close}
          />
        )}
        <main className={styles.main} role="main" id="main-content">
          {children}
        </main>
      </div>
    </div>
  );
}
