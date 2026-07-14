import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { buildDesignHtmlDocument } from "@/utils/designHtmlDocument";
import styles from "./HtmlDesignRenderer.module.css";

export interface HtmlDesignRendererProps {
  content: string;
}

const MIN_FRAME_HEIGHT = 120;

/** Render agent-authored HTML without sharing a CSS or DOM scope with Loom. */
export function HtmlDesignRenderer({
  content,
}: HtmlDesignRendererProps): JSX.Element {
  const frameRef = useRef<HTMLIFrameElement>(null);
  const observerRef = useRef<ResizeObserver | null>(null);
  const [height, setHeight] = useState(MIN_FRAME_HEIGHT);
  const srcDoc = useMemo(() => buildDesignHtmlDocument(content), [content]);

  const disconnectObserver = useCallback(() => {
    observerRef.current?.disconnect();
    observerRef.current = null;
  }, []);

  const handleLoad = useCallback(() => {
    disconnectObserver();

    const frameDocument = frameRef.current?.contentDocument;
    if (!frameDocument) return;

    const updateHeight = () => {
      const nextHeight = Math.max(
        MIN_FRAME_HEIGHT,
        frameDocument.body?.scrollHeight ?? 0,
        frameDocument.documentElement?.scrollHeight ?? 0,
      );
      setHeight(nextHeight);
    };

    updateHeight();
    if (typeof ResizeObserver !== "undefined") {
      const observer = new ResizeObserver(updateHeight);
      observer.observe(frameDocument.documentElement);
      observerRef.current = observer;
    }
  }, [disconnectObserver]);

  useEffect(() => disconnectObserver, [disconnectObserver]);

  return (
    <iframe
      ref={frameRef}
      className={styles.frame}
      data-testid="design-html-content"
      title="HTML design artifact"
      sandbox="allow-same-origin"
      srcDoc={srcDoc}
      style={{ height }}
      onLoad={handleLoad}
    />
  );
}
