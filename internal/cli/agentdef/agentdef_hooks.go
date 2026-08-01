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
	agentAddClose        bool
	agentAddCycle        string

	agentUpdateCommentReply bool
	agentUpdateLabels       []string
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
  loom agentdef update critic --clear-on-complete
  loom agentdef update worker --parent EPIC-7
  loom agentdef update worker --parent ""`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentUpdate,
}

func registerHookFlags(cmd *cobra.Command, reply *bool, labels *[]string, closeTask *bool, cycleSpec *string) {
	cmd.Flags().BoolVar(reply, "on-complete-comment-reply", false,
		"After a successful run, post the agent's final reply as a comment on the owned task")
	cmd.Flags().StringArrayVar(labels, "on-complete-add-label", nil,
		"After a successful run, add this label to the owned task (repeat for several; applied in flag order, always after the comment)")
	cmd.Flags().StringVar(cycleSpec, "on-complete-cycle", "",
		"After a successful run, advance a bounded review loop: THRESHOLD:REARM_LABEL:SHIP_LABEL[:PREFIX]. "+
			"Below the threshold it removes REARM_LABEL to hand the task back to the previous stage and bumps a counter label; "+
			"at the threshold it stamps SHIP_LABEL instead. Example: 3:criticized:ready-to-implement")
	cmd.Flags().BoolVar(closeTask, "on-complete-close", false,
		"After a successful run, close the owned task. Always applied last, so preceding comments and labels land first. Use this instead of closing from the agent's prompt: an agent-side close makes those writes fail against a closed task and strands the hand-off")
}

// hooksFromFlags builds the pipeline in the only order the invariant permits:
// the comment first, then labels in the order the flags were given.
func hooksFromFlags(commentReply bool, labels []string, closeTask bool, cycleSpec string) (*domain.AgentHooks, error) {
	actions := make([]domain.AgentHookAction, 0, 2+len(labels))
	if commentReply {
		actions = append(actions, domain.AgentHookAction{
			Type:   domain.AgentHookActionComment,
			Source: domain.AgentHookCommentSourceFinalReply,
		})
	}
	for _, l := range labels {
		label := strings.TrimSpace(l)
		if label == "" {
			return nil, fmt.Errorf("--on-complete-add-label requires a non-blank label")
		}
		actions = append(actions, domain.AgentHookAction{
			Type:  domain.AgentHookActionAddLabel,
			Value: label,
		})
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
	setRequested := agentUpdateCommentReply || len(agentUpdateLabels) > 0 || agentUpdateClose || agentUpdateCycle != ""
	switch {
	case agentUpdateClear && setRequested:
		return nil, fmt.Errorf("--clear-on-complete cannot be combined with --on-complete-comment-reply, --on-complete-add-label, --on-complete-close or --on-complete-cycle")
	case agentUpdateClear:
		return &domain.AgentHooks{}, nil
	case !setRequested && identityChanged:
		return nil, nil // identity-only update; leave the pipeline untouched
	case !setRequested:
		return nil, fmt.Errorf("nothing to update: pass --parent/--role/--mode, --on-complete-comment-reply, --on-complete-add-label, --on-complete-close and/or --on-complete-cycle, or --clear-on-complete")
	}
	return hooksFromFlags(agentUpdateCommentReply, agentUpdateLabels, agentUpdateClose, agentUpdateCycle)
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
