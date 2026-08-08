package agentdef

import (
	"context"
	"fmt"
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

	agentUpdateCommentReply bool
	agentUpdateLabels       []string
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
  loom agentdef update critic --clear-on-complete`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentUpdate,
}

func registerHookFlags(cmd *cobra.Command, reply *bool, labels *[]string) {
	cmd.Flags().BoolVar(reply, "on-complete-comment-reply", false,
		"After a successful run, post the agent's final reply as a comment on the owned task")
	cmd.Flags().StringArrayVar(labels, "on-complete-add-label", nil,
		"After a successful run, add this label to the owned task (repeat for several; applied in flag order, always after the comment)")
}

// hooksFromFlags builds the pipeline in the only order the invariant permits:
// the comment first, then labels in the order the flags were given.
func hooksFromFlags(commentReply bool, labels []string) (*domain.AgentHooks, error) {
	actions := make([]domain.AgentHookAction, 0, 1+len(labels))
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
	if len(actions) == 0 {
		return nil, nil
	}
	hooks := &domain.AgentHooks{OnComplete: actions}
	if err := hooks.Validate(); err != nil {
		return nil, err
	}
	return hooks, nil
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
	setRequested := agentUpdateCommentReply || len(agentUpdateLabels) > 0
	switch {
	case agentUpdateClear && setRequested:
		return nil, fmt.Errorf("--clear-on-complete cannot be combined with --on-complete-comment-reply or --on-complete-add-label")
	case agentUpdateClear:
		return &domain.AgentHooks{}, nil
	case !setRequested:
		return nil, fmt.Errorf("nothing to update: pass --on-complete-comment-reply and/or --on-complete-add-label, or --clear-on-complete")
	}
	return hooksFromFlags(agentUpdateCommentReply, agentUpdateLabels)
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
		default:
			fmt.Printf("  %d. %s\n", i+1, a.Type)
		}
	}
}
