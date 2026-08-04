/**
 * Unit tests for the check-openapi-staleness.mjs script.
 *
 * Tests the exported checkStaleness function by setting up temporary
 * directory structures and mocking child_process.spawnSync to avoid
 * running the real openapi-typescript binary (which is slow and fragile).
 */

import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  rmSync,
  existsSync,
} from "fs";
import { join } from "path";
import { tmpdir } from "os";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

// Mock child_process before importing the module under test.
vi.mock("child_process", () => ({
  spawnSync: vi.fn(),
}));

import { checkStaleness } from "../check-openapi-staleness.mjs";
import { spawnSync } from "child_process";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Create a temp directory that acts as a fake frontend project root.
 * Returns the frontendDir path. The directory layout is:
 *   <root>/
 *     api/               (3 dirs up from frontendDir + "api")
 *     internal/webui/frontend/   (frontendDir)
 *       node_modules/.bin/
 *       src/types/generated/
 */
function makeTmpProject() {
  const root = mkdtempSync(join(tmpdir(), "check-openapi-staleness-test-"));
  // frontendDir sits 3 levels deep so that join(frontendDir, "..", "..", "..", "api") resolves to root/api
  const frontendDir = join(root, "internal", "webui", "frontend");
  mkdirSync(frontendDir, { recursive: true });
  return { root, frontendDir };
}

/** Create the api/openapi.yaml spec file. */
function writeSpec(root, content = "openapi: '3.1.0'\ninfo:\n  title: Test\n  version: '1.0'\npaths: {}\n") {
  const specDir = join(root, "api");
  mkdirSync(specDir, { recursive: true });
  writeFileSync(join(specDir, "openapi.yaml"), content);
}

/** Create the src/types/generated/openapi.ts file. */
function writeGenerated(frontendDir, content = "// generated types\nexport interface Paths {}\n") {
  const dir = join(frontendDir, "src", "types", "generated");
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "openapi.ts"), content);
}

/** Create a fake openapi-typescript binary at node_modules/.bin/openapi-typescript. */
function writeBinary(frontendDir) {
  const binDir = join(frontendDir, "node_modules", ".bin");
  mkdirSync(binDir, { recursive: true });
  writeFileSync(join(binDir, "openapi-typescript"), "#!/bin/sh\n# fake binary\n");
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("checkStaleness", () => {
  let root;
  let frontendDir;

  beforeEach(() => {
    const tmp = makeTmpProject();
    root = tmp.root;
    frontendDir = tmp.frontendDir;
    vi.resetAllMocks();
  });

  afterEach(() => {
    rmSync(root, { recursive: true, force: true });
  });

  // -------------------------------------------------------------------------
  // 1. No spec file -> status "skip"
  // -------------------------------------------------------------------------

  describe("when api/openapi.yaml does not exist", () => {
    it("returns status 'skip'", () => {
      // No spec file created — just bare frontendDir
      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("skip");
      expect(result.message).toContain("openapi.yaml not found");
    });
  });

  // -------------------------------------------------------------------------
  // 2. Spec exists, no generated file -> status "missing"
  // -------------------------------------------------------------------------

  describe("when spec exists but generated file does not", () => {
    it("returns status 'missing'", () => {
      writeSpec(root);
      // No generated file created

      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("missing");
      expect(result.message).toContain("openapi.yaml exists");
      expect(result.message).toContain("does not");
      expect(result.message).toContain("generate:types");
    });
  });

  // -------------------------------------------------------------------------
  // 3. openapi-typescript binary missing -> status "error"
  // -------------------------------------------------------------------------

  describe("when openapi-typescript binary is missing", () => {
    it("returns status 'error'", () => {
      writeSpec(root);
      writeGenerated(frontendDir);
      // No binary created at node_modules/.bin/openapi-typescript

      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("error");
      expect(result.message).toContain("openapi-typescript is not installed");
    });
  });

  // -------------------------------------------------------------------------
  // 4. Spec + generated + binary, generation succeeds, output matches -> "ok"
  // -------------------------------------------------------------------------

  describe("when generated file matches regeneration", () => {
    it("returns status 'ok'", () => {
      const generatedContent = "// generated types\nexport interface Paths {}\n";
      writeSpec(root);
      writeGenerated(frontendDir, generatedContent);
      writeBinary(frontendDir);

      // Mock spawnSync: when called with the openapi-typescript binary,
      // write the same content to the temp file (simulating identical output)
      spawnSync.mockImplementation((cmd, args, _opts) => {
        if (cmd.endsWith("openapi-typescript")) {
          // args = [specPath, "-o", tmpFile]
          const oIdx = args.indexOf("-o");
          if (oIdx !== -1) {
            const tmpFile = args[oIdx + 1];
            mkdirSync(join(tmpFile, ".."), { recursive: true });
            writeFileSync(tmpFile, generatedContent);
          }
          return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
        }
        return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
      });

      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("ok");
      expect(result.message).toContain("up to date");
    });
  });

  // -------------------------------------------------------------------------
  // 5. Spec + generated + binary, generation succeeds, output differs -> "stale"
  // -------------------------------------------------------------------------

  describe("when generated file differs from regeneration", () => {
    it("returns status 'stale' with diff", () => {
      const committedContent = "// old generated types\nexport interface Paths {}\n";
      const freshContent = "// new generated types\nexport interface Paths { get: string; }\n";
      writeSpec(root);
      writeGenerated(frontendDir, committedContent);
      writeBinary(frontendDir);

      spawnSync.mockImplementation((cmd, args, _opts) => {
        if (cmd.endsWith("openapi-typescript")) {
          const oIdx = args.indexOf("-o");
          if (oIdx !== -1) {
            const tmpFile = args[oIdx + 1];
            mkdirSync(join(tmpFile, ".."), { recursive: true });
            writeFileSync(tmpFile, freshContent);
          }
          return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
        }
        // diff command — simulate diff finding differences (exit code 1)
        return {
          status: 1,
          stdout: Buffer.from(
            "--- committed\n+++ regenerated\n@@ -1,2 +1,2 @@\n-// old generated types\n+// new generated types\n",
          ),
          stderr: Buffer.from(""),
        };
      });

      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("stale");
      expect(result.message).toContain("stale");
      expect(result.message).toContain("generate:types");
      expect(result.diff).toBeDefined();
      expect(result.diff).toContain("old generated types");
      expect(result.diff).toContain("new generated types");
    });

    it("truncates diff output beyond 30 lines", () => {
      const committedContent = "// old\n";
      const freshContent = "// new\n";
      writeSpec(root);
      writeGenerated(frontendDir, committedContent);
      writeBinary(frontendDir);

      spawnSync.mockImplementation((cmd, args, _opts) => {
        if (cmd.endsWith("openapi-typescript")) {
          const oIdx = args.indexOf("-o");
          if (oIdx !== -1) {
            const tmpFile = args[oIdx + 1];
            mkdirSync(join(tmpFile, ".."), { recursive: true });
            writeFileSync(tmpFile, freshContent);
          }
          return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
        }
        // Simulate a large diff (more than 30 lines)
        const lines = Array.from({ length: 50 }, (_, i) => `line ${i}`);
        return {
          status: 1,
          stdout: Buffer.from(lines.join("\n") + "\n"),
          stderr: Buffer.from(""),
        };
      });

      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("stale");
      expect(result.diff).toContain("truncated");
      // Should only have first 30 lines plus the truncation message
      const diffLines = result.diff.split("\n");
      // 30 lines of diff + 1 truncation message line
      expect(diffLines.length).toBe(31);
    });
  });

  // -------------------------------------------------------------------------
  // 6. openapi-typescript generation fails -> status "error"
  // -------------------------------------------------------------------------

  describe("when openapi-typescript generation fails", () => {
    it("returns status 'error' with stderr message", () => {
      writeSpec(root);
      writeGenerated(frontendDir);
      writeBinary(frontendDir);

      spawnSync.mockImplementation((_cmd, _args, _opts) => {
        return {
          status: 1,
          stdout: Buffer.from(""),
          stderr: Buffer.from("Error: Invalid OpenAPI schema"),
        };
      });

      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("error");
      expect(result.message).toContain("Failed to generate");
      expect(result.message).toContain("Invalid OpenAPI schema");
    });

    it("uses error.message when stderr is empty and error is present", () => {
      writeSpec(root);
      writeGenerated(frontendDir);
      writeBinary(frontendDir);

      spawnSync.mockImplementation((_cmd, _args, _opts) => {
        return {
          status: 1,
          stdout: Buffer.from(""),
          stderr: Buffer.from(""),
          error: new Error("ENOENT: command not found"),
        };
      });

      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("error");
      expect(result.message).toContain("Failed to generate");
      expect(result.message).toContain("ENOENT");
    });
  });

  // -------------------------------------------------------------------------
  // 7. Temp directory cleanup
  // -------------------------------------------------------------------------

  describe("cleanup", () => {
    it("cleans up temp directory on success", () => {
      const generatedContent = "// types\n";
      writeSpec(root);
      writeGenerated(frontendDir, generatedContent);
      writeBinary(frontendDir);

      let createdTmpFile;
      spawnSync.mockImplementation((cmd, args, _opts) => {
        if (cmd.endsWith("openapi-typescript")) {
          const oIdx = args.indexOf("-o");
          if (oIdx !== -1) {
            createdTmpFile = args[oIdx + 1];
            mkdirSync(join(createdTmpFile, ".."), { recursive: true });
            writeFileSync(createdTmpFile, generatedContent);
          }
          return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
        }
        return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
      });

      checkStaleness(frontendDir);

      // The temp directory (parent of tmpFile) should have been cleaned up
      if (createdTmpFile) {
        expect(existsSync(join(createdTmpFile, ".."))).toBe(false);
      }
    });

    it("cleans up temp directory on generation error", () => {
      writeSpec(root);
      writeGenerated(frontendDir);
      writeBinary(frontendDir);

      spawnSync.mockImplementation((_cmd, _args, _opts) => {
        return {
          status: 1,
          stdout: Buffer.from(""),
          stderr: Buffer.from("error"),
        };
      });

      // Should not throw even when generation fails
      const result = checkStaleness(frontendDir);
      expect(result.status).toBe("error");
    });
  });

  // -------------------------------------------------------------------------
  // 8. Verify spawnSync is called with correct arguments
  // -------------------------------------------------------------------------

  describe("spawnSync invocation", () => {
    it("passes correct binary path, spec path, and -o flag as array", () => {
      const generatedContent = "// types\n";
      writeSpec(root);
      writeGenerated(frontendDir, generatedContent);
      writeBinary(frontendDir);

      spawnSync.mockImplementation((cmd, args, _opts) => {
        if (cmd.endsWith("openapi-typescript")) {
          const oIdx = args.indexOf("-o");
          if (oIdx !== -1) {
            mkdirSync(join(args[oIdx + 1], ".."), { recursive: true });
            writeFileSync(args[oIdx + 1], generatedContent);
          }
          return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
        }
        return { status: 0, stdout: Buffer.from(""), stderr: Buffer.from("") };
      });

      checkStaleness(frontendDir);

      // First call should be the openapi-typescript command
      expect(spawnSync).toHaveBeenCalled();
      const [firstCmd, firstArgs, firstOpts] = spawnSync.mock.calls[0];
      const expectedBinPath = join(frontendDir, "node_modules", ".bin", "openapi-typescript");
      const expectedSpecPath = join(root, "api", "openapi.yaml");
      expect(firstCmd).toBe(expectedBinPath);
      expect(firstArgs[0]).toBe(expectedSpecPath);
      expect(firstArgs[1]).toBe("-o");
      expect(firstOpts).toEqual({ stdio: ["pipe", "pipe", "pipe"] });
    });
  });
});
