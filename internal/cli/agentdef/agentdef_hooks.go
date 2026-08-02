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
	agentAddLabels       []string
	agentAddRemoveLabels []string
	agentAddClose        bool
	agentAddCycle        string

	agentUpdateCommentReply bool
	agentUpdateLabels       []string
	agentUpdateRemoveLabels []string
	agentUpdateClose        bool
	agentUpdateCycle        string
	agentUpdateClear        bool
)

var agentUpdateCmd = &cobra.Command{
	Use:   "update <NAME>",
	Short: "Set or clear an agent's post-run completion hooks",
	Long: `Set or clear the supervisor-owned on_complete pipeline for an agent.

The supervisor — not the agent's prompt — performs these writes after a
successful run, in order, stopping at the first failure. A failed write demotes
the run to failed so the owned task is reopened and retried.

  loom agentdef update critic --on-complete-comment-reply --on-complete-add-label criticized
  loom agentdef update critic --on-complete-remove-label needs-review --on-complete-add-label reviewed
  loom agentdef update critic --clear-on-complete`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentUpdate,
}

func registerHookFlags(
	cmd *cobra.Command,
	reply *bool,
	labels, removeLabels *[]string,
	closeTask *bool,
	cycleSpec *string,
) {
	cmd.Flags().BoolVar(reply, "on-complete-comment-reply", false,
		"After a successful run, post the agent's final reply as a comment on the owned task")
	cmd.Flags().StringArrayVar(labels, "on-complete-add-label", nil,
		"After a successful run, add this label to the owned task (repeat for several; applied in flag order, always after the comment)")
	cmd.Flags().StringArrayVar(removeLabels, "on-complete-remove-label", nil,
		"After a successful run, remove this label from the owned task (repeat for several; applied in flag order, after the comment and before the added labels). "+
			"CAUTION: removing a label that a FEEDING stage excludes re-arms that stage — it matches the task again, re-stamps the label, and the pipeline loops "+
			"forever (seen live as a ship/re-stamp cycle every ~32s). Check what upstream filter treats this label as its \"already handled\" marker before removing it")
	cmd.Flags().StringVar(cycleSpec, "on-complete-cycle", "",
		"After a successful run, advance a bounded review loop: THRESHOLD:REARM_LABEL:SHIP_LABEL[:PREFIX]. "+
			"Below the threshold it removes REARM_LABEL to hand the task back to the previous stage and bumps a counter label; "+
			"at the threshold it stamps SHIP_LABEL instead. Example: 3:criticized:ready-to-implement")
	cmd.Flags().BoolVar(closeTask, "on-complete-close", false,
		"After a successful run, close the owned task. Always applied last, so preceding comments and labels land first. Use this instead of closing from the agent's prompt: an agent-side close makes those writes fail against a closed task and strands the hand-off")
}

// hooksFromFlags builds the pipeline in the only order the invariants permit:
// the comment first, then the removals, then the added labels in the order the
// flags were given.
//
// Removals sit between the two because add_label is the CERTIFYING write — the
// token the next stage waits on — and everything the run did has to be visible
// before it. Stamping first would open a window in which the task carries both
// the label that routed it here and the label that hands it on, claimable by
// the upstream and downstream stages at once; removing first can only leave the
// task briefly unrouted, which stalls visibly instead of forking. It is the
// same argument the review cycle's remove-then-bump ordering rests on.
func hooksFromFlags(
	commentReply bool,
	labels, removeLabels []string,
	closeTask bool,
	cycleSpec string,
) (*domain.AgentHooks, error) {
	actions := make([]domain.AgentHookAction, 0, 2+len(labels)+len(removeLabels))
	if commentReply {
		actions = append(actions, domain.AgentHookAction{
			Type:   domain.AgentHookActionComment,
			Source: domain.AgentHookCommentSourceFinalReply,
		})
	}
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

func runAgentUpdate(_ *cobra.Command, args []string) error {
	hooks, err := agentUpdateHooksPatch()
	if err != nil {
		return err
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		a, err := h.Store.Agents().Update(ctx, ws, args[0], store.AgentUpdate{Hooks: hooks})
		if err != nil {
			return fmt.Errorf("update agent: %w", err)
		}
		if hooks.IsEmpty() {
			fmt.Printf("Cleared on_complete hooks for %s/%s\n", ws, a.Name)
			return nil
		}
		fmt.Printf("Updated on_complete hooks for %s/%s\n", ws, a.Name)
		printHookPipeline(a.Hooks)
		return nil
	})
}

// agentUpdateHooksPatch resolves the flags to a store patch value: a non-nil
// empty pipeline means "clear". Conflicting and no-op invocations are errors
// rather than silent successes, so a typo never looks like it took effect.
func agentUpdateHooksPatch() (*domain.AgentHooks, error) {
	setRequested := agentUpdateCommentReply || len(agentUpdateLabels) > 0 ||
		len(agentUpdateRemoveLabels) > 0 || agentUpdateClose || agentUpdateCycle != ""
	switch {
	case agentUpdateClear && setRequested:
		return nil, fmt.Errorf("--clear-on-complete cannot be combined with --on-complete-comment-reply, " +
			"--on-complete-add-label, --on-complete-remove-label, --on-complete-close or --on-complete-cycle")
	case agentUpdateClear:
		return &domain.AgentHooks{}, nil
	case !setRequested:
		return nil, fmt.Errorf("nothing to update: pass --on-complete-comment-reply, --on-complete-add-label, " +
			"--on-complete-remove-label, --on-complete-close and/or --on-complete-cycle, or --clear-on-complete")
	}
	return hooksFromFlags(agentUpdateCommentReply, agentUpdateLabels, agentUpdateRemoveLabels,
		agentUpdateClose, agentUpdateCycle)
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
