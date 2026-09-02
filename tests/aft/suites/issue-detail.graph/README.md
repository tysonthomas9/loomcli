# Issue-detail graph pilot

`flow.graph.yaml` is the authoritative product topology. Read it first: one UI
creation prefix reaches `detail-panel-ready`, which fans out to complete
human journeys covering description save/cancel, type, priority/owner, labels,
comments, lifecycle, title, dependency, and card reopen behavior.

`shared-steps.yaml` owns the parameterized setup blocks used by every replay,
including nested form-fill/create composition and shared state assertions.
`states.yaml` defines the observable contract at each node. The files under
`transitions/` contain ordinary AFT steps grouped by product concept, so browser
mechanics do not obscure the graph. Imported fragments never add edges.

Coverage is every leaf path plus the named golden journeys. The graph currently
compiles twelve transition-coverage cases plus two golden journeys, for fourteen
fresh-browser replays total. Each planned path
starts at `browser-ready`, replays issue creation through the New Issue modal,
and uses the compiler-provided `${AFT_CASE_ID}` to keep its issue, label, and
comment distinct. All mutations are performed by a human through mounted UI
controls; reloads and filtered URLs provide browser-visible persistence checks.
Reusable blocks reduce source duplication only: all fourteen paths still rerun
their expanded setup and assertions independently. The storyboard Flow view
folds those repeated captures by authored source while retaining each execution
under the collapsed card.

The deepened label, priority, and comment branches continue past their original
persistence checks to cover label removal, owner persistence on the card, and
empty-comment blocking. New branches cover type filtering, title validation and
rename persistence, deferred and review status placement, cancelling an unsaved
description edit, and adding a blocking dependency through the detail panel.

The standalone `/issues/:id` page is intentionally outside this graph. It
renders `IssueDetailView`, not the editable slide-over `IssueDetailPanel`, so it
is not an equivalent join state.
