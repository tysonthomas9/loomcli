import { describe, expect, it } from "vitest";

import {
  parseSkillPath,
  validateRoleName,
  validateSkillDescription,
  validateSkillFilePath,
  validateSkillName,
} from "../skillsPaths";

describe("skillsPaths", () => {
  it.each(["audit", "code-review", "a1-b2"])(
    "accepts valid skill name %s",
    (name) => expect(validateSkillName(name)).toBeNull(),
  );

  // The reserved names mirror Go's domain.skillReservedNames, which mirrors
  // fleet-db's models.skillReservedNames. "loom-skill-catalog" is the synthetic
  // catalog pointer the materializer owns; a stored skill by that name is
  // skipped at materialization, so catching it here keeps the failure inline
  // instead of a generic 422.
  it.each([
    "Claude",
    "anthropic",
    "loom-skill-catalog",
    "con",
    "bad--name",
    "bad_name",
  ])("rejects reserved or malformed skill name %s", (name) =>
    expect(validateSkillName(name)).not.toBeNull(),
  );

  it("accepts a one-line description", () => {
    expect(
      validateSkillDescription("Review a PR. Use before approving."),
    ).toBeNull();
  });

  // Each description becomes one line of the materialized skill index, so a
  // newline would split a single skill across two entries.
  it.each(["does <thing>", "line one\nline two", "tabbed\tsummary"])(
    "rejects description %j",
    (description) =>
      expect(validateSkillDescription(description)).not.toBeNull(),
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
