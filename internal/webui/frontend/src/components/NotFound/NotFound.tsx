/**
 * NotFound — 404 route component.
 * Shows a simple "Page not found" message with a link back to root.
 */

import { Link } from "react-router-dom";

export function NotFound() {
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        height: "100vh",
        gap: "1rem",
        color: "var(--text-secondary, #666)",
      }}
    >
      <h1 style={{ fontSize: "2rem", margin: 0 }}>404</h1>
      <p style={{ margin: 0 }}>Page not found</p>
      <Link to="/" style={{ color: "var(--accent, #0066cc)" }}>
        Go to dashboard
      </Link>
    </div>
  );
}
