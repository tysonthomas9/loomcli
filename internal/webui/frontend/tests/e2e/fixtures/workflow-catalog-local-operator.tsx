import { createRoot } from "react-dom/client";

import { WorkflowSourceModal } from "@/components/WorkflowSourceModal";
import { ToastProvider } from "@/hooks/ui";
import "@/styles/index.css";

const workspace =
  new URLSearchParams(window.location.search).get("workspace")?.trim() ||
  "TEST";
const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("missing proof root");
}

createRoot(rootElement).render(
  <ToastProvider>
    <WorkflowSourceModal
      isOpen
      workspaceId={workspace}
      workflowName="demo"
      onClose={() => undefined}
    />
  </ToastProvider>,
);
