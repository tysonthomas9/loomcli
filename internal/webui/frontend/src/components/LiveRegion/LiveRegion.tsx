/**
 * LiveRegion component.
 * Singleton that renders global aria-live containers for dynamic announcements.
 * Mounted once in AppLayout.
 */

import { useState, useEffect } from "react";

import { onAnnounce } from "@/hooks/ui";

/**
 * LiveRegion renders hidden aria-live regions (polite + assertive).
 * Listens for announce events and updates the appropriate region.
 */
export function LiveRegion(): JSX.Element {
  const [politeMessage, setPoliteMessage] = useState("");
  const [assertiveMessage, setAssertiveMessage] = useState("");

  useEffect(() => {
    const unsubscribe = onAnnounce(({ message, priority }) => {
      if (priority === "assertive") {
        setAssertiveMessage("");
        // Clear then set to ensure screen readers pick up repeated messages
        requestAnimationFrame(() => setAssertiveMessage(message));
      } else {
        setPoliteMessage("");
        requestAnimationFrame(() => setPoliteMessage(message));
      }
    });

    return () => {
      unsubscribe();
    };
  }, []);

  const srOnly: React.CSSProperties = {
    position: "absolute",
    width: "1px",
    height: "1px",
    padding: 0,
    margin: "-1px",
    overflow: "hidden",
    clip: "rect(0, 0, 0, 0)",
    whiteSpace: "nowrap",
    border: 0,
  };

  return (
    <>
      <div
        role="status"
        aria-live="polite"
        aria-atomic="true"
        style={srOnly}
        data-testid="live-region-polite"
      >
        {politeMessage}
      </div>
      <div
        aria-live="assertive"
        aria-atomic="true"
        style={srOnly}
        data-testid="live-region-assertive"
      >
        {assertiveMessage}
      </div>
    </>
  );
}
