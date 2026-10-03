// Code-block highlighting for ChatMarkdown with the Lezer parsers Loom's
// editor already ships (T3 Code uses Shiki, which Loom does not carry).
// Languages load on first use; an unknown fence language stays plain text.

import type { Language } from "@codemirror/language";
import { highlightCode, tagHighlighter, tags as t } from "@lezer/highlight";
import styles from "./ChatMarkdown.module.css";

type Loader = () => Promise<Language>;

const LOADERS: Record<string, Loader> = {
  javascript: () =>
    import("@codemirror/lang-javascript").then((m) => m.javascriptLanguage),
  jsx: () => import("@codemirror/lang-javascript").then((m) => m.jsxLanguage),
  typescript: () =>
    import("@codemirror/lang-javascript").then((m) => m.typescriptLanguage),
  tsx: () => import("@codemirror/lang-javascript").then((m) => m.tsxLanguage),
  json: () => import("@codemirror/lang-json").then((m) => m.jsonLanguage),
  go: () => import("@codemirror/lang-go").then((m) => m.goLanguage),
  python: () => import("@codemirror/lang-python").then((m) => m.pythonLanguage),
  rust: () => import("@codemirror/lang-rust").then((m) => m.rustLanguage),
  sql: () => import("@codemirror/lang-sql").then((m) => m.StandardSQL.language),
  yaml: () => import("@codemirror/lang-yaml").then((m) => m.yamlLanguage),
  css: () => import("@codemirror/lang-css").then((m) => m.cssLanguage),
  html: () => import("@codemirror/lang-html").then((m) => m.htmlLanguage),
  xml: () => import("@codemirror/lang-xml").then((m) => m.xmlLanguage),
  markdown: () =>
    import("@codemirror/lang-markdown").then((m) => m.markdownLanguage),
  cpp: () => import("@codemirror/lang-cpp").then((m) => m.cppLanguage),
  php: () => import("@codemirror/lang-php").then((m) => m.phpLanguage),
  diff: () => import("codemirror-lang-diff").then((m) => m.diffLanguage),
  shell: () =>
    Promise.all([
      import("@codemirror/language"),
      import("@codemirror/legacy-modes/mode/shell"),
    ]).then(([l, m]) => l.StreamLanguage.define(m.shell)),
};

const ALIASES: Record<string, string> = {
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  jsonc: "json",
  golang: "go",
  py: "python",
  rs: "rust",
  yml: "yaml",
  scss: "css",
  htm: "html",
  md: "markdown",
  c: "cpp",
  "c++": "cpp",
  h: "cpp",
  patch: "diff",
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  console: "shell",
};

/** The highlighter's language id for a fence language, or null when it has none. */
export function highlightLanguage(fence: string): string | null {
  const id = fence.toLowerCase();
  const name = ALIASES[id] ?? id;
  return name in LOADERS ? name : null;
}

const cache = new Map<string, Promise<Language | null>>();

/** Loads a language once; null when it fails to load. */
export function loadLanguage(name: string): Promise<Language | null> {
  let p = cache.get(name);
  if (!p) {
    const load = LOADERS[name];
    p = load ? load().catch(() => null) : Promise.resolve(null);
    cache.set(name, p);
  }
  return p;
}

const highlighter = tagHighlighter([
  {
    tag: [
      t.keyword,
      t.modifier,
      t.controlKeyword,
      t.operatorKeyword,
      t.definitionKeyword,
      t.moduleKeyword,
    ],
    class: styles.tkKeyword!,
  },
  { tag: [t.string, t.special(t.string), t.regexp], class: styles.tkString! },
  { tag: [t.number, t.bool, t.null, t.atom], class: styles.tkNumber! },
  { tag: [t.comment, t.lineComment, t.blockComment], class: styles.tkComment! },
  {
    tag: [t.function(t.variableName), t.function(t.propertyName)],
    class: styles.tkFunction!,
  },
  { tag: [t.typeName, t.className, t.namespace], class: styles.tkType! },
  {
    tag: [t.propertyName, t.attributeName, t.tagName],
    class: styles.tkProperty!,
  },
  { tag: [t.inserted], class: styles.tkInserted! },
  { tag: [t.deleted], class: styles.tkDeleted! },
  { tag: [t.heading, t.meta], class: styles.tkMeta! },
]);

/** One highlighted run of text; className is "" for unstyled text. */
export interface Token {
  text: string;
  className: string;
}

/** Splits code into highlighted runs; a line break is its own "\n" run. */
export function highlight(code: string, language: Language): Token[] {
  const out: Token[] = [];
  highlightCode(
    code,
    language.parser.parse(code),
    highlighter,
    (text, className) => out.push({ text, className }),
    () => out.push({ text: "\n", className: "" }),
  );
  return out;
}
