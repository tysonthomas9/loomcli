import type { EditorState } from "@codemirror/state";
import { syntaxTree } from "@codemirror/language";

type SyntaxNode = ReturnType<typeof syntaxTree>["topNode"];

export type SupportedSymbolLanguage =
  | "go"
  | "javascript"
  | "jsx"
  | "typescript"
  | "tsx"
  | "python";

export type FileSymbolKind =
  | "function"
  | "method"
  | "class"
  | "type"
  | "interface";

export interface FileSymbol {
  name: string;
  kind: FileSymbolKind;
  from: number;
  to: number;
  nameFrom: number;
  line: number;
}

export interface FileSymbolState {
  symbols: FileSymbol[];
  trail: FileSymbol[];
}

const supportedLanguages = new Set<string>([
  "go",
  "javascript",
  "jsx",
  "typescript",
  "tsx",
  "python",
]);

export function supportsLezerSymbols(
  language: string | undefined,
): language is SupportedSymbolLanguage {
  return !!language && supportedLanguages.has(language);
}

export function extractSymbolsFromState(
  state: EditorState,
  language: string | undefined,
): FileSymbol[] {
  if (!supportsLezerSymbols(language)) return [];
  const symbols: FileSymbol[] = [];
  const doc = state.doc.toString();
  const tree = syntaxTree(state);

  const visit = (node: SyntaxNode, ancestors: SyntaxNode[]) => {
    const symbol = symbolFromNode(state, doc, language, node, ancestors);
    if (symbol) {
      symbols.push(symbol);
    }
    for (let child = node.firstChild; child; child = child.nextSibling) {
      visit(child, [...ancestors, node]);
    }
  };

  visit(tree.topNode, []);
  return symbols.sort(
    (a, b) => a.nameFrom - b.nameFrom || a.name.localeCompare(b.name),
  );
}

export function symbolTrailAtPosition(
  symbols: FileSymbol[],
  position: number,
): FileSymbol[] {
  return symbols
    .filter((symbol) => symbol.from <= position && position <= symbol.to)
    .sort((a, b) => a.from - b.from || b.to - a.to);
}

export function symbolStateFromEditor(
  state: EditorState,
  language: string | undefined,
): FileSymbolState {
  const symbols = extractSymbolsFromState(state, language);
  return {
    symbols,
    trail: symbolTrailAtPosition(symbols, state.selection.main.head),
  };
}

function symbolFromNode(
  state: EditorState,
  doc: string,
  language: SupportedSymbolLanguage,
  node: SyntaxNode,
  ancestors: SyntaxNode[],
): FileSymbol | null {
  if (language === "go") {
    if (node.name === "FunctionDecl") {
      return symbolWithChild(state, doc, node, "DefName", "function");
    }
    if (node.name === "MethodDecl") {
      return symbolWithChild(state, doc, node, "FieldName", "method");
    }
    if (node.name === "TypeSpec") {
      return symbolWithChild(state, doc, node, "DefName", "type");
    }
    return null;
  }

  if (language === "python") {
    if (node.name === "ClassDefinition") {
      return symbolWithChild(state, doc, node, "VariableName", "class");
    }
    if (node.name === "FunctionDefinition") {
      const inClass = ancestors.some(
        (ancestor) => ancestor.name === "ClassDefinition",
      );
      return symbolWithChild(
        state,
        doc,
        node,
        "VariableName",
        inClass ? "method" : "function",
      );
    }
    return null;
  }

  switch (node.name) {
    case "ClassDeclaration":
      return symbolWithChild(state, doc, node, "VariableDefinition", "class");
    case "FunctionDeclaration":
      return symbolWithChild(
        state,
        doc,
        node,
        "VariableDefinition",
        "function",
      );
    case "MethodDeclaration":
      return symbolWithChild(state, doc, node, "PropertyDefinition", "method");
    case "InterfaceDeclaration":
      return symbolWithChild(state, doc, node, "TypeDefinition", "interface");
    case "TypeAliasDeclaration":
      return symbolWithChild(state, doc, node, "TypeDefinition", "type");
    case "VariableDeclaration":
      if (hasChild(node, "ArrowFunction")) {
        return symbolWithChild(
          state,
          doc,
          node,
          "VariableDefinition",
          "function",
        );
      }
      return null;
    default:
      return null;
  }
}

function symbolWithChild(
  state: EditorState,
  doc: string,
  node: SyntaxNode,
  childName: string,
  kind: FileSymbolKind,
): FileSymbol | null {
  const child = findChild(node, childName);
  if (!child) return null;
  const name = doc.slice(child.from, child.to).trim();
  if (!name) return null;
  return {
    name,
    kind,
    from: node.from,
    to: node.to,
    nameFrom: child.from,
    line: state.doc.lineAt(child.from).number,
  };
}

function findChild(node: SyntaxNode, childName: string): SyntaxNode | null {
  for (let child = node.firstChild; child; child = child.nextSibling) {
    if (child.name === childName) return child;
  }
  return null;
}

function hasChild(node: SyntaxNode, childName: string): boolean {
  return findChild(node, childName) !== null;
}
