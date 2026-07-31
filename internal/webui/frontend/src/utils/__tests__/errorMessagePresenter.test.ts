import { describe, expect, it } from "vitest";

import { presentServerErrorMessage } from "../errorMessagePresenter";

describe("presentServerErrorMessage", () => {
  it("maps duplicate-agent backend conflicts to a short user-facing sentence", () => {
    const raw =
      "create agent: fleetdb: POST /api/v1/E2E-WS-TASK/agents: HTTP 409: already exists: domain: already exists";

    expect(presentServerErrorMessage(raw, 409)).toEqual({
      raw,
      message: "An agent with this name already exists.",
    });
  });

  it("maps repo-less workspace errors to a clear next action", () => {
    const raw = "workspace has no repos for agent";

    expect(presentServerErrorMessage(raw, 400)).toEqual({
      raw,
      message:
        "This workspace has no repos yet — add one from the sidebar first.",
    });
  });

  it("falls back to the meaningful server tail with sentence case", () => {
    const raw =
      'create agent: domain: role "task" already exists and is not interactive; choose a different agent name';

    expect(presentServerErrorMessage(raw, 400)).toEqual({
      raw,
      message:
        'Role "task" already exists and is not interactive; choose a different agent name',
    });
  });
});
