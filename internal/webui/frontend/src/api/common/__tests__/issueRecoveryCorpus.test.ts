import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { prepareIssueRecovery } from "../issueRecovery";
import type { RecoveryHandle } from "../recoveryHandle";

// This is the same file consumed by the Go Fleet client conformance test.
const corpus = JSON.parse(
  readFileSync(
    new URL(
      "../../../../../../backend/fleet/testdata/issue_recovery_corpus.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as Array<{
  name: string;
  valid: boolean;
  document: string;
  selected_issue_id?: string;
}>;
const now = Date.parse("2026-09-05T00:00:00Z");
const offer: RecoveryHandle = {
  handle: "A".repeat(43),
  source_identity: "s1.Zml4dHVyZQ",
  workspace: "WS",
  source_repos: [],
  expires_at: "2026-09-05T00:01:00Z",
  manifest: "fleet.issue-workspace.v5",
};

describe("shared native recovery corpus", () => {
  for (const entry of corpus) {
    it(entry.name, () => {
      const read = () =>
        prepareIssueRecovery(
          entry.document,
          offer,
          offer.handle,
          now,
          entry.selected_issue_id,
        );
      if (entry.valid) {
        const result = read();
        expect(result.document).toBe(entry.document);
        expect(Object.isFrozen(result)).toBe(true);
      } else expect(read).toThrow();
    });
  }
});
