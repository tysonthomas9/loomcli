# Move a Work Item with one atomic FleetDB command

Status: accepted

Cross-workspace Work Item move uses one FleetDB command that creates the
target and closes the source atomically. `internal/app/workitemmove` owns the
consumer-defined command seam and authorizes both workspaces; Work Items and
Workspace retain ownership of their records and expose no low-level transport.
This rejects the former create/comment/close sequence because a crash could
duplicate the target or leave contradictory source and target state.

The command requires one FleetDB deployment, exact source revision, target
workspace, and idempotency request. It rejects claimed, assigned, running, or
dependency-connected sources. The target receives a target-workspace ID and
starts open and unassigned; the closed immutable source retains history and a
`moved_to` reference, while the target records `moved_from`. Exact replay
returns the original target and divergent replay conflicts.
