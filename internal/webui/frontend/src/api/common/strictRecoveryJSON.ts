/** JSON.parse silently loses duplicate members and unsafe integer precision.
 * Scan the original tokens first, without constructing a second object tree. */
export function parseStrictRecoveryJSON(document: string): unknown {
  let position = 0;
  function whitespace() {
    while (/\s/.test(document[position] ?? "") && position < document.length) {
      if (!/[ \t\r\n]/.test(document[position] ?? ""))
        throw new Error("Invalid JSON whitespace");
      position++;
    }
  }
  function string(): string {
    const start = position++;
    while (position < document.length) {
      const character = document[position++];
      if (character === "\\") {
        position++;
        continue;
      }
      if (character === '"')
        return checkedString(
          JSON.parse(document.slice(start, position)) as string,
        );
    }
    throw new Error("Incomplete JSON string");
  }
  function value(depth: number): void {
    if (depth > 512) throw new Error("Recovery JSON nesting exceeds limit");
    whitespace();
    const character = document[position];
    if (character === '"') {
      string();
      return;
    }
    if (character === "{" || character === "[") {
      container(character, depth);
      return;
    }
    const match =
      /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(
        document.slice(position),
      );
    if (!match) throw new Error("Invalid JSON value");
    position += match[0].length;
    if (/^-?\d/.test(match[0])) {
      const number = Number(match[0]);
      if (
        !Number.isFinite(number) ||
        (Number.isInteger(number) && !Number.isSafeInteger(number)) ||
        decimalIdentity(match[0]) !== decimalIdentity(number.toString())
      )
        throw new Error("Unsafe JSON number");
    }
  }
  function container(open: string, depth: number): void {
    position++;
    whitespace();
    const close = open === "{" ? "}" : "]";
    const keys = new Set<string>();
    if (document[position] === close) {
      position++;
      return;
    }
    while (position < document.length) {
      if (open === "{") {
        if (document[position] !== '"') throw new Error("Invalid JSON member");
        const key = string();
        if (keys.has(key)) throw new Error("Duplicate JSON member");
        keys.add(key);
        whitespace();
        if (document[position++] !== ":")
          throw new Error("Invalid JSON member separator");
      }
      value(depth + 1);
      whitespace();
      if (document[position] === close) {
        position++;
        return;
      }
      if (document[position++] !== ",")
        throw new Error("Invalid JSON separator");
      whitespace();
    }
    throw new Error("Incomplete JSON container");
  }
  value(0);
  whitespace();
  if (position !== document.length) throw new Error("Trailing JSON data");
  return JSON.parse(document) as unknown;
}

// Compare decimal values without first rounding the source token to a double.
function decimalIdentity(token: string): string {
  const negative = token.startsWith("-");
  const [mantissa = "", exponent = "0"] = token
    .replace(/^-/, "")
    .toLowerCase()
    .split("e");
  const [whole = "", fraction = ""] = mantissa.split(".");
  const digits = (whole + fraction).replace(/^0+/, "");
  if (!digits) return "0";
  const trimmed = digits.replace(/0+$/, "");
  const power =
    Number(exponent) - fraction.length + digits.length - trimmed.length;
  return `${negative ? "-" : ""}${trimmed}e${power}`;
}
function checkedString(value: string): string {
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(++index);
      if (!(next >= 0xdc00 && next <= 0xdfff))
        throw new Error("Unpaired JSON surrogate");
    } else if (code >= 0xdc00 && code <= 0xdfff)
      throw new Error("Unpaired JSON surrogate");
  }
  return value;
}
