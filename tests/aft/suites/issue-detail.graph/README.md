# Issue-detail graph pilot

`flow.graph.yaml` is the authoritative product topology. Read it first: one UI
creation prefix reaches `detail-panel-ready`, which fans out to six complete
human journeys covering description, priority, labels, comments, lifecycle,
and card reopen behavior.

`states.yaml` defines the observable contract at each node. The files under
`transitions/` contain ordinary AFT steps grouped by product concept, so browser
mechanics do not obscure the graph. Imported fragments never add edges.

Coverage is every transition plus the named golden journeys. Each planned path
starts at `browser-ready`, replays issue creation through the New Issue modal,
and uses the compiler-provided `${AFT_CASE_ID}` to keep its issue, label, and
comment distinct. All mutations are performed by a human through mounted UI
controls; reloads and filtered URLs provide browser-visible persistence checks.

The standalone `/issues/:id` page is intentionally outside this graph. It
renders `IssueDetailView`, not the editable slide-over `IssueDetailPanel`, so it
is not an equivalent join state.
