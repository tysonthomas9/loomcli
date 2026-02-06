/**
 * AppLayout component - top-level layout wrapper.
 * Provides a consistent structure with fixed header and main content area.
 */

import type { ReactNode } from 'react';

import styles from './AppLayout.module.css';

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
  /** Application title displayed in header (defaults to "Beads") */
  title?: string;
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
  title = 'Beads',
  className,
}: AppLayoutProps): JSX.Element {
  const rootClassName = className ? `${styles.appLayout} ${className}` : styles.appLayout;

  return (
    <div className={rootClassName}>
      <a href="#main-content" className={styles.skipLink}>
        Skip to main content
      </a>
      <header className={styles.header} role="banner">
        <div className={styles.headerContent}>
          <div className={styles.brand}>
            <h1 className={styles.title}>{title}</h1>
          </div>
          {navigation && (
            <nav className={styles.navigation} aria-label="Main navigation">
              {navigation}
            </nav>
          )}
          {actions && <div className={styles.actions}>{actions}</div>}
        </div>
      </header>
      <div className={styles.contentWrapper}>
        {navRail}
        {sidebar}
        <main className={styles.main} role="main" id="main-content">
          {children}
        </main>
      </div>
    </div>
  );
}
