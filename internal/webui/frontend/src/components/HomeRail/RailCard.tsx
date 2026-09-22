import type { ReactNode } from "react";

import styles from "./HomeRail.module.css";

export interface RailCardProps {
  title: string;
  meta: string;
  testId: string;
  children: ReactNode;
  bodyClassName?: string | undefined;
}

export function RailCard({
  title,
  meta,
  testId,
  children,
  bodyClassName,
}: RailCardProps): JSX.Element {
  return (
    <section className={styles.card} data-testid={testId}>
      <header className={styles.head}>
        <h3>{title}</h3>
        <span>{meta}</span>
      </header>
      <div className={[styles.body, bodyClassName].filter(Boolean).join(" ")}>
        {children}
      </div>
    </section>
  );
}
