#!/usr/bin/env python3
"""Generate the PX.7 matrix suites (fake tier) and their real-GitHub copies.

The YAML is generated so every case uses the same step vocabulary; the real-GitHub
copies differ only in their suite name and header.
"""
import json, sys, pathlib

S = 'bash "$AFT_TESTS_DIR/scripts/loomgit-matrix.sh"'

# Dependency policy, in one place (P1.26): a task in review blocks its
# dependents, so a dependent's task step is placed after its blocker's review is
# approved. If dependents should only wait for the blocker's run to finish, set
# "run": dependents then start right after the blocker's revision exists. The
# driver asserts the same policy (AFT_DEPENDENT_WAITS_FOR) before it starts a
# dependent, so a mismatch fails loudly.
DEPENDENT_WAITS_FOR = "review"

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

def setup(case, native=False):
    return run(f"setup {case}{' native' if native else ''}", "Fixture: one repo, one Loom workspace and its lead (real tier: a clone of the run's sandbox repo)") + \
        run(f"lead-start {case}", "Real tier: the workspace's real codex lead is started from its agent page and its controlled runtime comes up")

def case(name, intent, steps):
    return [f"  - name: {json.dumps(name)}", f"    intent: {json.dumps(intent)}", "    steps:"] + steps

def walk():
    c = "walk"
    st = setup(c, native=True) + settings(c, "stack", "on", "off")
    st += task(c, "a") + human_create_pr(c, "a", "main") + run(f"ui {c} a open", "W1: task A shows PR A open, and keeps it after a reload")
    st += task(c, "b", "a") + human_create_pr(c, "b", "a")
    st += task(c, "c", "b") + human_create_pr(c, "c", "b") + run(f"ui {c} c open", "W2: task C shows its PR open on top of B's")
    st += human_merge(c, "c") + run(f"merge-state {c} c waiting 'merges after #A, #B'", "W3: C's Approve and merge waits for the PRs below it") + \
        run(f"hold-open {c} 6 a b c", "W3: nothing merges") + run(f"ui {c} c open 'merges after #A, #B'", "W3: task C says it merges after #A, #B")
    st += human_merge(c, "a") + run(f"merged {c} a", "W4: A merges") + \
        run(f"rebuilt {c} b main", "W4: B's PR is rebuilt on main") + run(f"rebuilt {c} c b", "W4: C's PR still sits on B's and changes only C") + \
        run(f"ui {c} a merged", "W4: task A shows its PR merged")
    st += run(f"comment {c} b 'Please rename: add a file matrix-walk-b-review.txt that says renamed by review.'", "W5: a reviewer comments on B's PR (on GitHub in the real tier); the signed webhook brings it to Loom") + \
        run(f"fixup {c} b rename", "W5: the feedback agent (real codex in the real tier) writes the fix-up in the task copy and completes it") + \
        run(f"fixup-pushed {c} b c", "W5: B's PR is updated without an Approve, and C is replayed on it") + \
        run(f"no-merge-approval {c} b", "W5: B has no pending Approve and merge") + \
        run(f"merge-state {c} c waiting", "W5: C's Approve and merge still waits after the clean replay")
    st += human_merge(c, "b") + run(f"merged {c} b", "W6: B merges") + run(f"merged {c} c", "W6: C merges by itself after its clean rebuild") + \
        run(f"ui {c} c merged", "W6: task C shows its PR merged")
    st += run(f"snapshot {c} stack", "Record the stack's PRs") + settings(c, "trunk", "on", "off")
    st += task(c, "d") + human_create_pr(c, "d", "main") + run(f"untouched {c} stack", "W8: the old stack's PRs are untouched") + \
        run(f"ui {c} d open", "W8: task D shows its own PR to main")
    st += run(f"hand-push {c} d", "W7: someone pushes a commit to D's PR by hand") + human_merge(c, "d") + \
        run(f"merge-state {c} d stale_subject 'someone else pushed'", "W7: Loom refuses to merge the PR someone else pushed to") + \
        run(f"not-overwritten {c} d 8", "W7: Loom never overwrites the hand-pushed commit")
    return case("W1-W8 stacked PR walk-through", "Stacked PRs with Lead may approve on and Lead may merge off: a human builds A, B, C as one stack, merges it bottom-up through a review fix-up, switches to PR per task and meets a hand push", st)

def settings_cases():
    out = []
    c = "s1"
    st = setup(c) + settings(c, "stack", "on", "off")
    st += task(c, "a") + lead_create_pr(c, "a", "main")
    st += task(c, "b", "a") + lead_create_pr(c, "b", "a")
    st += task(c, "c", "b") + lead_create_pr(c, "c", "b")
    st += lead(c, "merge", "a", "refused", "tries Approve and merge on A and is refused") + run(f"hold-open {c} 4 a b c", "Nothing merges")
    st += human_merge(c, "a") + run(f"merged {c} a", "A merges") + run(f"rebuilt {c} b main", "B is rebuilt on main") + run(f"rebuilt {c} c b", "C stays on B") + run(f"ui {c} b open", "Task B shows its PR open")
    out += case("S1 Stacked, lead may approve on, lead may merge off", "The lead's approvals open a stacked A-B-C; the lead cannot merge; a human Approve and merge on A merges it and B, C are rebuilt", st)

    c = "s2"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += task(c, "a") + lead(c, "approve", "a", "refused", "tries to approve A and is refused with review_required; the task stays in review") + \
        run(f"no-pr {c} a 5", "No PR opens") + human_create_pr(c, "a", "main") + run(f"ui {c} a open", "Task A shows its PR")
    out += case("S2 Stacked, lead may approve off", "With Lead may approve off the lead cannot approve; a human approval opens the PR", st)

    c = "s3"
    st = setup(c) + settings(c, "stack", "on", "when_green")
    st += task(c, "a") + lead_create_pr(c, "a", "main") + run(f"merged {c} a", "The lead merges green A by itself")
    st += task(c, "b", "a", "FAIL") + lead_create_pr(c, "b", "main") + run(f"checks {c} b red", "B's required check is red")
    st += task(c, "c", "b") + lead_create_pr(c, "c", "b") + run(f"hold-open {c} 10 b c", "The lead stops at red B; C waits")
    st += run(f"comment {c} b 'The check fails: remove the line FAIL from matrix-s3-b.txt and keep the rest.'", "A reviewer asks for B's fix") + \
        run(f"fixup {c} b green", "The feedback agent removes FAIL") + run(f"fixup-pushed {c} b c", "B's PR is updated and C replayed") + \
        run(f"merged {c} b c", "The lead merges B, then C") + run(f"ui {c} c merged", "Task C shows its PR merged")
    out += case("S3 Stacked, lead may approve on, lead may merge when green", "The lead merges green PRs bottom-up by itself, stops at red B and continues after B's fix", st)

    c = "s4"
    st = setup(c) + settings(c, "stack", "off", "when_green")
    st += task(c, "a") + human_create_pr(c, "a", "main") + run(f"merged {c} a", "The lead merges green A without asking")
    st += task(c, "b", "a") + human_create_pr(c, "b", "main") + run(f"merged {c} b", "The lead merges green B")
    st += task(c, "c", "b") + human_create_pr(c, "c", "main") + run(f"merged {c} c", "The lead merges green C") + run(f"ui {c} c merged", "Task C shows its PR merged")
    out += case("S4 Stacked, lead may approve off, lead may merge when green", "A human opens each PR; the lead merges the green ones bottom-up without asking", st)

    c = "s5"
    st = setup(c) + settings(c, "trunk", "on", "off")
    st += task(c, "a") + task(c, "b") + lead_create_pr(c, "a", "main") + lead_create_pr(c, "b", "main")
    st += task(c, "c", "a") + lead(c, "approve", "c", "ok", "approves C, which depends on A") + \
        run(f"no-pr {c} c 8", "C gets no PR while A is unmerged")
    st += human_merge(c, "a") + run(f"merged {c} a", "A lands") + run(f"pr {c} c main", "C now gets its own PR to main") + \
        run(f"rebuilt {c} c main", "C's PR changes only C") + run(f"ui {c} b open", "Task B shows its own PR")
    out += case("S5 PR per task, lead may approve on, lead may merge off", "Independent A and B get their own PRs to main; C, which depends on A, gets its PR once A lands", st)

    c = "s6"
    st = setup(c) + settings(c, "trunk", "on", "when_green")
    st += task(c, "a") + task(c, "b", "-", "FAIL") + lead_create_pr(c, "a", "main") + run(f"merged {c} a", "A merges by itself")
    st += lead_create_pr(c, "b", "main") + run(f"checks {c} b red", "B is red")
    st += task(c, "c", "a") + lead_create_pr(c, "c", "main") + run(f"merged {c} c", "C merges by itself") + \
        run(f"hold-open {c} 6 b", "Red B stays open and blocks nobody") + run(f"ui {c} b open", "Task B shows its PR open")
    out += case("S6 PR per task, lead may approve on, lead may merge when green", "Green PRs merge by themselves; red B stays open and never blocks A or C", st)

    c = "s7"
    st = setup(c) + settings(c, "trunk", "off", "off")
    st += task(c, "a") + human_create_pr(c, "a", "main") + human_merge(c, "a") + run(f"merged {c} a", "A merges") + run(f"ui {c} a merged", "Task A shows its PR merged")
    out += case("S7 PR per task, human does everything", "A human approve opens the PR to main and a human Approve and merge merges it", st)

    c = "s8"
    st = setup(c) + settings(c, "trunk", "off", "when_green")
    st += task(c, "a") + task(c, "b") + human_create_pr(c, "a", "main") + human_create_pr(c, "b", "main") + \
        run(f"merged {c} a b", "The lead merges both green PRs without asking") + run(f"ui {c} b merged", "Task B shows its PR merged")
    out += case("S8 PR per task, lead may approve off, lead may merge when green", "A human opens the PRs; the lead merges the green ones without stack order", st)

    c = "s9"
    st = setup(c) + settings(c, "stack", "on", "off")
    st += task(c, "a") + human_create_pr(c, "a", "main") + task(c, "b", "a") + human_create_pr(c, "b", "a")
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

def variants():
    out = []
    c = "l1"
    st = setup(c) + run(f"lead-epic-midsession {c}", "Real tier: an epic assigned to the ALREADY RUNNING lead is delivered to it mid-session (fake tier: no running lead, skipped)")
    out += case("L1 Mid-session epic assignment to a running lead", "The seam live-interactive ll-lead-assignment leaves open: assignment delivered into a lead that is already running", st)

    c = "n1"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += task(c, "a") + lead(c, "cli-approve", "a", "refused", "runs loom git approve on A with Lead may approve off; no verdict is recorded and A stays in review") + \
        run(f"no-pr {c} a 5", "No PR opens") + human_create_pr(c, "a", "main")
    out += case("N1 The lead's CLI approve cannot bypass Lead may approve off", "loom git approve run by the lead must not record a human approval", st)

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
    st += task(c, "a") + lead_create_pr(c, "a", "main") + task(c, "b", "a") + lead_create_pr(c, "b", "a")
    st += run(f"lead-request-merge {c} b", "The lead asks a human to merge the stack through B; the request is pending") + \
        run(f"hold-open {c} 6 a b", "Nothing merges before a human confirms") + \
        run(f"confirm-merge-request {c}", "A human confirms the lead's request (API: the UI has no confirm control)") + \
        run(f"merged {c} a b", "A and B merge") + run(f"ui {c} b merged", "Task B shows its PR merged")
    out += case("N3 The lead's merge request needs a human", "With Lead may merge off the lead can only request a merge; it runs after a human confirms", st)

    c = "r1"
    st = setup(c) + settings(c, "stack", "off", "off")
    st += task(c, "a") + run(f"open-task {c} a", "Human opens task A") + [
        "      - click:", "          testid: detail-reject-button", "        intent: Human clicks Reject on task A",
        "      - fill: { testid: detail-reject-comment, value: \"Rejected by the matrix: try again.\" }", "        intent: Human writes why",
        "      - click:", "          testid: detail-reject-submit", "        intent: Human sends the rejection",
    ] + run(f"reject {c} a", "Task A is open again, with a reject verdict and no PR") + run(f"rerun {c} a", "The epic runner runs A again") + \
        wait_rev(c, "a") + human_create_pr(c, "a", "main") + run(f"ui {c} a open", "Task A shows its PR")
    out += case("R1 Reject, rerun, approve", "A rejected task runs again and its new revision opens the PR", st)
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
    assert DEPENDENT_WAITS_FOR in ("review", "run")
    root = pathlib.Path(root)
    fake = "Fake-forge tier: runs on every make test-aft (--suite 'loomgit-matrix-*' --no-agent)."
    real = "REAL tier: real GitHub sandbox repo + real codex; only via run-aft.sh --real-github."
    for suite, what, cases, body in (("matrix-walk", ": stacked PR walk-through W1-W8", ["walk"], walk()),
                                     ("matrix-settings", ": settings cases S1-S10", [f"s{i}" for i in range(1, 11)], settings_cases()),
                                     ("matrix-variants", ": lead, negative and recovery variants L1, N1-N3, R1", ["l1", "n1", "n2", "n3", "r1"], variants())):
        emit(root / "suites" / f"loomgit-{suite}.test.yaml", f"loomgit-{suite}", what, fake, cases, body)
        emit(root / "real-github-suites" / f"real-github-{suite}.test.yaml", f"real-github-{suite}", what, real, cases, body)

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
