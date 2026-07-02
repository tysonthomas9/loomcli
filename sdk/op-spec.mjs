// op-spec.mjs — SOURCE OF TRUTH for GENERATED @loom/sdk driver ops.
//
// Run `npm run gen` (node gen.mjs) to regenerate the managed `<gen:namespaces>`
// regions in driver.js and driver.d.ts, plus the ops + client.namespaces
// entries in api-surface.v1.json. This is the pipeline that keeps the TS SDK
// surface in sync with the Go driver ops from ONE declaration — add an op here
// and regenerate instead of hand-editing three files in lockstep.
//
// Pre-codegen ops (claim-ready, epic-get, exec-task, connectors, …) remain
// hand-written for now; they can migrate into this spec incrementally. New op
// families (issues, …) are declared here and fully generated.
//
// Field `name`s are the camelCase WIRE names sent to POST /driver/{op}; the Go
// handler decodes the same names. `type` is the TypeScript type for driver.d.ts.

export const generatedNamespaces = [
  {
    namespace: "issues",
    doc: "Fleet-db card read/write for workflows (thin IssueBackend pass-throughs).",
    ops: [
      {
        method: "get",
        op: "issue-get",
        httpMethod: "POST",
        result: "Record<string, unknown> | null",
        fields: [{ name: "issueId", type: "string", required: true }],
      },
      {
        method: "list",
        op: "issue-list",
        httpMethod: "POST",
        result: "Record<string, unknown>[] | null",
        fields: [
          { name: "externalRef", type: "string" },
          { name: "type", type: "string" },
          { name: "status", type: "string" },
          { name: "limit", type: "number" },
        ],
      },
      {
        method: "listComments",
        op: "issue-list-comments",
        httpMethod: "POST",
        result: "Record<string, unknown>[] | null",
        fields: [{ name: "issueId", type: "string", required: true }],
      },
      {
        method: "comment",
        op: "issue-comment",
        httpMethod: "POST",
        result: "Record<string, unknown> | null",
        fields: [
          { name: "issueId", type: "string", required: true },
          { name: "body", type: "string", required: true },
        ],
      },
      {
        method: "update",
        op: "issue-update",
        httpMethod: "POST",
        result: "Record<string, unknown> | null",
        fields: [
          { name: "issueId", type: "string", required: true },
          { name: "status", type: "string" },
          { name: "priority", type: "number" },
          { name: "labels", type: "string[]" },
          { name: "assignee", type: "string" },
          { name: "externalRef", type: "string" },
        ],
      },
      {
        method: "addLabel",
        op: "issue-add-label",
        httpMethod: "POST",
        result: "Record<string, unknown> | null",
        fields: [
          { name: "issueId", type: "string", required: true },
          { name: "label", type: "string", required: true },
        ],
      },
      {
        method: "removeLabel",
        op: "issue-remove-label",
        httpMethod: "POST",
        result: "Record<string, unknown> | null",
        fields: [
          { name: "issueId", type: "string", required: true },
          { name: "label", type: "string", required: true },
        ],
      },
    ],
  },
  {
    namespace: "roles",
    doc: "Read-only Role (behavior-config) records + prompt body for prompt agents.",
    ops: [
      {
        method: "get",
        op: "role-get",
        httpMethod: "POST",
        result: "{ role: Record<string, unknown> | null; prompt: string } | null",
        fields: [{ name: "name", type: "string", required: true }],
      },
    ],
  },
];
