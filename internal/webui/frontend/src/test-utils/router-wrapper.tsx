/**
 * Test utility: wraps components/hooks in a MemoryRouter for React Router context.
 */
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";

export function RouterWrapper({ children }: { children: ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>;
}
