package agentdef

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Completion-hook flags. The same set is shared by `add` and `update`; only
// `update` gets --clear-on-complete, since creating with an empty pipeline is
// already the default.
var (
	agentAddCommentReply bool
	agentAddWriteDesign  bool
	agentAddLabels       []string
	agentAddRemoveLabels []string
	agentAddSetStatus    string
	agentAddClose        bool
	agentAddCycle        string

	agentUpdateCommentReply bool
	agentUpdateWriteDesign  bool
	agentUpdateLabels       []string
	agentUpdateRemoveLabels []string
	agentUpdateSetStatus    string
	agentUpdateClose        bool
	agentUpdateCycle        string
	agentUpdateClear        bool
)

// Identity flags on `update`. store.AgentUpdate has supported these fields all
// along; without CLI exposure, re-scoping an agent to an epic or changing its
// role meant remove + re-add (and losing its hooks with it).
var (
	agentUpdateParent string
	agentUpdateRole   string
	agentUpdateMode   string
)

var agentUpdateCmd = &cobra.Command{
	Use:   "update <NAME>",
	Short: "Update an agent's hooks, epic scope, role, or mode",
	Long: `Update an existing agent definition.

Completion hooks: the supervisor — not the agent's prompt — performs these
writes after a successful run, in order, stopping at the first failure. A
failed write demotes the run to failed so the owned task is reopened and
retried.

Identity fields: --parent scopes the agent's claims to an epic (empty string
clears the scope), --role and --mode change how the daemon runs it. Changes
apply on the daemon's next config poll; a running attempt keeps the entry it
started with.

  loom agentdef update critic --on-complete-comment-reply --on-complete-add-label criticized
  loom agentdef update critic --on-complete-remove-label needs-review --on-complete-add-label reviewed
  loom agentdef update planner --on-complete-write-design --on-complete-set-status review
  loom agentdef update planner --on-complete-set-status "blocked:upstream API decision pending"
  loom agentdef update critic --clear-on-complete
  loom agentdef update worker --parent EPIC-7
  loom agentdef update worker --parent ""`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentUpdate,
}

func registerHookFlags(
	cmd *cobra.Command,
	reply, writeDesign *bool,
	labels, removeLabels *[]string,
	setStatus *string,
	closeTask *bool,
	cycleSpec *string,
) {
	cmd.Flags().BoolVar(reply, "on-complete-comment-reply", false,
		"After a successful run, post the agent's final reply as a comment on the owned task")
	cmd.Flags().BoolVar(writeDesign, "on-complete-write-design", false,
		"After a successful run, write the agent's final reply into the owned task's design field. "+
			"Lets a read_only role produce a design at all — writing one otherwise needs a shell. "+
			"Applied before the comment and before any label, and it replaces the field rather than appending")
	cmd.Flags().StringArrayVar(labels, "on-complete-add-label", nil,
		"After a successful run, add this label to the owned task (repeat for several; applied in flag order, always after the comment)")
	cmd.Flags().StringArrayVar(removeLabels, "on-complete-remove-label", nil,
		"After a successful run, remove this label from the owned task (repeat for several; applied in flag order, after the comment and before the added labels). "+
			"CAUTION: removing a label that a FEEDING stage excludes re-arms that stage — it matches the task again, re-stamps the label, and the pipeline loops "+
			"forever (seen live as a ship/re-stamp cycle every ~32s). Check what upstream filter treats this label as its \"already handled\" marker before removing it")
	cmd.Flags().StringVar(setStatus, "on-complete-set-status", "",
		"After a successful run, move the owned task to STATUS[:REASON]. Settable: open, review, deferred, blocked "+
			"(in_progress belongs to the claim endpoint and closed to --on-complete-close, so neither is accepted). "+
			"blocked REQUIRES a reason, which is stored as the task's notes (\"BLOCKED: <reason>\") because the board's "+
			"needs-attention state is blocked-with-notes; no other status may carry one. "+
			"Applied after the added labels: only an open task is claimable, so the status is the gate that makes the "+
			"hand-off actionable and has to land last. Example: blocked:upstream API decision pending")
	cmd.Flags().StringVar(cycleSpec, "on-complete-cycle", "",
		"After a successful run, advance a bounded review loop: THRESHOLD:REARM_LABEL:SHIP_LABEL[:PREFIX]. "+
			"Below the threshold it removes REARM_LABEL to hand the task back to the previous stage and bumps a counter label; "+
			"at the threshold it stamps SHIP_LABEL instead. Example: 3:criticized:ready-to-implement")
	cmd.Flags().BoolVar(closeTask, "on-complete-close", false,
		"After a successful run, close the owned task. Always applied last, so preceding comments and labels land first. Use this instead of closing from the agent's prompt: an agent-side close makes those writes fail against a closed task and strands the hand-off")
}

// hooksFromFlags builds the pipeline in the only order the invariants permit:
// the bodies first (design, then comment), then the removals, then the added
// labels in the order the flags were given, then the status.
//
// The design goes ahead of the comment because both draw on the same extracted
// reply and only one of them is idempotent. A failure anywhere later demotes the
// run, which reopens the task and retries the WHOLE pipeline: re-running an
// overwritten design field costs nothing, while re-running a comment leaves a
// duplicate on the task forever. Put the replaceable write first and the
// append-only write last, and a retry converges.
//
// Removals sit between the bodies and the stamps because add_label is the
// CERTIFYING write — the token the next stage waits on — and everything the run
// did has to be visible before it. Stamping first would open a window in which
// the task carries both the label that routed it here and the label that hands
// it on, claimable by the upstream and downstream stages at once; removing first
// can only leave the task briefly unrouted, which stalls visibly instead of
// forking. It is the same argument the review cycle's remove-then-bump ordering
// rests on.
//
// set_status comes after the added labels, one level up from that same
// argument: in loom the status is what decides whether a task can be claimed at
// all (task_router scores anything that is not `open` as 0), so it is the gate,
// not just another token. Opening a task before its hand-off label is stamped
// would make it claimable while unrouted — the fork the removals ordering exists
// to avoid. The cycle still goes last because it manages the status itself,
// returning the task to `open` for whichever stage it hands to.
func hooksFromFlags(
	commentReply, writeDesign bool,
	labels, removeLabels []string,
	setStatus string,
	closeTask bool,
	cycleSpec string,
) (*domain.AgentHooks, error) {
	actions := make([]domain.AgentHookAction, 0, 4+len(labels)+len(removeLabels))
	actions = append(actions, bodyActions(writeDesign, commentReply)...)
	removals, err := labelActions(domain.AgentHookActionRemoveLabel, "--on-complete-remove-label", removeLabels)
	if err != nil {
		return nil, err
	}
	actions = append(actions, removals...)
	stamps, err := labelActions(domain.AgentHookActionAddLabel, "--on-complete-add-label", labels)
	if err != nil {
		return nil, err
	}
	actions = append(actions, stamps...)
	if setStatus != "" {
		status, err := parseSetStatusSpec(setStatus)
		if err != nil {
			return nil, err
		}
		actions = append(actions, status)
	}
	if cycleSpec != "" {
		cyc, err := parseCycleSpec(cycleSpec)
		if err != nil {
			return nil, err
		}
		actions = append(actions, domain.AgentHookAction{Type: domain.AgentHookActionCycle, Cycle: cyc})
	}
	// Appended last, and Validate enforces that: the close is what makes every
	// write above observable to the next stage instead of failing against a
	// task the agent already closed.
	if closeTask {
		actions = append(actions, domain.AgentHookAction{Type: domain.AgentHookActionClose})
	}
	if len(actions) == 0 {
		return nil, nil
	}
	hooks := &domain.AgentHooks{OnComplete: actions}
	if err := hooks.Validate(); err != nil {
		return nil, err
	}
	return hooks, nil
}

// bodyActions builds the run's body writes. Both name the same source, because
// there is one notion of "the run's artifact" and the supervisor resolves it
// once for both; the design leads for the retry-convergence reason argued in
// hooksFromFlags.
func bodyActions(writeDesign, commentReply bool) []domain.AgentHookAction {
	var actions []domain.AgentHookAction
	if writeDesign {
		actions = append(actions, domain.AgentHookAction{
			Type:   domain.AgentHookActionWriteDesign,
			Source: domain.AgentHookCommentSourceFinalReply,
		})
	}
	if commentReply {
		actions = append(actions, domain.AgentHookAction{
			Type:   domain.AgentHookActionComment,
			Source: domain.AgentHookCommentSourceFinalReply,
		})
	}
	return actions
}

// labelActions builds one label-carrying action per flag value. add_label and
// remove_label share it because they share a validation arm in the model: a
// value one accepts and the other refuses would be a difference no caller could
// predict, so the CLI must not invent one either.
func labelActions(t domain.AgentHookActionType, flag string, labels []string) ([]domain.AgentHookAction, error) {
	if len(labels) == 0 {
		return nil, nil
	}
	actions := make([]domain.AgentHookAction, 0, len(labels))
	for _, l := range labels {
		label := strings.TrimSpace(l)
		if label == "" {
			return nil, fmt.Errorf("%s requires a non-blank label", flag)
		}
		actions = append(actions, domain.AgentHookAction{Type: t, Value: label})
	}
	return actions, nil
}

// parseSetStatusSpec reads STATUS[:REASON]. One flag rather than two, for the
// same reason parseCycleSpec takes a positional spec: the parts are meaningless
// apart. A reason configures nothing without the status it explains, and a
// `blocked` status without one is refused outright — so two flags would let an
// operator half-configure a pair that is only ever valid together.
//
// Split on the FIRST colon only, so a reason keeps any colons of its own
// ("blocked:waiting on infra: the new cluster"). Which statuses are legal, and
// which of them may carry a reason, is not decided here: hooksFromFlags runs the
// model's Validate, which runs the server's own PATCH contract, so the CLI
// cannot accept a status fleet-db would refuse on write.
func parseSetStatusSpec(spec string) (domain.AgentHookAction, error) {
	status, reason, _ := strings.Cut(spec, ":")
	action := domain.AgentHookAction{
		Type:   domain.AgentHookActionSetStatus,
		Value:  strings.TrimSpace(status),
		Reason: strings.TrimSpace(reason),
	}
	if action.Value == "" {
		return domain.AgentHookAction{}, fmt.Errorf("--on-complete-set-status expects STATUS[:REASON], got %q", spec)
	}
	return action, nil
}

// parseCycleSpec reads THRESHOLD:REARM:SHIP[:PREFIX]. Positional rather than
// four flags because the parts are meaningless apart — a threshold without the
// labels it drives configures nothing.
func parseCycleSpec(spec string) (*domain.AgentHookCycle, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 3 || len(parts) > 4 {
		return nil, fmt.Errorf("--on-complete-cycle expects THRESHOLD:REARM_LABEL:SHIP_LABEL[:PREFIX], got %q", spec)
	}
	threshold, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return nil, fmt.Errorf("--on-complete-cycle threshold %q is not a number", parts[0])
	}
	cyc := &domain.AgentHookCycle{
		Threshold:  threshold,
		RearmLabel: strings.TrimSpace(parts[1]),
		ShipLabel:  strings.TrimSpace(parts[2]),
	}
	if len(parts) == 4 {
		cyc.Prefix = strings.TrimSpace(parts[3])
	}
	return cyc, nil
}

func runAgentUpdate(cmd *cobra.Command, args []string) error {
	patch, identityChanged, err := agentUpdateIdentityPatch(cmd.Flags().Changed)
	if err != nil {
		return err
	}
	hooks, err := agentUpdateHooksPatch(identityChanged)
	if err != nil {
		return err
	}
	patch.Hooks = hooks
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		a, err := h.Store.Agents().Update(ctx, ws, args[0], patch)
		if err != nil {
			return fmt.Errorf("update agent: %w", err)
		}
		if identityChanged {
			parent := a.Parent
			if parent == "" {
				parent = "(none)"
			}
			fmt.Printf("Updated agent %s/%s (role=%s mode=%s parent=%s)\n", ws, a.Name, a.RoleName, a.Mode, parent)
		}
		switch {
		case hooks == nil:
			// hooks untouched
		case hooks.IsEmpty():
			fmt.Printf("Cleared on_complete hooks for %s/%s\n", ws, a.Name)
		default:
			fmt.Printf("Updated on_complete hooks for %s/%s\n", ws, a.Name)
			printHookPipeline(a.Hooks)
		}
		return nil
	})
}

// agentUpdateIdentityPatch builds the non-hook part of the update from the
// identity flags. changed reports whether a flag was explicitly passed, so
// `--parent ""` (clear the epic scope) is distinguishable from "not given".
func agentUpdateIdentityPatch(changed func(string) bool) (store.AgentUpdate, bool, error) {
	patch := store.AgentUpdate{}
	touched := false
	if changed("parent") {
		p := strings.TrimSpace(agentUpdateParent)
		patch.Parent = &p
		touched = true
	}
	if changed("role") {
		r := strings.TrimSpace(agentUpdateRole)
		if r == "" {
			return store.AgentUpdate{}, false, fmt.Errorf("--role requires a non-empty role name")
		}
		patch.RoleName = &r
		touched = true
	}
	if changed("mode") {
		m := strings.TrimSpace(agentUpdateMode)
		if m != "" && m != string(domain.AgentModeEphemeral) && m != string(domain.AgentModeService) {
			return store.AgentUpdate{}, false, fmt.Errorf("--mode must be %q or %q (empty clears it)", domain.AgentModeEphemeral, domain.AgentModeService)
		}
		mode := domain.AgentMode(m)
		patch.Mode = &mode
		touched = true
	}
	return patch, touched, nil
}

// agentUpdateHooksPatch resolves the flags to a store patch value: a non-nil
// empty pipeline means "clear". Conflicting and no-op invocations are errors
// rather than silent successes, so a typo never looks like it took effect.
func agentUpdateHooksPatch(identityChanged bool) (*domain.AgentHooks, error) {
	setRequested := agentUpdateCommentReply || agentUpdateWriteDesign || len(agentUpdateLabels) > 0 ||
		len(agentUpdateRemoveLabels) > 0 || agentUpdateSetStatus != "" ||
		agentUpdateClose || agentUpdateCycle != ""
	switch {
	case agentUpdateClear && setRequested:
		return nil, fmt.Errorf("--clear-on-complete cannot be combined with --on-complete-comment-reply, " +
			"--on-complete-write-design, --on-complete-add-label, --on-complete-remove-label, " +
			"--on-complete-set-status, --on-complete-close or --on-complete-cycle")
	case agentUpdateClear:
		return &domain.AgentHooks{}, nil
	case !setRequested && identityChanged:
		return nil, nil // identity-only update; leave the pipeline untouched
	case !setRequested:
		return nil, fmt.Errorf("nothing to update: pass --parent/--role/--mode, --on-complete-comment-reply, " +
			"--on-complete-write-design, --on-complete-add-label, --on-complete-remove-label, " +
			"--on-complete-set-status, --on-complete-close and/or --on-complete-cycle, or --clear-on-complete")
	}
	return hooksFromFlags(agentUpdateCommentReply, agentUpdateWriteDesign, agentUpdateLabels,
		agentUpdateRemoveLabels, agentUpdateSetStatus, agentUpdateClose, agentUpdateCycle)
}

// printHookPipeline renders the ordered steps as stored, so an operator can see
// the exact execution order rather than a summary.
func printHookPipeline(h *domain.AgentHooks) {
	if h.IsEmpty() {
		return
	}
	fmt.Printf("On complete:\n")
	for i, a := range h.OnComplete {
		switch a.Type {
		case domain.AgentHookActionComment:
			fmt.Printf("  %d. comment (source=%s)\n", i+1, a.Source)
		case domain.AgentHookActionWriteDesign:
			fmt.Printf("  %d. write_design (source=%s)\n", i+1, a.Source)
		case domain.AgentHookActionSetStatus:
			// The reason is part of what the step DOES — it becomes the task's
			// notes — so it is printed, not summarized away.
			if a.Reason != "" {
				fmt.Printf("  %d. set_status %s (reason=%s)\n", i+1, a.Value, a.Reason)
			} else {
				fmt.Printf("  %d. set_status %s\n", i+1, a.Value)
			}
		case domain.AgentHookActionAddLabel:
			fmt.Printf("  %d. add_label %s\n", i+1, a.Value)
		case domain.AgentHookActionRemoveLabel:
			fmt.Printf("  %d. remove_label %s\n", i+1, a.Value)
		case domain.AgentHookActionClose:
			fmt.Printf("  %d. close\n", i+1)
		case domain.AgentHookActionCycle:
			fmt.Printf("  %d. cycle threshold=%d rearm=%s ship=%s\n",
				i+1, a.Cycle.Threshold, a.Cycle.RearmLabel, a.Cycle.ShipLabel)
		default:
			fmt.Printf("  %d. %s\n", i+1, a.Type)
		}
	}
}
