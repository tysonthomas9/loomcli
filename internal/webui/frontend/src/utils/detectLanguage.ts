/**
 * detectLanguage maps a file path to a CodeMirror language id. It is the single
 * source of truth for path→language across the file browser and the agent file
 * editor; `CodeMirrorEditor` owns the id→Extension mapping (bundled + lazy).
 * Returns undefined for unknown/plain-text files (rendered without highlighting).
 */
export function detectLanguage(path: string): string | undefined {
  const file = path.split("/").pop()?.toLowerCase() ?? "";
  if (file === "dockerfile" || file.endsWith(".dockerfile"))
    return "dockerfile";

  const dot = file.lastIndexOf(".");
  const ext = dot > 0 ? file.slice(dot + 1) : "";
  switch (ext) {
    case "go":
      return "go";
    case "json":
    case "jsonc":
    case "json5":
      return "json";
    case "yaml":
    case "yml":
      return "yaml";
    case "md":
    case "markdown":
    case "mdx":
      return "markdown";
    case "diff":
    case "patch":
      return "diff";
    case "js":
    case "cjs":
    case "mjs":
      return "javascript";
    case "jsx":
      return "jsx";
    case "ts":
    case "cts":
    case "mts":
      return "typescript";
    case "tsx":
      return "tsx";
    case "css":
    case "scss":
    case "less":
      return "css";
    case "html":
    case "htm":
    case "xhtml":
      return "html";
    case "py":
    case "pyi":
      return "python";
    case "rs":
      return "rust";
    case "sql":
      return "sql";
    case "xml":
    case "svg":
    case "xsd":
      return "xml";
    case "c":
    case "h":
    case "cc":
    case "cpp":
    case "cxx":
    case "hpp":
    case "hh":
      return "cpp";
    case "php":
      return "php";
    case "sh":
    case "bash":
    case "zsh":
    case "ksh":
      return "shell";
    case "toml":
      return "toml";
    case "ini":
    case "cfg":
    case "conf":
    case "properties":
      return "ini";
    default:
      return undefined;
  }
}
