import { EditorState, type Extension } from "@codemirror/state";
import { ensureSyntaxTree } from "@codemirror/language";
import { go } from "@codemirror/lang-go";
import { javascript } from "@codemirror/lang-javascript";
import { python } from "@codemirror/lang-python";
import { describe, expect, it } from "vitest";

import {
  extractSymbolsFromState,
  symbolTrailAtPosition,
  supportsLezerSymbols,
} from "../lezerSymbols";

/**
 * Build a state whose syntax tree is fully parsed. CodeMirror parses lazily
 * with a time budget, so on a loaded machine a bare EditorState.create can
 * leave a partial tree and extraction would silently miss symbols.
 */
function parsedState(doc: string, extension: Extension): EditorState {
  const state = EditorState.create({ doc, extensions: [extension] });
  ensureSyntaxTree(state, state.doc.length, 30_000);
  return state;
}

describe("lezer symbol extraction", () => {
  it("extracts go types, functions, methods, and cursor trail", () => {
    const doc = [
      "package main",
      "",
      "type Server struct{}",
      "func (s *Server) Serve() {",
      '  println("ok")',
      "}",
      "func main() {}",
      "",
    ].join("\n");
    const state = parsedState(doc, go());

    const symbols = extractSymbolsFromState(state, "go");

    expect(symbols.map((symbol) => `${symbol.kind}:${symbol.name}`)).toEqual([
      "type:Server",
      "method:Serve",
      "function:main",
    ]);
    const trail = symbolTrailAtPosition(symbols, doc.indexOf("println"));
    expect(trail.map((symbol) => symbol.name)).toEqual(["Serve"]);
  });

  it("extracts ts classes, methods, functions, interfaces, type aliases, and arrows", () => {
    const doc = [
      "class Greeter {",
      "  greet(name: string) { return name }",
      "}",
      "function top(x: number) { return x }",
      "const arrow = () => 1",
      "interface Thing { id: string }",
      "type Alias = string",
      "",
    ].join("\n");
    const state = parsedState(doc, javascript({ typescript: true }));

    const symbols = extractSymbolsFromState(state, "typescript");

    expect(symbols.map((symbol) => `${symbol.kind}:${symbol.name}`)).toEqual([
      "class:Greeter",
      "method:greet",
      "function:top",
      "function:arrow",
      "interface:Thing",
      "type:Alias",
    ]);
    const trail = symbolTrailAtPosition(symbols, doc.indexOf("return name"));
    expect(trail.map((symbol) => symbol.name)).toEqual(["Greeter", "greet"]);
  });

  it("extracts python classes, methods, functions, and degrades unsupported languages", () => {
    const doc = [
      "class Greeter:",
      "    def greet(self, name):",
      "        return name",
      "",
      "def top(x):",
      "    return x",
      "",
    ].join("\n");
    const state = parsedState(doc, python());

    const symbols = extractSymbolsFromState(state, "python");

    expect(symbols.map((symbol) => `${symbol.kind}:${symbol.name}`)).toEqual([
      "class:Greeter",
      "method:greet",
      "function:top",
    ]);
    expect(supportsLezerSymbols("markdown")).toBe(false);
    expect(extractSymbolsFromState(state, "markdown")).toEqual([]);
  });
});
