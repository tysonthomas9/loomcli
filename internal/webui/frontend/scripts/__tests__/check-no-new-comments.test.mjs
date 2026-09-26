import { describe, it, expect } from "vitest";

import {
  parseRanges,
  scanSource,
  run,
} from "../check-no-new-comments.mjs";

const ALL = [{ start: 1, end: 1000 }];

function flaggedLines(path, source, added = ALL) {
  return scanSource(path, source, added).map((v) => v.line);
}

describe("scanSource — real comments", () => {
  it("flags line comments with location and text", () => {
    const source = "const a = 1;\n// added\nconst b = 2;\n";
    expect(scanSource("src/a.ts", source, ALL)).toEqual([
      { path: "src/a.ts", line: 2, text: "// added" },
    ]);
  });

  it("flags trailing comments", () => {
    expect(flaggedLines("a.ts", "const a = 1; // trailing\n")).toEqual([1]);
  });

  it("flags every line of a block comment", () => {
    const source = "const a = 1;\n/*\n x\n*/\nconst b = 2;\n";
    expect(flaggedLines("a.ts", source)).toEqual([2, 3, 4]);
  });

  it("flags JSDoc comments", () => {
    const source = "/** doc */\nexport function f() {}\n";
    expect(flaggedLines("a.ts", source)).toEqual([1]);
  });

  it("flags JSX comments in tsx", () => {
    const source =
      "export const C = () => (\n  <div>\n    {/* x */}\n    <span />\n  </div>\n);\n";
    expect(flaggedLines("a.tsx", source)).toEqual([3]);
  });

  it("flags comments inside JSX attributes and template substitutions", () => {
    const source =
      "export const C = () => <div title={/* t */ 'a'} />;\nconst s = `${/* x */ 1}`;\n";
    expect(flaggedLines("a.tsx", source)).toEqual([1, 2]);
  });

  it("flags comments at end of file", () => {
    expect(flaggedLines("a.ts", "const a = 1;\n// tail")).toEqual([2]);
  });

  it("gives suppressions and triple-slash directives no exemption", () => {
    const source = [
      '/// <reference types="vitest" />',
      "// eslint-disable-next-line no-console",
      "console.log(1);",
      "// @ts-expect-error",
      "const a: number = 'x';",
      "// @ts-ignore",
      "const b: number = 'y';",
      "",
    ].join("\n");
    expect(flaggedLines("a.ts", source)).toEqual([1, 2, 4, 6]);
  });
});

describe("scanSource — not comments", () => {
  it("ignores // in strings, templates and regexes", () => {
    const source = [
      'const u = "http://a";',
      "const s = 'x // y';",
      "const t = `// not ${u} /* nope */`;",
      "const r = /\\/\\/ x/;",
      "const d = 4 / 2 / 1;",
      "",
    ].join("\n");
    expect(flaggedLines("a.ts", source)).toEqual([]);
  });

  it("ignores comment-like JSX text", () => {
    const source =
      "export const C = () => (\n  <p>\n    // not a comment /* nor this */\n  </p>\n);\n";
    expect(flaggedLines("a.tsx", source)).toEqual([]);
  });

  it("ignores a shebang", () => {
    expect(flaggedLines("a.ts", "#!/usr/bin/env node\nconst a = 1;\n")).toEqual(
      [],
    );
  });

  it("ignores comments outside the added ranges", () => {
    const source = "// old\nconst a = 1;\n/*\nold\n*/\n";
    expect(flaggedLines("a.ts", source, [{ start: 2, end: 2 }])).toEqual([]);
    expect(flaggedLines("a.ts", source, [{ start: 4, end: 4 }])).toEqual([4]);
  });
});

describe("parseRanges", () => {
  it("groups ranges by path and skips blank lines", () => {
    const ranges = parseRanges("a.ts\t1\t2\n\nb c.tsx\t3\t3\na.ts\t5\t6\n");
    expect([...ranges.keys()]).toEqual(["a.ts", "b c.tsx"]);
    expect(ranges.get("a.ts")).toEqual([
      { start: 1, end: 2 },
      { start: 5, end: 6 },
    ]);
  });

  it.each(["a.ts\t1", "a.ts\tx\t2", "a.ts\t0\t1", "a.ts\t3\t2"])(
    "rejects malformed line %j",
    (line) => {
      expect(() => parseRanges(line)).toThrow(/malformed range line/);
    },
  );
});

describe("run", () => {
  const files = {
    "a.ts": "const a = 1;\n// new\n",
    "b.tsx": "export const B = () => <div />;\n",
  };
  const readFile = (path) => {
    if (!(path in files)) throw new Error("ENOENT");
    return files[path];
  };

  function capture(input) {
    let out = "";
    const code = run(input, readFile, (s) => {
      out += s;
    });
    return { code, out };
  }

  it("exits 0 when no added line holds a comment", () => {
    expect(capture("a.ts\t1\t1\nb.tsx\t1\t1\n")).toEqual({ code: 0, out: "" });
  });

  it("exits 1 and prints file:line for violations", () => {
    expect(capture("a.ts\t1\t2\n")).toEqual({ code: 1, out: "a.ts:2: // new\n" });
  });

  it("exits 2 on malformed input", () => {
    expect(capture("junk\n").code).toBe(2);
  });

  it("exits 2 when a file cannot be read", () => {
    const { code, out } = capture("missing.ts\t1\t1\n");
    expect(code).toBe(2);
    expect(out).toContain("read missing.ts");
  });
});
