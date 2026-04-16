import { useEffect, useRef } from "react";
import { type Extension, EditorState, Compartment } from "@codemirror/state";
import {
  EditorView,
  keymap,
  placeholder as placeholderExt,
  lineNumbers,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import {
  search,
  searchKeymap,
  openSearchPanel,
  closeSearchPanel,
} from "@codemirror/search";
import styles from "./CodeMirrorEditor.module.css";

import { go } from "@codemirror/lang-go";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { markdown } from "@codemirror/lang-markdown";
import { diff } from "codemirror-lang-diff";

export interface CodeMirrorEditorProps {
  /** Current document content */
  value: string;
  /** Called when the document changes (omit for read-only) */
  onChange?: ((value: string) => void) | undefined;
  /** Language for syntax highlighting (e.g., "go", "json", "yaml", "markdown") */
  language?: string | undefined;
  /** Read-only mode — disables editing but keeps syntax highlighting */
  readOnly?: boolean | undefined;
  /** Placeholder text shown when editor is empty */
  placeholder?: string | undefined;
  /** When true, opens CM6's built-in search panel */
  searchOpen?: boolean | undefined;
  /** Hide line numbers gutter */
  hideLineNumbers?: boolean | undefined;
  /** Additional CSS class for the container */
  className?: string | undefined;
}

function getLanguageExtension(lang?: string): Extension {
  switch (lang) {
    case "go":
      return go();
    case "json":
      return json();
    case "yaml":
    case "yml":
      return yaml();
    case "markdown":
    case "md":
      return markdown();
    case "diff":
      return diff();
    default:
      return [];
  }
}

export function CodeMirrorEditor({
  value,
  onChange,
  language,
  readOnly,
  placeholder,
  searchOpen,
  hideLineNumbers,
  className,
}: CodeMirrorEditorProps): JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const langCompartmentRef = useRef(new Compartment());
  const readOnlyCompartmentRef = useRef(new Compartment());
  const lineNumbersCompartmentRef = useRef(new Compartment());

  // Sync onChange to ref to avoid stale closures
  const onChangeRef = useRef(onChange);
  useEffect(() => {
    onChangeRef.current = onChange;
  }, [onChange]);

  // Create EditorView on mount, destroy on unmount
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const langCompartment = langCompartmentRef.current;
    const readOnlyCompartment = readOnlyCompartmentRef.current;
    const lineNumbersCompartment = lineNumbersCompartmentRef.current;

    const state = EditorState.create({
      doc: value,
      extensions: [
        lineNumbersCompartment.of(hideLineNumbers ? [] : lineNumbers()),
        history(),
        search(),
        keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap]),
        langCompartment.of(getLanguageExtension(language)),
        readOnlyCompartment.of(EditorState.readOnly.of(readOnly ?? false)),
        placeholder ? placeholderExt(placeholder) : [],
        EditorView.updateListener.of((update) => {
          if (update.docChanged && onChangeRef.current) {
            onChangeRef.current(update.state.doc.toString());
          }
        }),
        EditorView.theme({
          "&": {
            height: "100%",
            fontSize: "var(--font-size-sm)",
            fontFamily: "var(--font-family-mono)",
          },
          ".cm-content": {
            caretColor: "var(--text-primary)",
            color: "var(--text-primary)",
          },
          ".cm-activeLine": {
            backgroundColor: "var(--bg-card)",
          },
          "&.cm-focused .cm-selectionBackground, .cm-selectionBackground": {
            backgroundColor:
              "color-mix(in srgb, var(--color-primary) 20%, transparent)",
          },
        }),
      ],
    });

    const view = new EditorView({ state, parent: container });
    viewRef.current = view;

    // ResizeObserver — debounced
    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        if (viewRef.current) {
          viewRef.current.requestMeasure();
        }
      }, 100);
    });
    observer.observe(container);

    return () => {
      observer.disconnect();
      clearTimeout(resizeTimer);
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []); // Mount once — prop changes handled by separate effects

  // Sync external value changes
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const currentDoc = view.state.doc.toString();
    if (value !== currentDoc) {
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: value },
      });
    }
  }, [value]);

  // Sync language changes via compartment reconfiguration
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: langCompartmentRef.current.reconfigure(
        getLanguageExtension(language),
      ),
    });
  }, [language]);

  // Sync readOnly changes via compartment
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: readOnlyCompartmentRef.current.reconfigure(
        EditorState.readOnly.of(readOnly ?? false),
      ),
    });
  }, [readOnly]);

  // Sync hideLineNumbers changes via compartment
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: lineNumbersCompartmentRef.current.reconfigure(
        hideLineNumbers ? [] : lineNumbers(),
      ),
    });
  }, [hideLineNumbers]);

  // Open/close search panel from prop
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    if (searchOpen) {
      openSearchPanel(view);
    } else {
      closeSearchPanel(view);
    }
  }, [searchOpen]);

  const rootClassName = [styles.container, className].filter(Boolean).join(" ");

  return (
    <div
      ref={containerRef}
      className={rootClassName}
      data-readonly={readOnly || undefined}
      data-testid="codemirror-editor"
    />
  );
}
