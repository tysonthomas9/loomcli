import { defineAgent, defineWorkflow } from "@flue/runtime";

// Flue HEAD (durable-streams) requires every workflow module to default-export a
// defineWorkflow() definition. This runner is unimplemented and fails closed, but
// it is one of the four bundled spec files, so the bundle only normalizes if it
// too default-exports a definition. The agent is a credential-free stub.
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run()),
});

// Round-trip through JSON so Flue HEAD's strict serializable-output check
// (which rejects undefined) never throws on optional result fields.
function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

// OpenShell task runner: not implemented.
//
// There is no real OpenShell integration anywhere in the repo. The previous
// implementation returned a synthetic `completed` result with no execution,
// which let task runs fake-complete. Until a real OpenShell runtime exists this
// runner fails closed so callers receive an explicit, terminal error instead of
// a fabricated success. The driver also excludes this entrypoint from manifests
// and guards it at resolve time.
export async function run() {
  return {
    status: "failed",
    exitCode: 1,
    errorClass: "openshell_runner_unimplemented",
    errorMessage: "OpenShell task runner is not implemented",
    runtimeMetadata: {
      task_runner: "openshell-task-runner",
    },
  };
}
