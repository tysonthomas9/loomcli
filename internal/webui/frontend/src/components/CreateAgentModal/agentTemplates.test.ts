import { describe, expect, it } from "vitest";

import {
  AGENT_TEMPLATES,
  NEW_ROLE_TEMPLATE,
  SCRIPTED_WORKFLOW_TEMPLATES,
} from "./agentTemplates";

describe("agent template role defaults", () => {
  it("creates new prompt-agent roles in the safe coder phase", () => {
    expect(NEW_ROLE_TEMPLATE.roleCreate?.taskFilter).toBe("has_design");
    expect(
      AGENT_TEMPLATES.find((template) => template.id === "role-new")?.roleCreate
        ?.taskFilter,
    ).toBe("has_design");
  });

  it("pins review workflows to Codex while bug-fix stays workspace-resolved", () => {
    const workflows = new Map(
      SCRIPTED_WORKFLOW_TEMPLATES.map((template) => [
        template.id,
        template.workflow,
      ]),
    );

    expect(workflows.get("review-loop-agent")?.requiredBackend).toBe("codex");
    expect(workflows.get("local-review-agent")?.requiredBackend).toBe("codex");
    expect(workflows.get("bug-fix-agent")?.requiredBackend).toBeUndefined();
  });
});
