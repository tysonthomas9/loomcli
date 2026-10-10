#!/usr/bin/env python3
"""Generate the PX.7 matrix suites (fake tier) and their real-GitHub copies.

The YAML is generated so every case uses the same step vocabulary; the real-GitHub
copies differ only in their suite name and header.
"""
import json, re, sys, pathlib

S = 'bash "$AFT_TESTS_DIR/scripts/loomgit-matrix.sh"'

# Dependents (Tyson, 2026-10-09): a dependent starts when its blocker's agent
# finishes, on the blocker's unreviewed revision; Approve, Apply and Publish
# still follow dependency order. build() is the one place that orders runs and
# reviews.
NEEDS = " [needs #943 dependents run]"

def run(args, intent):
    return [f"      - run: {json.dumps(S + ' ' + args)}", f"        intent: {json.dumps(intent)}"]

def wait_rev(case, slot, tries=4):
    out = []
    for i in range(1, tries + 1):
        out += run(f"wait-rev {case} {slot} {i} {tries}",
                   f"Task {slot} runs (real codex on the real tier) until it records a revision and waits in review ({i}/{tries})")
    return out

def task(case, slot, after="-", red=""):
    extra = f" {red}" if red else ""
    what = "writes a file containing FAIL, so its check is red" if red else "writes its own file"
    dep = f", blocked by task {after}" if after != "-" else ""
    return run(f"task {case} {slot} {after}{extra}", f"API client creates task {slot}{dep} in its own epic and starts its run; it {what}") + wait_rev(case, slot)

def build(case, specs, reviewed):
    """Run the tasks in specs [(slot, after|"-", "FAIL"|"", [writes-slot])] and review them.

    Every task is created up front in one epic and runs before any review;
    reviewed maps a slot to the steps that review it (approve, PR, merge...),
    always taken in specs order.
    """
    specs = [tuple(x) + ("",) * (4 - len(x)) for x in specs]
    spec = " ".join(slot + (f":{after}:{red}:{writes}" if writes else (f":{after}" if after != "-" or red else "") + (f":{red}" if red else "")) for slot, after, red, writes in specs)
    st = run(f"chain {case} {spec}", "API client creates the tasks (" + spec + ") in one epic and starts it once; a dependent runs when its blocker's agent finishes")
    for slot, *_ in specs:
        st += wait_rev(case, slot)
    for slot, *_ in specs:
        st += reviewed.get(slot, [])
    return st

def reject_ui(case, slot):
    # A code revision's Reject lives in the Revisions section and has no test id or reason box.
    return open_changes(case, slot) + [
        "      - wait:", "          fn: \"document.querySelector('[data-testid=revisions-section]')?.textContent.includes('Awaiting review')\"",
        "        intent: The revision is awaiting review, so Reject is live",
        "      - click:", "          role: button", "          name: Reject", "          exact: true",
        f"        intent: {json.dumps('Human clicks Reject on the revision of task ' + slot)}",
    ]


def open_changes(case, slot):
    return run(f"open-task {case} {slot}", f"Human opens task {slot}") + [
        "      - click:", "          role: tab", "          name: Changes", "          exact: true",
        f"        intent: {json.dumps('Human opens the Changes tab of task ' + slot)}",
        "      - wait:", "          fn: \"!!document.querySelector('[data-testid=task-changes-tab]')\"",
        "        intent: The Changes panel is mounted",
        "      - wait:", f"          text: matrix-{case}-{slot}.txt",
        f"        intent: The task's file is in the diff",
    ]

def human_create_pr(case, slot, base):
    return open_changes(case, slot) + [
        "      - expect:", "          enabled:", "            testid: approve-create-pr",
        f"        intent: {json.dumps('Approve and create PR is offered for task ' + slot)}",
        "      - click:", "          testid: approve-create-pr",
        f"        intent: {json.dumps('Human clicks Approve and create PR on task ' + slot)}",
    ] + run(f"pr {case} {slot} {base}", f"PR of {slot} opens on the forge based on {base}, and the task shows it")

def human_merge(case, slot):
    return run(f"open-task {case} {slot}", f"Human opens task {slot}") + [
        "      - wait:", "          fn: \"!!document.querySelector('[data-testid=approve-merge]:not([disabled])')\"",
        f"        intent: {json.dumps('Approve and merge is offered for task ' + slot)}",
        "      - click:", "          testid: approve-merge",
        f"        intent: {json.dumps('Human clicks Approve and merge on task ' + slot)}",
    ]

def lead(case, action, slot, want, intent):
    return run(f"lead-do {case} {action} {slot} {want}", "The lead (a real codex lead, told mid-session in its terminal, on the real tier) " + intent)

def lead_create_pr(case, slot, base):
    return lead(case, "approve", slot, "ok", f"approves task {slot}; Loom records a policy verdict by the lead and opens its PR") + \
        run(f"pr {case} {slot} {base}", f"PR of {slot} opens on the forge based on {base}, and the task shows it")

def settings(case, mode, approve, merge):
    return run(f"settings {case} {mode} {approve} {merge}",
               f"Human sets Delivery mode {mode}, Lead may approve {approve}, Lead may merge {merge} in Settings > Git; the server and a reload agree")

def setup(case, no_native=False):
    # GitHub always publishes stacks natively (D41); no_native only for the fake-tier negative case.
    return run(f"setup {case}{' no-native' if no_native else ''}", "Fixture: one repo with GitHub native stacks" + (" UNAVAILABLE" if no_native else "") + ", one Loom workspace and its lead (real tier: a clone of the run's sandbox repo)") + \
        run(f"lead-start {case}", "Real tier: the workspace's real codex lead is started from its agent page and its controlled runtime comes up")

def case(name, intent, steps, needs=None, labels=()):
    if needs is None:
        needs = any(re.search(r'loomgit-matrix\.sh\\" chain \S+ [^"]*:', l) for l in steps)
    for label, why in labels:
        name += f" [needs {label}]"
        intent += f" Expected to fail until {label}: {why}"
    if needs:
        name += NEEDS
        intent += " Needs the dependents-run change in #943; it fails on 8267795e0 until that lands."
    return [f"  - name: {json.dumps(name)}", f"    intent: {json.dumps(intent)}", "    steps:"] + steps

def walk():
    c = "walk"
    st = setup(c) + settings(c, "stack", "on", "off")
    st += build(c, [("a", "-", ""), ("b", "a", ""), ("c", "b", "")], {
        "a": human_create_pr(c, "a", "main") + run(f"ui {c} a open", "W1: task A shows PR A open, and keeps it after a reload"),
        "b": human_create_pr(c, "b", "a"),
        "c": human_create_pr(c, "c", "b") + run(f"ui {c} c open", "W2: task C shows its PR open on top of B's")})
    st += human_merge(c, "c") + run(f"merge-state {c} c waiting 'merges after #A, #B'", "W3: C's Approve and merge waits for the PRs below it") + \
        run(f"hold-open {c} 6 a b c", "W3: nothing merges") + run(f"ui {c} c open 'merges after #A, #B'", "W3: task C says it merges after #A, #B")
    # W5 before W4: B holds a merge-after permission when its review fix-up lands.
    st += human_merge(c, "b") + run(f"merge-state {c} b waiting 'merges after #A'", "W5: B gets Approve and merge first; it waits for A") + \
        run(f"comment {c} b 'Please rename: add a file matrix-walk-b-review.txt that says renamed by review.'", "W5: a reviewer comments on B's PR (on GitHub in the real tier); the signed webhook brings it to Loom") + \
        run(f"fixup {c} b rename", "W5: the feedback agent (real codex in the real tier) writes the fix-up in the task copy and completes it") + \
        run(f"fixup-pushed {c} b c", "W5: B's PR is updated without an Approve, and C is replayed on it") + \
        run(f"merge-state {c} b cancelled 'approve it again'", "W5: the fix-up cancels B's Approve and merge") + \
        run(f"merge-state {c} c waiting", "W5: C's Approve and merge still waits after the clean replay")
    st += human_merge(c, "a") + run(f"merged {c} a", "W4: A merges") + \
        run(f"rebuilt {c} b main", "W4: B's PR is rebuilt on main") + run(f"rebuilt {c} c b", "W4: C's PR still sits on B's and changes only C") + \
        run(f"hold-open {c} 6 b c", "W4: B, whose approval the fix-up cancelled, does not merge; nor does C above it") + \
        run(f"ui {c} a merged", "W4: task A shows its PR merged")
    st += human_merge(c, "b") + run(f"merged {c} b", "W6: B merges after its new Approve and merge") + run(f"merged {c} c", "W6: C merges by itself after its clean rebuild") + \
        run(f"ui {c} c merged", "W6: task C shows its PR merged")
    # W8 with the old mode's PRs still open: a second stack E-F stays open across the switch.
    st += build(c, [("e", "-", ""), ("f", "e", "")], {"e": human_create_pr(c, "e", "main"), "f": human_create_pr(c, "f", "e")})
    st += run(f"snapshot {c} stack", "W8: record the open stack E-F") + settings(c, "trunk", "on", "off")
    st += task(c, "d") + human_create_pr(c, "d", "main") + run(f"untouched {c} stack 2", "W8: the still-open stacked PRs E and F are untouched by the switch") + \
        run(f"ui {c} d open", "W8: task D shows its own PR to main") + run(f"ui {c} f open", "W8: task F still shows its stacked PR open")
    # W7 on a stacked PR: a hand push makes E diverge from the stack (F no longer sits on E's head).
    st += run(f"hand-push {c} e", "W7: someone pushes a commit to stacked PR E by hand, so E diverges from its stack") + human_merge(c, "e") + \
        run(f"merge-state {c} e stale_subject 'someone else pushed'", "W7: Loom refuses to merge the stacked PR someone else pushed to") + \
        run(f"not-overwritten {c} e 8", "W7: Loom never overwrites the hand-pushed commit") + run(f"ui {c} e open 'someone else pushed'", "W7: task E says someone else pushed")
    return case("W1-W8 stacked PR walk-through", "Stacked PRs with Lead may approve on and Lead may merge off: a human builds A, B, C as one stack, gives B merge permission before its review fix-up cancels it, merges bottom-up, switches to PR per task with stack E-F still open, and meets a hand push on stacked E", st)

def settings_cases():
    out = []
    c = "s1"
    st = setup(c) + settings(c, "stack", "on", "off")
    st += build(c, [("a", "-", ""), ("b", "a", ""), ("c", "b", "")], {
        "a": lead_create_pr(c, "a", "main"), "b": lead_create_pr(c, "b", "a"), "c": lead_create_pr(c, "c", "b")})
    st += lead(c, "merge", "a", "refused", "tries Approve and merge on A and is refused with 'Lead may merge is off'; nothing is queued and nothing merges") + run(f"hold-open {c} 4 a b c", "Nothing merges")
    st += human_merge(c, "a") + run(f"merged {c} a", "A merges") + run(f"rebuilt {c} b main", "B is rebuilt on main") + run(f"rebuilt {c} c b", "C stays on B") + run(f"ui {c} b open", "Task B shows its PR open")
    out += case("S1 Stacked, lead may approve on, lead may merge off", "The lead's approvals open a stacked A-B-C; the lead's merge is refused outright (D38); a human Approve and merge on A merges it and B, C are rebuilt", st,
                labels=[("P3.16", "today the lead's merge is refused with a different message (D38's 'Lead may merge is off').")])

    c = "s2"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += task(c, "a") + lead(c, "approve", "a", "refused", "tries to approve A and is refused with review_required; the task stays in review") + \
        run(f"no-pr {c} a 5", "No PR opens") + human_create_pr(c, "a", "main") + run(f"ui {c} a open", "Task A shows its PR")
    out += case("S2 Stacked, lead may approve off", "With Lead may approve off the lead cannot approve; a human approval opens the PR", st)

    c = "s3"
    st = setup(c) + settings(c, "stack", "on", "when_green")
    st += build(c, [("a", "-", ""), ("b", "a", "FAIL"), ("c", "b", "")], {
        "a": lead_create_pr(c, "a", "main") + run(f"merged {c} a", "The lead merges green A by itself"),
        "b": lead_create_pr(c, "b", "main") + run(f"checks {c} b red", "B's required check is red"),
        "c": lead_create_pr(c, "c", "b") + run(f"hold-open {c} 10 b c", "The lead stops at red B; C waits")})
    st += run(f"comment {c} b 'The check fails: remove the line FAIL from matrix-s3-b.txt and keep the rest.'", "A reviewer asks for B's fix") + \
        run(f"fixup {c} b green", "The feedback agent removes FAIL") + run(f"fixup-pushed {c} b c", "B's PR is updated and C replayed") + \
        run(f"merged {c} b c", "The lead merges B, then C") + run(f"ui {c} c merged", "Task C shows its PR merged")
    out += case("S3 Stacked, lead may approve on, lead may merge when green", "The lead merges green PRs bottom-up by itself, stops at red B and continues after B's fix", st)

    c = "s4"
    st = setup(c) + settings(c, "stack", "off", "when_green")
    st += build(c, [("a", "-", ""), ("b", "a", ""), ("c", "b", "")], {
        "a": human_create_pr(c, "a", "main") + run(f"merged {c} a", "The lead merges green A without asking"),
        "b": human_create_pr(c, "b", "main") + run(f"merged {c} b", "The lead merges green B"),
        "c": human_create_pr(c, "c", "main") + run(f"merged {c} c", "The lead merges green C") + run(f"ui {c} c merged", "Task C shows its PR merged")})
    out += case("S4 Stacked, lead may approve off, lead may merge when green", "A human opens each PR; the lead merges the green ones bottom-up without asking", st)

    c = "s5"
    st = setup(c) + settings(c, "trunk", "on", "off")
    st += build(c, [("a", "-", ""), ("b", "-", ""), ("c", "a", "")], {
        "a": lead_create_pr(c, "a", "main"), "b": lead_create_pr(c, "b", "main"),
        "c": lead(c, "approve", "c", "ok", "approves C, which depends on A") + run(f"no-pr {c} c 8", "C gets no PR while A is unmerged")})
    st += human_merge(c, "a") + run(f"merged {c} a", "A lands") + run(f"pr {c} c main", "C now gets its own PR to main") + \
        run(f"rebuilt {c} c main", "C's PR changes only C") + run(f"ui {c} b open", "Task B shows its own PR")
    out += case("S5 PR per task, lead may approve on, lead may merge off", "Independent A and B get their own PRs to main; C, which depends on A, gets its PR once A lands", st)

    c = "s6"
    st = setup(c) + settings(c, "trunk", "on", "when_green")
    st += build(c, [("a", "-", ""), ("b", "-", "FAIL"), ("c", "a", "")], {
        "a": lead_create_pr(c, "a", "main") + run(f"merged {c} a", "A merges by itself"),
        "b": lead_create_pr(c, "b", "main") + run(f"checks {c} b red", "B is red"),
        "c": lead_create_pr(c, "c", "main") + run(f"merged {c} c", "C merges by itself")})
    st += run(f"hold-open {c} 6 b", "Red B stays open and blocks nobody") + run(f"ui {c} b open", "Task B shows its PR open")
    out += case("S6 PR per task, lead may approve on, lead may merge when green", "Green PRs merge by themselves; red B stays open and never blocks A or C", st)

    c = "s7"
    st = setup(c) + settings(c, "trunk", "off", "off")
    st += task(c, "a") + human_create_pr(c, "a", "main") + human_merge(c, "a") + run(f"merged {c} a", "A merges") + run(f"ui {c} a merged", "Task A shows its PR merged")
    out += case("S7 PR per task, human does everything", "A human approve opens the PR to main and a human Approve and merge merges it", st)

    c = "s8"
    st = setup(c) + settings(c, "trunk", "off", "when_green")
    st += build(c, [("a", "-", ""), ("b", "-", "")], {"a": human_create_pr(c, "a", "main"), "b": human_create_pr(c, "b", "main")}) + \
        run(f"merged {c} a b", "The lead merges both green PRs without asking") + run(f"ui {c} b merged", "Task B shows its PR merged")
    out += case("S8 PR per task, lead may approve off, lead may merge when green", "A human opens the PRs; the lead merges the green ones without stack order", st)

    c = "s9"
    st = setup(c) + settings(c, "stack", "on", "off")
    st += build(c, [("a", "-", ""), ("b", "a", "")], {"a": human_create_pr(c, "a", "main"), "b": human_create_pr(c, "b", "a")})
    st += run(f"snapshot {c} stack", "Record the open stack") + settings(c, "trunk", "on", "off")
    st += task(c, "d") + human_create_pr(c, "d", "main") + run(f"untouched {c} stack", "A and B are untouched") + run(f"ui {c} d open", "Task D shows its own PR")
    out += case("S9 Switching delivery mode", "With stack A-B open, switching to PR per task leaves it alone and new task D gets its own PR to main", st)

    c = "s10"
    st = setup(c) + settings(c, "stack", "on", "off") + \
        lead(c, "set-approve", "-", "refused", "tries to turn Lead may approve off; refused (403), settings unchanged") + \
        lead(c, "set-merge", "-", "refused", "tries to set Lead may merge to when green; refused (403), settings unchanged") + \
        lead(c, "set-mode", "-", "ok", "switches Delivery mode to PR per task; allowed") + \
        run(f"settings-ui {c} trunk on off", "Settings > Git shows the lead's mode change and the unchanged lead permissions")
    out += case("S10 Lead permissions are human only", "The lead may change delivery mode but never Lead may approve or Lead may merge", st)
    return out

def variants(real=False):
    out = []
    if real:
        # Real tier only: the fake tier has no running lead, and a skipped step would read as a pass.
        c = "l1"
        st = setup(c) + run(f"lead-epic-midsession {c}", "An epic assigned to the ALREADY RUNNING real lead is delivered to it mid-session")
        out += case("L1 Mid-session epic assignment to a running lead", "The seam live-interactive ll-lead-assignment leaves open: assignment delivered into a lead that is already running", st)

    c = "n1"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += task(c, "a") + lead(c, "cli-approve", "a", "refused", "runs loom approve on A with Lead may approve off; Loom refuses with 'lead approval policy is off', no verdict is recorded and A stays in review") + \
        run(f"no-pr {c} a 5", "No PR opens") + human_create_pr(c, "a", "main")
    out += case("N1 The lead's CLI approve cannot bypass Lead may approve off [needs P2.25]", "loom approve run by the lead must be refused with 'lead approval policy is off', record no verdict and leave the task in review. Expected to fail until P2.25: the CLI records a human actor whoever runs it.", st, needs=False)

    c = "n2"
    st = setup(c) + settings(c, "trunk", "off", "off")
    st += task(c, "a", "-", "FAIL") + human_create_pr(c, "a", "main") + run(f"checks {c} a red", "A's required check is red")
    st += human_merge(c, "a") + run(f"merge-state {c} a blocked", "Approve and merge on red A waits as blocked") + run(f"hold-open {c} 6 a", "Red A does not merge")
    st += run(f"comment {c} a 'The check fails: remove the line FAIL from matrix-n2-a.txt and keep the rest.'", "A reviewer asks for A's fix") + \
        run(f"fixup {c} a green", "The feedback agent removes FAIL") + run(f"fixup-pushed {c} a", "A's PR is updated with the fix-up") + \
        run(f"merge-state {c} a cancelled 'approve it again'", "The fix-up cancels the old Approve and merge") + run(f"checks {c} a green", "A is green now")
    st += human_merge(c, "a") + run(f"merged {c} a", "The second Approve and merge merges A") + run(f"ui {c} a merged", "Task A shows its PR merged")
    out += case("N2 Approve and merge on a red PR, then recovery", "A red PR's Approve and merge blocks; a review fix-up cancels it; approving the green fix-up merges", st)

    c = "n3"
    st = setup(c) + settings(c, "stack", "on", "off")
    st += build(c, [("a", "-", ""), ("b", "a", "")], {"a": lead_create_pr(c, "a", "main"), "b": lead_create_pr(c, "b", "a")})
    st += lead(c, "request-merge", "b", "refused", "asks to merge its stack through B with Lead may merge off; Loom refuses with 'Lead may merge is off', queues no request and merges nothing (D38: no human Confirm step)") + \
        run(f"hold-open {c} 6 a b", "A and B stay open") + run(f"ui {c} b open", "Task B shows its PR open")
    out += case("N3 The lead's merge request is refused when Lead may merge is off", "D38: with Lead may merge off, Loom refuses the lead's merge request outright; nothing waits for a human to confirm", st,
                labels=[("P3.16", "today Loom queues the request for a human to confirm.")])

    c = "r1"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += task(c, "a") + reject_ui(c, "a") + run(f"reject {c} a", "Task A is open again, with a reject verdict and no PR") + run(f"rerun {c} a", "The epic runner runs A again") + \
        wait_rev(c, "a") + human_create_pr(c, "a", "main") + run(f"ui {c} a open", "Task A shows its PR")
    out += case("R1 Reject, rerun, approve", "A rejected task runs again and its new revision opens the PR", st)

    c = "d1"
    st = setup(c) + settings(c, "stack", "on", "off")
    st += run(f"chain {c} a b:a", "API client creates A and B (blocked by A) in one epic and starts it") + wait_rev(c, "a") + wait_rev(c, "b") + \
        run(f"ran-before-approval {c} b a", "B ran as soon as A's agent finished, on A's unreviewed revision; A is still in review") + \
        human_create_pr(c, "a", "main") + human_create_pr(c, "b", "a") + run(f"ui {c} b open", "Task B shows its PR on top of A's")
    out += case("D1 A dependent runs before its blocker is approved", "B runs on A's frozen revision before anyone reviews A; the PRs still open in dependency order", st, needs=True)

    c = "d2"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += run(f"chain {c} a b:a", "API client creates A and B (blocked by A) in one epic and starts it") + wait_rev(c, "a") + wait_rev(c, "b") + \
        reject_ui(c, "a") + run(f"reject {c} a", "A is open again with a reject verdict and no PR") + \
        run(f"stale {c} b a", "B, built on the rejected A, is stale, has no PR and offers a rebuild")
    out += case("D2 Rejecting a blocker makes its dependent stale", "Rejecting A marks B, which ran on A's revision, stale with a rebuild offer", st, needs=True)

    c = "d3"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += run(f"chain {c} a b:a", "API client creates A and B (blocked by A) in one epic and starts it") + wait_rev(c, "a") + wait_rev(c, "b") + \
        open_changes(c, "b") + [
        "      - click:", "          testid: approve-create-pr", "        intent: Human clicks Approve and create PR on B before A is reviewed",
    ] + run(f"approve-waits {c} b a", "B's approval waits with \"waiting for A to be approved\"; no PR opens") + \
        human_create_pr(c, "a", "main") + run(f"pr {c} b a", "Once A is approved, B's waiting approval opens its PR on A's") + \
        run(f"ui {c} b open", "Task B shows its PR")
    out += case("D3 Approving a dependent before its blocker waits", "Approve on B before A waits for A; approving A then publishes A and B in order", st, needs=True)

    c = "x1"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += build(c, [("a", "-", ""), ("b", "a", "", "a")], {"a": human_create_pr(c, "a", "main"), "b": human_create_pr(c, "b", "a")})
    st += human_merge(c, "b") + run(f"merge-state {c} b waiting 'merges after #A'", "B's Approve and merge waits for A") + \
        run(f"hand-push {c} a a 'changed outside Loom'", "Someone rewrites A's file on A's PR by hand") + run(f"checks {c} a green", "A's check is green") + \
        run(f"outside-merge {c} a", "Someone merges A on the forge, outside Loom") + \
        run(f"merge-state {c} b reapproval_required 'not clean'", "B's rebuild on main does not apply cleanly, so its approval asks to approve again") + \
        run(f"hold-open {c} 6 b", "B does not merge") + run(f"ui {c} b open 'approve again'", "Task B asks to approve again")
    out += case("X1 A rebuild that does not apply cleanly needs approving again", "When A lands with different content, B's rebuild conflicts and B's Approve and merge asks for a new approval", st)

    c = "x2"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += build(c, [("a", "-", ""), ("b", "a", "")], {"a": human_create_pr(c, "a", "main"), "b": human_create_pr(c, "b", "a")})
    st += human_merge(c, "b") + run(f"merge-state {c} b waiting 'merges after #A'", "B's Approve and merge waits for A") + \
        run(f"open-task {c} b", "Human opens task B") + [
        "      - wait:", "          fn: \"!!document.querySelector('[data-testid=cancel-auto-merge]:not([disabled])')\"", "        intent: Cancel merge after is offered",
        "      - click:", "          testid: cancel-auto-merge", "        intent: Human cancels B's merge after",
    ] + run(f"merge-state {c} b cancelled", "B's Approve and merge is cancelled") + \
        human_merge(c, "a") + run(f"merged {c} a", "A merges") + run(f"rebuilt {c} b main", "B is rebuilt on main") + \
        run(f"hold-open {c} 8 b", "B, whose merge after was cancelled, does not merge") + run(f"ui {c} b open", "Task B shows its PR open")
    out += case("X2 Cancelling merge after by hand", "A human cancels B's waiting Approve and merge; A merges and B stays open", st)

    if not real:
        # Fake tier only: real GitHub cannot be made to lack native stacks on demand.
        c = "n4"
        st = setup(c, no_native=True) + settings(c, "stack", "off", "off")
        st += task(c, "a") + open_changes(c, "a") + [
            "      - click:", "          testid: approve-create-pr", "        intent: Human clicks Approve and create PR on task A",
        ] + run(f"native-unavailable {c} a", "Publishing fails with a clear 'native stacks unavailable' error, shown on the task; no PR opens and Loom does not fall back to its own publisher")
        out += case("N4 No native stacks: a clear error, no fallback [needs D41]", "D41: GitHub always uses native stacks. When they are unavailable, publishing stops with a clear error instead of falling back to Loom's own publisher. Expected to fail until D41: today Loom falls back.", st, needs=False)

    return out

HEADER = """# PX.7 Git settings matrix{what}. Generated by tests/aft/scripts/gen-matrix-suites.py;
# edit the generator, not this file. {tier}
suite: {suite}
classification: {{ area: git, feature: git-settings-matrix, surface: mixed, purpose: journey }}
baseUrl: "${{AFT_BASE_URL:-http://127.0.0.1:3100}}"
teardown: >-
  for c in {cases}; do bash "$AFT_TESTS_DIR/scripts/loomgit-matrix.sh" teardown "$c" || true; done
tests:
"""

def emit(path, suite, what, tier, cases, body):
    text = HEADER.format(suite=suite, what=what, tier=tier, cases=" ".join(cases)) + "\n".join(body) + "\n"
    pathlib.Path(path).write_text(text)

def main(root):
    root = pathlib.Path(root)
    fake = "Fake-forge tier: runs on every make test-aft (--suite 'loomgit-matrix-*' --no-agent)."
    real = "REAL tier: real GitHub sandbox repo + real codex; only via run-aft.sh --real-github."
    for suite, what, cases, body in (("matrix-walk", ": stacked PR walk-through W1-W8", ["walk"], walk()),
                                     ("matrix-settings", ": settings cases S1-S10", [f"s{i}" for i in range(1, 11)], settings_cases()),
                                     ("matrix-variants", ": lead, negative, recovery and dependent-run variants L1, N1-N4, R1, D1-D3, X1-X2", ["l1", "n1", "n2", "n3", "n4", "r1", "d1", "d2", "d3", "x1", "x2"], None)):
        fake_body = body if body is not None else variants()
        real_body = body if body is not None else variants(real=True)
        fake_cases = [c for c in cases if c != "l1"]  # L1 is real-tier only
        emit(root / "suites" / f"loomgit-{suite}.test.yaml", f"loomgit-{suite}", what.replace("L1, ", ""), fake, fake_cases if body is None else cases, fake_body)
        real_cases = [c for c in cases if c != "n4"]  # N4 is fake-tier only
        emit(root / "real-github-suites" / f"real-github-{suite}.test.yaml", f"real-github-{suite}", what.replace("N1-N4", "N1-N3"), real, real_cases if body is None else cases, real_body)

def check(root):
    """Exit 1 when the committed suites differ from what the generator writes."""
    import tempfile
    root = pathlib.Path(root)
    with tempfile.TemporaryDirectory() as tmp:
        t = pathlib.Path(tmp)
        (t / "suites").mkdir()
        (t / "real-github-suites").mkdir()
        main(t)
        stale = [str(p.relative_to(t)) for p in sorted(t.rglob("*.yaml"))
                 if not (root / p.relative_to(t)).is_file() or (root / p.relative_to(t)).read_text() != p.read_text()]
    if stale:
        print("stale generated suites: " + ", ".join(stale), file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    args = sys.argv[1:]
    if args and args[0] == "--check":
        check(args[1] if len(args) > 1 else pathlib.Path(__file__).resolve().parents[1])
    else:
        main(args[0] if args else pathlib.Path(__file__).resolve().parents[1])
