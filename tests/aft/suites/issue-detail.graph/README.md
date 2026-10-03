# Issue-detail graph pilot

`flow.graph.yaml` is the authoritative product topology. Read it first: one UI
creation prefix reaches `detail-panel-ready`, which fans out to complete
human journeys covering description save/cancel, type, assignee, labels,
comments, lifecycle, title, dependency, and card reopen behavior. A second
creation branch from `create-form-ready` builds an epic and a child task in
the same modal and covers agent claims of that child (STACKED-PRS-29).

`shared-steps.yaml` owns the parameterized setup blocks used by every replay,
including nested form-fill/create composition and shared state assertions.
`states.yaml` defines the observable contract at each node. The files under
`transitions/` contain ordinary AFT steps grouped by product concept, so browser
mechanics do not obscure the graph. Imported fragments never add edges.

Coverage is every leaf path plus the named golden journeys. The graph currently
has 47 states and 48 transitions, and compiles fourteen transition-coverage
cases plus three golden journeys, for seventeen fresh-browser replays total. Each planned path
starts at `browser-ready`, replays issue creation through the New Issue modal,
and uses the compiler-provided `${AFT_CASE_ID}` to keep its issue, label, and
comment distinct. All human mutations go through mounted UI controls; reloads
and filtered URLs provide browser-visible persistence checks. The only API
steps are the agent's claims in `transitions/claim.yaml` (plus the id lookup
each claim needs), because no UI control calls the claim endpoint.
Reusable blocks reduce source duplication only: all fourteen paths still rerun
their expanded setup and assertions independently. The storyboard Flow view
folds those repeated captures by authored source while retaining each execution
under the collapsed card.

The deepened label, priority, and comment branches continue past their original
persistence checks to cover label removal, assignee persistence on the card, and
empty-comment blocking. New branches cover type filtering, title validation and
rename persistence, deferred and review status placement, cancelling an unsaved
description edit, and adding a blocking dependency through the detail panel.

The claim branch (`transitions/claim.yaml`) starts at `create-form-ready`: the
human creates an epic, then a child task with that epic chosen in the Epic
field, and sees the child in Ready inside the epic's swim lane. From there:

- the agent claims the ready child and the open panel switches to in progress
  (joins `in-progress-detail`);
- the human closes the panel (joins `issue-card-ready`) and blocks the child
  through the existing dependency branch; after `blocked-on-board` the agent's
  claim is refused and the card stays in Blocked. A golden journey pins this
  path to the epic prefix;
- the human creates another task, opens the epic from its lane title, and
  blocks the epic on that task; the child moves to Blocked, and the agent's
  claim is refused while the child stays open.

The standalone `/issues/:id` page is intentionally outside this graph. It
renders `IssueDetailView`, not the editable slide-over `IssueDetailPanel`, so it
is not an equivalent join state.
