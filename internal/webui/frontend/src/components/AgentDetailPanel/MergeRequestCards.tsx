import {
  useMergeRequests,
  type MergeRequestView,
} from "@/hooks/workspace/useMergeUpTo";

function MergeRequestCard({
  request,
  busy,
  onConfirm,
}: {
  request: MergeRequestView;
  busy: boolean;
  onConfirm: () => void;
}): JSX.Element {
  return (
    <section aria-label={`Merge request ${request.id}`}>
      <p>
        {request.requested_kind} {request.requested_by} asks to merge{" "}
        {request.stack_id} up to {request.target}. Expires{" "}
        {new Date(request.expires_at).toLocaleTimeString()}.
      </p>
      <table>
        <thead>
          <tr>
            <th>Layer</th>
            <th>PR</th>
            <th>Head</th>
            <th>Checks</th>
            <th>Review</th>
          </tr>
        </thead>
        <tbody>
          {request.layers.map((layer) => (
            <tr key={layer.change}>
              <td>{layer.change}</td>
              <td>
                {layer.pr_url ? (
                  <a href={layer.pr_url} target="_blank" rel="noreferrer">
                    {layer.pr_url}
                  </a>
                ) : (
                  "none"
                )}
              </td>
              <td>
                <code>{layer.head}</code>
              </td>
              <td>{layer.checks}</td>
              <td>{layer.review}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <button type="button" disabled={busy} onClick={onConfirm}>
        Confirm merge up to {request.target}
      </button>
    </section>
  );
}

/** Confirm cards for merge requests a lead created; only a human confirms. */
export function MergeRequestCards({
  workspaceId,
  agentName,
}: {
  workspaceId: string;
  agentName: string;
}): JSX.Element | null {
  const { pending, error, busy, confirm } = useMergeRequests(
    workspaceId,
    agentName,
  );
  if (pending.length === 0 && !error) return null;
  return (
    <div>
      {pending.map((request) => (
        <MergeRequestCard
          key={request.id}
          request={request}
          busy={busy}
          onConfirm={() => void confirm(request)}
        />
      ))}
      {error && <p role="alert">{error}</p>}
    </div>
  );
}
