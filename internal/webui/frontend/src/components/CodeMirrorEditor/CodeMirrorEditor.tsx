import { useCallback, useEffect, useRef } from "react";
import {
  type Extension,
  EditorState,
  Compartment,
  RangeSetBuilder,
} from "@codemirror/state";
import {
  EditorView,
  keymap,
  placeholder as placeholderExt,
  lineNumbers,
  gutter,
  GutterMarker,
  Decoration,
  ViewPlugin,
  WidgetType,
  type DecorationSet,
  type ViewUpdate,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import {
  search,
  searchKeymap,
  openSearchPanel,
  closeSearchPanel,
  gotoLine,
} from "@codemirror/search";
import {
  StreamLanguage,
  syntaxHighlighting,
  HighlightStyle,
} from "@codemirror/language";
import { tags as t } from "@lezer/highlight";
import styles from "./CodeMirrorEditor.module.css";

// Token colors come from CSS variables (defined per-theme in the module CSS)
// so highlighting adapts to light/dark. Without an explicit highlight style the
// language parsers run but produce no colors — see CodeMirrorEditor.module.css.
const highlightStyle = HighlightStyle.define([
  {
    tag: [
      t.keyword,
      t.modifier,
      t.controlKeyword,
      t.operatorKeyword,
      t.definitionKeyword,
      t.moduleKeyword,
    ],
    color: "var(--cm-keyword)",
  },
  { tag: [t.string, t.special(t.string), t.regexp], color: "var(--cm-string)" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "var(--cm-number)" },
  {
    tag: [t.comment, t.lineComment, t.blockComment, t.docComment],
    color: "var(--cm-comment)",
    fontStyle: "italic",
  },
  {
    tag: [t.function(t.variableName), t.function(t.propertyName), t.labelName],
    color: "var(--cm-function)",
  },
  { tag: [t.typeName, t.className, t.namespace], color: "var(--cm-type)" },
  { tag: [t.propertyName, t.attributeName], color: "var(--cm-property)" },
  { tag: [t.tagName], color: "var(--cm-tag)" },
  {
    tag: [t.heading, t.heading1, t.heading2, t.heading3],
    color: "var(--cm-heading)",
    fontWeight: "bold",
  },
  {
    tag: [t.link, t.url],
    color: "var(--cm-link)",
    textDecoration: "underline",
  },
  { tag: [t.meta, t.processingInstruction], color: "var(--cm-meta)" },
  { tag: [t.invalid], color: "var(--cm-invalid)" },
]);

import { go } from "@codemirror/lang-go";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { markdown } from "@codemirror/lang-markdown";
import { javascript } from "@codemirror/lang-javascript";
import { css } from "@codemirror/lang-css";
import { html } from "@codemirror/lang-html";
import { diff } from "codemirror-lang-diff";
import {
  symbolStateFromEditor,
  type FileSymbolState,
} from "@/utils/lezerSymbols";

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
  /** One-based line number to reveal. */
  scrollToLine?: number | undefined;
  /** Change token used to repeat a scroll to the same line. */
  scrollToLineKey?: number | string | undefined;
  /** Called after a requested line has been revealed. */
  onScrollToLineApplied?: (() => void) | undefined;
  /** Emits in-file lezer symbols and the cursor-position symbol trail. */
  onSymbolsChange?: ((state: FileSymbolState) => void) | undefined;
  /** Quick-diff gutter marks against the visible editor's base revision. */
  gitGutterMarks?: GitGutterLineMark[] | undefined;
  /** Parsed git blame blocks used for active-line inline annotation. */
  blameLines?: CodeMirrorBlameLine[] | undefined;
  /** Enables active-line blame annotation. */
  blameEnabled?: boolean | undefined;
  /** Called when a blame annotation is clicked. */
  onBlameCommitClick?: ((sha: string) => void) | undefined;
  /** Additional CSS class for the container */
  className?: string | undefined;
}

export type GitGutterMarkKind = "added" | "changed" | "deleted";

export interface GitGutterLineMark {
  line: number;
  kind: GitGutterMarkKind;
}

export interface CodeMirrorBlameLine {
  line: number;
  lines: number;
  sha: string;
  author: string;
  time: string;
  summary: string;
}

class GitDiffMarker extends GutterMarker {
  elementClass: string;

  constructor(readonly kind: GitGutterMarkKind) {
    super();
    this.elementClass = `cm-git-gutter-line cm-git-gutter-line-${kind}`;
  }

  eq(other: GutterMarker): boolean {
    return other instanceof GitDiffMarker && other.kind === this.kind;
  }

  toDOM(): Node {
    const el = document.createElement("span");
    el.className = `cm-git-gutter-dot cm-git-gutter-dot-${this.kind}`;
    el.title =
      this.kind === "added"
        ? "Added"
        : this.kind === "deleted"
          ? "Deleted"
          : "Changed";
    return el;
  }
}

const gitDiffMarkers: Record<GitGutterMarkKind, GitDiffMarker> = {
  added: new GitDiffMarker("added"),
  changed: new GitDiffMarker("changed"),
  deleted: new GitDiffMarker("deleted"),
};

function gitGutterExtension(marks: GitGutterLineMark[] | undefined): Extension {
  if (!marks || marks.length === 0) return [];
  const byLine = new Map<number, GitGutterMarkKind>();
  for (const mark of marks) {
    if (mark.line > 0) byLine.set(mark.line, mark.kind);
  }
  return [
    gutter({
      class: "cm-git-gutter",
      renderEmptyElements: false,
      lineMarker: (view, line) => {
        const lineNo = view.state.doc.lineAt(line.from).number;
        const kind = byLine.get(lineNo);
        return kind ? gitDiffMarkers[kind] : null;
      },
      lineMarkerChange: () => true,
    }),
    EditorView.baseTheme({
      ".cm-git-gutter": {
        width: "6px",
      },
      ".cm-git-gutter-dot": {
        display: "block",
        width: "3px",
        height: "14px",
        margin: "0 auto",
        borderRadius: "2px",
      },
      ".cm-git-gutter-dot-added": {
        backgroundColor: "#2f855a",
      },
      ".cm-git-gutter-dot-changed": {
        backgroundColor: "#b7791f",
      },
      ".cm-git-gutter-dot-deleted": {
        backgroundColor: "var(--color-danger)",
      },
    }),
  ];
}

function blameForLine(
  blameLines: CodeMirrorBlameLine[],
  line: number,
): CodeMirrorBlameLine | null {
  return (
    blameLines.find((entry) => {
      const end = entry.line + Math.max(1, entry.lines) - 1;
      return line >= entry.line && line <= end;
    }) ?? null
  );
}

class BlameWidget extends WidgetType {
  constructor(
    readonly entry: CodeMirrorBlameLine,
    readonly onClick?: ((sha: string) => void) | undefined,
  ) {
    super();
  }

  eq(other: WidgetType): boolean {
    return other instanceof BlameWidget && other.entry.sha === this.entry.sha;
  }

  toDOM(): HTMLElement {
    const el = document.createElement("button");
    el.type = "button";
    el.className = "cm-blame-inline";
    el.textContent = `${this.entry.author || "Unknown"} · ${this.entry.summary || this.entry.sha.slice(0, 8)}`;
    el.title = `${this.entry.sha}\n${this.entry.author}\n${this.entry.time}\n${this.entry.summary}`;
    el.onclick = (event) => {
      event.preventDefault();
      event.stopPropagation();
      this.onClick?.(this.entry.sha);
    };
    return el;
  }
}

function blameExtension(
  enabled: boolean | undefined,
  blameLines: CodeMirrorBlameLine[] | undefined,
  onClick: ((sha: string) => void) | undefined,
): Extension {
  if (!enabled || !blameLines || blameLines.length === 0) return [];
  const plugin = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;

      constructor(view: EditorView) {
        this.decorations = this.build(view);
      }

      update(update: ViewUpdate): void {
        if (
          update.docChanged ||
          update.selectionSet ||
          update.viewportChanged
        ) {
          this.decorations = this.build(update.view);
        }
      }

      build(view: EditorView): DecorationSet {
        const line = view.state.doc.lineAt(view.state.selection.main.head);
        const entry = blameForLine(blameLines, line.number);
        if (!entry) return Decoration.none;
        const builder = new RangeSetBuilder<Decoration>();
        builder.add(
          line.to,
          line.to,
          Decoration.widget({
            widget: new BlameWidget(entry, onClick),
            side: 1,
          }),
        );
        return builder.finish();
      }
    },
    {
      decorations: (value) => value.decorations,
    },
  );
  return [
    plugin,
    EditorView.baseTheme({
      ".cm-blame-inline": {
        marginLeft: "18px",
        border: "none",
        background: "transparent",
        color: "var(--color-text-muted)",
        font: "inherit",
        fontSize: "var(--font-size-xs, 11px)",
        cursor: "pointer",
        opacity: "0.82",
      },
      ".cm-blame-inline:hover": {
        color: "var(--color-text-primary)",
        textDecoration: "underline",
      },
    }),
  ];
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
    case "javascript":
      return javascript();
    case "jsx":
      return javascript({ jsx: true });
    case "typescript":
      return javascript({ typescript: true });
    case "tsx":
      return javascript({ jsx: true, typescript: true });
    case "css":
      return css();
    case "html":
      return html();
    default:
      return [];
  }
}

// Heavier / less-common languages load on demand so they don't bloat the
// editor's main chunk. detectLanguage emits these ids; getLanguageExtension
// returns [] for them until the pack resolves and reconfigures the compartment.
const lazyLanguages: Record<string, () => Promise<Extension>> = {
  python: () => import("@codemirror/lang-python").then((m) => m.python()),
  rust: () => import("@codemirror/lang-rust").then((m) => m.rust()),
  sql: () => import("@codemirror/lang-sql").then((m) => m.sql()),
  xml: () => import("@codemirror/lang-xml").then((m) => m.xml()),
  cpp: () => import("@codemirror/lang-cpp").then((m) => m.cpp()),
  php: () => import("@codemirror/lang-php").then((m) => m.php()),
  shell: () =>
    import("@codemirror/legacy-modes/mode/shell").then((m) =>
      StreamLanguage.define(m.shell),
    ),
  toml: () =>
    import("@codemirror/legacy-modes/mode/toml").then((m) =>
      StreamLanguage.define(m.toml),
    ),
  ini: () =>
    import("@codemirror/legacy-modes/mode/properties").then((m) =>
      StreamLanguage.define(m.properties),
    ),
  dockerfile: () =>
    import("@codemirror/legacy-modes/mode/dockerfile").then((m) =>
      StreamLanguage.define(m.dockerFile),
    ),
};

export function CodeMirrorEditor({
  value,
  onChange,
  language,
  readOnly,
  placeholder,
  searchOpen,
  hideLineNumbers,
  scrollToLine,
  scrollToLineKey,
  onScrollToLineApplied,
  onSymbolsChange,
  gitGutterMarks,
  blameLines,
  blameEnabled,
  onBlameCommitClick,
  className,
}: CodeMirrorEditorProps): JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const langCompartmentRef = useRef(new Compartment());
  const readOnlyCompartmentRef = useRef(new Compartment());
  const lineNumbersCompartmentRef = useRef(new Compartment());
  const gitGutterCompartmentRef = useRef(new Compartment());
  const blameCompartmentRef = useRef(new Compartment());
  const langLoadTokenRef = useRef(0);
  const languageRef = useRef(language);

  // Sync onChange to ref to avoid stale closures
  const onChangeRef = useRef(onChange);
  useEffect(() => {
    onChangeRef.current = onChange;
  }, [onChange]);
  const onSymbolsChangeRef = useRef(onSymbolsChange);
  const emitSymbolState = useCallback((view: EditorView) => {
    onSymbolsChangeRef.current?.(
      symbolStateFromEditor(view.state, languageRef.current),
    );
  }, []);
  useEffect(() => {
    onSymbolsChangeRef.current = onSymbolsChange;
    const view = viewRef.current;
    if (view) emitSymbolState(view);
  }, [emitSymbolState, onSymbolsChange]);
  useEffect(() => {
    languageRef.current = language;
  }, [language]);

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
        gitGutterCompartmentRef.current.of(gitGutterExtension(gitGutterMarks)),
        blameCompartmentRef.current.of(
          blameExtension(blameEnabled, blameLines, onBlameCommitClick),
        ),
        history(),
        search(),
        keymap.of([
          ...defaultKeymap,
          ...historyKeymap,
          ...searchKeymap,
          { key: "Mod-g", run: gotoLine },
          { key: "Ctrl-g", run: gotoLine },
        ]),
        langCompartment.of(getLanguageExtension(language)),
        syntaxHighlighting(highlightStyle),
        readOnlyCompartment.of([
          EditorState.readOnly.of(readOnly ?? false),
          // editable=false drops contenteditable + the textbox role so assistive
          // tech doesn't present a read-only viewer as an editable field.
          EditorView.editable.of(!(readOnly ?? false)),
        ]),
        placeholder ? placeholderExt(placeholder) : [],
        EditorView.updateListener.of((update) => {
          if (update.docChanged && onChangeRef.current) {
            onChangeRef.current(update.state.doc.toString());
          }
          if (update.docChanged || update.selectionSet) {
            emitSymbolState(update.view);
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
    emitSymbolState(view);

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

  // Sync language changes via compartment reconfiguration. Bundled languages
  // apply synchronously; lazy languages apply when their pack resolves, guarded
  // by a token so a newer language change always wins.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    languageRef.current = language;
    const token = ++langLoadTokenRef.current;
    view.dispatch({
      effects: langCompartmentRef.current.reconfigure(
        getLanguageExtension(language),
      ),
    });
    emitSymbolState(view);
    const loader = language ? lazyLanguages[language] : undefined;
    if (loader) {
      loader()
        .then((ext) => {
          const v = viewRef.current;
          if (v && token === langLoadTokenRef.current) {
            v.dispatch({
              effects: langCompartmentRef.current.reconfigure(ext),
            });
            emitSymbolState(v);
          }
        })
        .catch(() => {
          // Pack failed to load — leave as plain text.
        });
    }
  }, [emitSymbolState, language]);

  // Sync readOnly changes via compartment
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: readOnlyCompartmentRef.current.reconfigure([
        EditorState.readOnly.of(readOnly ?? false),
        EditorView.editable.of(!(readOnly ?? false)),
      ]),
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

  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: gitGutterCompartmentRef.current.reconfigure(
        gitGutterExtension(gitGutterMarks),
      ),
    });
  }, [gitGutterMarks]);

  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: blameCompartmentRef.current.reconfigure(
        blameExtension(blameEnabled, blameLines, onBlameCommitClick),
      ),
    });
  }, [blameEnabled, blameLines, onBlameCommitClick]);

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

  useEffect(() => {
    const view = viewRef.current;
    if (!view || !scrollToLine) return;
    const lineNumber = Math.min(
      Math.max(1, Math.floor(scrollToLine)),
      view.state.doc.lines,
    );
    const line = view.state.doc.line(lineNumber);
    view.dispatch({
      selection: { anchor: line.from },
      effects: EditorView.scrollIntoView(line.from, { y: "center" }),
    });
    view.focus();
    onScrollToLineApplied?.();
  }, [onScrollToLineApplied, scrollToLine, scrollToLineKey]);

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
