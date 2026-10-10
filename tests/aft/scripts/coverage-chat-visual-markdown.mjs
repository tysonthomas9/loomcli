#!/usr/bin/env node
// Project the exact ChatMarkdown component's source prefixes onto DOM text.
// CSS module names affect styling, never text; every other import is bundled
// from the same source checkout as the AFT suite.
import { createRequire } from "node:module";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const frontend = resolve(process.argv[2] || "");
if (
  !frontend ||
  !resolve(frontend, "src/components/AgentChat/ChatMarkdown.tsx").startsWith(
    frontend + "/",
  )
)
  throw Error("frontend-source-path");
const sourceFile = resolve(
  frontend,
  "src/components/AgentChat/ChatMarkdown.tsx",
);
const limitFile = resolve(frontend, "src/components/AgentChat/LongText.tsx");
const highlightFile = resolve(
  frontend,
  "src/components/AgentChat/codeHighlight.ts",
);
const copyFile = resolve(
  frontend,
  "src/components/AgentChat/MessageCopyButton.tsx",
);
const cssFile = resolve(
  frontend,
  "src/components/AgentChat/ChatMarkdown.module.css",
);
const lockFile = resolve(frontend, "package-lock.json");
const sha256 = (path) =>
  createHash("sha256").update(readFileSync(path)).digest("hex");
// The visible-text traversal is verified against these exact renderer and CSS
// bytes in Chromium. A change needs new browser parity evidence before credit.
if (
  sha256(sourceFile) !==
    "cbf6aad87f1f4f4e2160679732a886d2248f6b60b07aee9d7c48c5395600522f" ||
  sha256(cssFile) !==
    "8fc71740bb4e2e99b526105279f286544e1e27c4fb81b4eb1fc9c051573bda4b"
)
  throw Error("unverified-visible-renderer-source");
if (!readFileSync(limitFile, "utf8").includes("{text}</div>"))
  throw Error("unrecognized-full-text-rendering");
const lock = JSON.parse(readFileSync(lockFile, "utf8"));
const css = readFileSync(cssFile, "utf8");
if (
  !/\.markdown\[data-streaming="true"\]\s+\.tableActions\s*\{\s*visibility:\s*hidden;\s*\}/.test(
    css,
  )
)
  throw Error("unrecognized-streaming-actions-visibility");
const cssNames = Object.fromEntries(
  [...css.matchAll(/\.([A-Za-z][A-Za-z0-9_-]*)/g)].map((match) => [
    match[1],
    match[1],
  ]),
);
const dependencies = {};
for (const name of [
  "react",
  "react-dom",
  "react-markdown",
  "remark-gfm",
  "rehype-sanitize",
  "esbuild",
  "jsdom",
]) {
  const installed = JSON.parse(
    readFileSync(
      resolve(frontend, "node_modules", name, "package.json"),
      "utf8",
    ),
  ).version;
  if (lock.packages?.[`node_modules/${name}`]?.version !== installed)
    throw Error(`unmatched-installed-dependency:${name}`);
  dependencies[name] = installed;
}
const require = createRequire(resolve(frontend, "package.json"));
const esbuild = require("esbuild");
const { JSDOM } = require("jsdom");
const input = JSON.parse(readFileSync(0, "utf8"));
const terminalOnly = input.mode === "terminal-full";
if (input.mode !== undefined && !terminalOnly)
  throw Error("invalid-projection-mode");
if (
  typeof input.answer !== "string" ||
  !input.answer ||
  input.answer.length > (terminalOnly ? 128000 : 8000) ||
  !Array.isArray(input.frames) ||
  input.frames.length > 20000 ||
  !Array.isArray(input.arrivals) ||
  input.arrivals.length > 5000 ||
  !input.frames.every(
    (frame) =>
      typeof frame?.text === "string" && typeof frame.streaming === "boolean",
  ) ||
  !input.arrivals.every((text) => typeof text === "string")
)
  throw Error("invalid-projection-input");

const entry = `import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { ChatMarkdown } from ${JSON.stringify(sourceFile)};
export function render(text, streaming) {
  return renderToStaticMarkup(React.createElement(ChatMarkdown, { text, streaming }));
}`;
const built = await esbuild.build({
  stdin: {
    contents: entry,
    sourcefile: "visual-projection.ts",
    resolveDir: frontend,
  },
  bundle: true,
  platform: "node",
  format: "cjs",
  write: false,
  nodePaths: [resolve(frontend, "node_modules")],
  plugins: [
    {
      name: "css-module-names-only",
      setup(build) {
        build.onLoad({ filter: /\.module\.css$/ }, () => ({
          contents: `export default ${JSON.stringify(cssNames)}`,
          loader: "js",
        }));
      },
    },
  ],
});
const module = { exports: {} };
new Function("require", "module", "exports", built.outputFiles[0].text)(
  require,
  module,
  module.exports,
);
const dom = new JSDOM('<div id="projection"></div>');
const host = dom.window.document.getElementById("projection");
function project(source, streaming) {
  host.innerHTML = module.exports.render(source, streaming);
  const markdown = host.querySelector('[data-testid="chat-markdown"]');
  if (
    !markdown ||
    host.querySelectorAll('[data-testid="chat-markdown"]').length !== 1
  )
    throw Error("missing-component-projection");
  const boundaries = new Set([
    "DIV",
    "P",
    "PRE",
    "UL",
    "OL",
    "LI",
    "BLOCKQUOTE",
    "SECTION",
    "H1",
    "H2",
    "H3",
    "H4",
    "H5",
    "H6",
  ]);
  function shown(node, replyOnly = false) {
    if (node.nodeType === 3) return node.nodeValue ?? "";
    if (node.nodeType !== 1) return "";
    const el = node;
    if (replyOnly && el.hasAttribute("data-chat-renderer-chrome")) return "";
    if (streaming && el.classList.contains(cssNames.tableActions)) return "";
    if (["SCRIPT", "STYLE", "SVG", "INPUT"].includes(el.tagName)) return "";
    if (el.tagName === "BR") return "\n";
    if (el.tagName === "TABLE")
      return [...el.querySelectorAll("tr")]
        .map((row) => shown(row, replyOnly))
        .join("\n");
    if (el.tagName === "TR")
      return [...el.children]
        .filter((child) => ["TH", "TD"].includes(child.tagName))
        .map((cell) => shown(cell, replyOnly))
        .join("\t");
    const children = [...el.childNodes].map((child) => shown(child, replyOnly));
    const separated =
      el.classList.contains(cssNames.toolbar) ||
      el.classList.contains(cssNames.codeblockHeader) ||
      el.classList.contains(cssNames.tableActions);
    const body = children.join(separated ? "\n" : "");
    return boundaries.has(el.tagName) ? `\n${body}\n` : body;
  }
  const normalize = (value) => value.trim().replace(/\n{3,}/g, "\n\n");
  return {
    raw: markdown.textContent,
    visible: normalize(shown(markdown)),
    content: normalize(shown(markdown, true)),
  };
}

if (terminalOnly) {
  if (input.frames.length || input.arrivals.length)
    throw Error("terminal-mode-has-motion-evidence");
  const terminal = project(input.answer, false);
  process.stdout.write(
    JSON.stringify({
      version: 1,
      mode: "terminal-full",
      sourceUtf16: input.answer.length,
      terminal: terminal.raw,
      chatMarkdownSha256: sha256(sourceFile),
      cssSha256: sha256(cssFile),
    }) + "\n",
  );
  process.exit(0);
}

const positions = new Map();
const visiblePositions = new Map();
const contentPositions = new Map();
for (let at = 0; at <= input.answer.length; at++) {
  const { raw, visible, content } = project(input.answer.slice(0, at), true);
  const range = positions.get(raw);
  if (range) range[1] = at;
  else positions.set(raw, [at, at]);
  const visibleRange = visiblePositions.get(visible);
  if (visibleRange) visibleRange[1] = at;
  else visiblePositions.set(visible, [at, at]);
  const contentRange = contentPositions.get(content);
  if (contentRange) contentRange[1] = at;
  else contentPositions.set(content, [at, at]);
}
let cumulative = "";
let previous = project("", true).visible;
let previousContent = project("", true).content;
const arrivals = input.arrivals.map((delta) => {
  const priorSourceLength = cumulative.length;
  cumulative += delta;
  if (!input.answer.startsWith(cumulative))
    throw Error("arrival-is-not-saved-source-prefix");
  const { visible, content } = project(cumulative, true);
  const range = visiblePositions.get(visible);
  if (!range) throw Error("missing-arrival-projection");
  if (visible !== previous && range[0] <= priorSourceLength)
    throw Error("ambiguous-visible-arrival-projection");
  const contentRange = contentPositions.get(content);
  if (!contentRange) throw Error("missing-content-arrival-projection");
  if (content !== previousContent && contentRange[0] <= priorSourceLength)
    throw Error("ambiguous-content-arrival-projection");
  const result = {
    sourceUtf16: cumulative.length,
    visibleChanged: visible !== previous,
    contentChanged: content !== previousContent,
    requiredMinSourceUtf16: contentRange[0],
    projectedUtf16: visible.length,
    projectedWords: content.trim().split(/\s+/).filter(Boolean).length,
  };
  previous = visible;
  previousContent = content;
  return result;
});
const terminal = project(input.answer, false);
const frames = input.frames.map((frame) => {
  if (!frame.streaming) {
    if (frame.text !== terminal.raw) return null;
    return {
      minSourceUtf16: input.answer.length,
      maxSourceUtf16: input.answer.length,
      visibleWords: terminal.visible.trim().split(/\s+/).filter(Boolean).length,
      visible: terminal.visible,
      content: terminal.content,
      contentWords: terminal.content.trim().split(/\s+/).filter(Boolean).length,
    };
  }
  const range = positions.get(frame.text);
  if (!range) return null;
  const first = project(input.answer.slice(0, range[0]), true);
  const last = project(input.answer.slice(0, range[1]), true);
  if (first.visible !== last.visible || first.content !== last.content)
    return null;
  return {
    minSourceUtf16: range[0],
    maxSourceUtf16: range[1],
    visibleWords: first.visible.trim().split(/\s+/).filter(Boolean).length,
    visible: first.visible,
    content: first.content,
    contentWords: first.content.trim().split(/\s+/).filter(Boolean).length,
  };
});
process.stdout.write(
  JSON.stringify({
    version: 1,
    terminal: terminal.raw,
    terminalVisible: terminal.visible,
    terminalContent: terminal.content,
    frames,
    arrivals,
    sourceUtf16: input.answer.length,
    identity: {
      chatMarkdownSha256: sha256(sourceFile),
      longTextSha256: sha256(limitFile),
      codeHighlightSha256: sha256(highlightFile),
      messageCopySha256: sha256(copyFile),
      cssSha256: sha256(cssFile),
      lockSha256: sha256(lockFile),
      dependencies,
    },
  }) + "\n",
);
