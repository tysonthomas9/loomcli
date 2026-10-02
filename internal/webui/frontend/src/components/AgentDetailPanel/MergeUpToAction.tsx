import { useState } from "react";

import {
  useMergeUpToForm,
  type MergeStackView,
} from "@/hooks/workspace/useMergeUpTo";

import styles from "./CreatePRAction.module.css";
import { MergeRequestCards } from "./MergeRequestCards";
import actionStyles from "./GitActionBar.module.css";

interface MergeUpToActionProps {
  workspaceId: string;
  agentName: string;
}

function MergeProgress({ view }: { view: MergeStackView }): JSX.Element {
  return (
    <div aria-live="polite">
      <p>
        {view.backend} merge: {view.phase || "awaiting confirmation"}
        {view.reason ? ` — ${view.reason}` : ""}
      </p>
      <ul>
        {view.layers.map((layer) => (
          <li key={layer.change}>
            {layer.change}: {layer.state} ({layer.head.slice(0, 12)})
          </li>
        ))}
      </ul>
    </div>
  );
}

function MergeFields({
  stackId,
  target,
  setStackId,
  setTarget,
  clear,
}: {
  stackId: string;
  target: string;
  setStackId: (value: string) => void;
  setTarget: (value: string) => void;
  clear: () => void;
}): JSX.Element {
  return (
    <>
      <label className={actionStyles.inlineLabel}>
        Stack ID{" "}
        <input
          value={stackId}
          onChange={(event) => {
            setStackId(event.target.value);
            clear();
          }}
        />
      </label>
      <label className={actionStyles.inlineLabel}>
        Up to layer{" "}
        <input
          value={target}
          onChange={(event) => {
            setTarget(event.target.value);
            clear();
          }}
        />
      </label>
    </>
  );
}

export function MergeUpToAction({
  workspaceId,
  agentName,
}: MergeUpToActionProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const form = useMergeUpToForm(workspaceId, agentName);
  return (
    <>
      <MergeRequestCards workspaceId={workspaceId} agentName={agentName} />
      <button
        type="button"
        className={styles.createPrBtn}
        onClick={() => setOpen(!open)}
      >
        Merge stack
      </button>
      {open && (
        <div className={styles.prForm}>
          <MergeFields
            stackId={form.stackId}
            target={form.target}
            setStackId={form.setStackId}
            setTarget={form.setTarget}
            clear={form.clear}
          />
          <button
            type="button"
            disabled={form.busy || !form.stackId || !form.target}
            onClick={() => void form.refresh()}
          >
            Show merge state
          </button>
          {form.view && <MergeProgress view={form.view} />}
          {form.view && !form.view.phase && (
            <button
              type="button"
              disabled={form.busy}
              onClick={() => void form.submit()}
            >
              Confirm merge request
            </button>
          )}
          {form.error && <p role="alert">{form.error}</p>}
        </div>
      )}
    </>
  );
}
