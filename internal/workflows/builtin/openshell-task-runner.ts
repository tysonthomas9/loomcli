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
