import {
  captureLocalOperatorLaunchFromFragment,
  exchangeLocalOperatorLaunch,
} from "@/api/workflows/localOperatorSession";

interface ProofEvent {
  stage:
    | "capture-start"
    | "fragment-checked"
    | "exchange-start"
    | "exchange-complete"
    | "render-start";
  hadLaunchMaterial?: boolean;
  launchCaptured?: boolean;
  launchMaterialErased?: boolean;
  hash?: string;
}

interface WorkflowCatalogProof {
  events: ProofEvent[];
  error?: string;
}

declare global {
  interface Window {
    __workflowCatalogProof: WorkflowCatalogProof;
  }
}

const proof: WorkflowCatalogProof = { events: [] };
window.__workflowCatalogProof = proof;
const proofWorkspace =
  new URLSearchParams(window.location.search).get("workspace")?.trim() ||
  "TEST";

proof.events.push({
  stage: "capture-start",
  hadLaunchMaterial:
    window.location.hash.includes("loom_launch=") ||
    window.location.hash.includes("loom_workspace="),
});

try {
  const launch = captureLocalOperatorLaunchFromFragment();
  proof.events.push({
    stage: "fragment-checked",
    launchCaptured: launch !== null,
    launchMaterialErased:
      !window.location.href.includes("loom_launch=") &&
      !window.location.href.includes("loom_workspace="),
    hash: window.location.hash,
  });

  if (launch) {
    proof.events.push({ stage: "exchange-start" });
    await exchangeLocalOperatorLaunch(launch);
    proof.events.push({ stage: "exchange-complete" });
  }

  await import("@/styles/index.css");
  const [{ createRoot }, { ToastProvider }, { WorkflowSourceModal }] =
    await Promise.all([
      import("react-dom/client"),
      import("@/hooks/ui"),
      import("@/components/WorkflowSourceModal"),
    ]);

  proof.events.push({ stage: "render-start" });
  const rootElement = document.getElementById("root");
  if (!rootElement) throw new Error("missing proof root");
  createRoot(rootElement).render(
    <ToastProvider>
      <WorkflowSourceModal
        isOpen
        workspaceId={proofWorkspace}
        workflowName="demo"
        onClose={() => undefined}
      />
    </ToastProvider>,
  );
} catch (error) {
  proof.error = error instanceof Error ? error.message : String(error);
  const rootElement = document.getElementById("root");
  if (rootElement) {
    rootElement.textContent = "Workflow Catalog proof failed to boot";
  }
}

export {};
