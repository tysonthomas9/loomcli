import { describe, expect, it } from "vitest";

import {
  parseSkillPath,
  validateRoleName,
  validateSkillFilePath,
  validateSkillName,
} from "../skillsPaths";

describe("skillsPaths", () => {
  it.each(["audit", "code-review", "a1-b2"])(
    "accepts valid skill name %s",
    (name) => expect(validateSkillName(name)).toBeNull(),
  );

  it.each(["Claude", "anthropic", "con", "bad--name", "bad_name"])(
    "rejects reserved or malformed skill name %s",
    (name) => expect(validateSkillName(name)).not.toBeNull(),
  );

  it.each([
    "../secret",
    "scripts\\run.sh",
    "scripts:run.sh",
    "skill.md",
    "con.txt",
    "bad\u0000name",
  ])("rejects unsafe or reserved file path %s", (path) => {
    expect(validateSkillFilePath(path)).not.toBeNull();
  });

  it("parses a skill and nested bundled file", () => {
    expect(parseSkillPath("audit/scripts/run.sh")).toEqual({
      skill: "audit",
      file: "scripts/run.sh",
    });
    expect(parseSkillPath("audit")).toBeNull();
    expect(validateSkillFilePath("nested/SKILL.md")).toBeNull();
  });

  it("validates role names before they enter persisted explorer refs", () => {
    expect(validateRoleName("reviewer")).toBeNull();
    expect(validateRoleName("bad/role")).not.toBeNull();
  });
});
