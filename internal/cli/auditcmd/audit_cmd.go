// Package auditcmd registers the user-facing mutation audit command.
package auditcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const defaultAuditLimit = 100

type withActiveWorkspaceFunc func(func(context.Context, *bootstrap.StoreHandle, string) error) error

type commandDeps struct {
	withActiveWorkspace withActiveWorkspaceFunc
}

func productionCommandDeps() commandDeps {
	return commandDeps{withActiveWorkspace: cmdstore.WithActiveWorkspace}
}

func init() {
	cli.RegisterCommand(newAuditCommand(productionCommandDeps()))
}

type auditOptions struct {
	since  string
	limit  int
	entity string
	actor  string
	follow bool
	output string
}

func newAuditCommand(deps commandDeps) *cobra.Command {
	opts := auditOptions{limit: defaultAuditLimit}
	cmd := &cobra.Command{
		Use:     "audit",
		Short:   "Read the workspace mutation audit trail",
		GroupID: "workspace",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := runAudit(cmd, deps, opts)
			if err != nil {
				silenceResultError(cmd)
			}
			return err
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.since, "since", "", "Read events strictly after this stream cursor")
	flags.IntVar(&opts.limit, "limit", defaultAuditLimit, "Maximum historical events to print")
	flags.StringVar(&opts.entity, "entity", "", "Only events whose entity ID matches exactly")
	flags.StringVar(&opts.actor, "actor", "", "Only events whose actor matches exactly")
	flags.BoolVar(&opts.follow, "follow", false, "Keep printing new events until interrupted")
	flags.StringVar(&opts.output, "output", "", "Output format (json for newline-delimited raw events)")
	return cmd
}

func runAudit(cmd *cobra.Command, deps commandDeps, opts auditOptions) error {
	if deps.withActiveWorkspace == nil {
		return fmt.Errorf("audit store unavailable")
	}
	if opts.limit < 1 || opts.limit > 1000 {
		return fmt.Errorf("--limit must be between 1 and 1000")
	}
	if opts.output != "" && opts.output != "json" {
		return fmt.Errorf("--output must be json")
	}
	filter := store.AuditEventFilter{
		EntityID: strings.TrimSpace(opts.entity),
		Actor:    strings.TrimSpace(opts.actor),
	}
	return deps.withActiveWorkspace(func(ctx context.Context, handle *bootstrap.StoreHandle, workspaceKey string) error {
		if handle == nil || handle.Store == nil {
			return fmt.Errorf("audit store unavailable")
		}
		reader, ok := handle.Store.TriggerEvents().(store.AuditJournalReader)
		if !ok {
			return fmt.Errorf("audit trail is unavailable for this store")
		}
		events, cursor, _, err := reader.ListAuditEvents(ctx, workspaceKey, strings.TrimSpace(opts.since), opts.limit, filter)
		if err != nil {
			return fmt.Errorf("read audit history: %w", err)
		}
		for _, event := range events {
			if !auditEventMatches(event, filter) {
				continue
			}
			if err := writeAuditEvent(cmd.OutOrStdout(), event, opts.output); err != nil {
				return err
			}
		}
		if !opts.follow {
			return nil
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Following audit events for workspace %s...\n", workspaceKey)
		live, streamErrs := reader.SubscribeAuditEvents(ctx, workspaceKey, cursor, filter)
		return consumeAuditStream(ctx, cmd.OutOrStdout(), live, streamErrs, filter, opts.output)
	})
}

func consumeAuditStream(
	ctx context.Context,
	w io.Writer,
	events <-chan store.AuditEvent,
	errs <-chan error,
	filter store.AuditEventFilter,
	output string,
) error {
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if !auditEventMatches(event, filter) {
				continue
			}
			if err := writeAuditEvent(w, event, output); err != nil {
				return err
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return fmt.Errorf("follow audit events: %w", err)
			}
		}
	}
	return nil
}

func auditEventMatches(event store.AuditEvent, filter store.AuditEventFilter) bool {
	if filter.EntityID != "" && event.EntityID != filter.EntityID {
		return false
	}
	if filter.Actor != "" && event.Actor != filter.Actor {
		return false
	}
	return true
}

func writeAuditEvent(w io.Writer, event store.AuditEvent, output string) error {
	if output == "json" {
		if err := json.NewEncoder(w).Encode(event); err != nil {
			return fmt.Errorf("encode audit event: %w", err)
		}
		return nil
	}
	_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
		event.Timestamp.UTC().Format("2006-01-02T15:04:05Z07:00"),
		event.Actor,
		event.Action,
		event.EntityID,
		auditPhrase(event),
	)
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

// auditPhrase maps every action currently declared by fleet-db's event model.
// Unknown actions remain visible as their raw action string.
func auditPhrase(event store.AuditEvent) string { //nolint:cyclop,funlen // Keep the fleet-db action registry exhaustive and reviewable in one place.
	actor, entity := event.Actor, event.EntityID
	switch event.Action {
	case "issue.create":
		return fmt.Sprintf("%s created %s", actor, entity)
	case "issue.update":
		return fmt.Sprintf("%s updated %s", actor, entity)
	case "issue.close":
		if reason := event.Metadata["reason"]; reason != "" {
			return fmt.Sprintf("%s closed %s: %s", actor, entity, reason)
		}
		return fmt.Sprintf("%s closed %s", actor, entity)
	case "issue.reopen":
		return fmt.Sprintf("%s reopened %s", actor, entity)
	case "issue.delete":
		return fmt.Sprintf("%s deleted %s", actor, entity)
	case "issue.claim":
		return fmt.Sprintf("%s claimed %s", actor, entity)
	case "issue.release":
		return fmt.Sprintf("%s released %s", actor, entity)
	case "issue.assign":
		if assignee := event.Metadata["assignee"]; assignee != "" {
			return fmt.Sprintf("%s assigned %s to %s", actor, entity, assignee)
		}
		return fmt.Sprintf("%s assigned %s", actor, entity)
	case "issue.defer":
		if until := event.Metadata["defer_until"]; until != "" {
			return fmt.Sprintf("%s deferred %s until %s", actor, entity, until)
		}
		return fmt.Sprintf("%s deferred %s", actor, entity)
	case "issue.undefer":
		return fmt.Sprintf("%s undeferred %s", actor, entity)
	case "dep.add":
		return dependencyPhrase(actor, entity, event.Metadata["depends_on_id"], "added")
	case "dep.remove":
		return dependencyPhrase(actor, entity, event.Metadata["depends_on_id"], "removed")
	case "label.add":
		return namedValuePhrase("label", event.Metadata["label"], "added", actor, entity)
	case "label.remove":
		return namedValuePhrase("label", event.Metadata["label"], "removed", actor, entity)
	case "metadata.set":
		return namedValuePhrase("metadata", event.Metadata["meta_key"], "set", actor, entity)
	case "metadata.remove":
		return namedValuePhrase("metadata", event.Metadata["meta_key"], "removed", actor, entity)
	case "comment.add":
		return fmt.Sprintf("%s commented on %s", actor, entity)
	case "workspace.create":
		return fmt.Sprintf("%s created workspace %s", actor, entity)
	case "workspace.update":
		return fmt.Sprintf("%s updated workspace %s", actor, entity)
	case "workspace.delete":
		return fmt.Sprintf("%s deleted workspace %s", actor, entity)
	case "repo.create":
		return fmt.Sprintf("%s created repo %s", actor, entity)
	case "repo.update":
		return fmt.Sprintf("%s updated repo %s", actor, entity)
	case "repo.delete":
		return fmt.Sprintf("%s deleted repo %s", actor, entity)
	case "agent.create":
		return fmt.Sprintf("%s created agent %s", actor, entity)
	case "agent.update":
		return fmt.Sprintf("%s updated agent %s", actor, entity)
	case "agent.delete":
		return fmt.Sprintf("%s deleted agent %s", actor, entity)
	case "driver_run.create":
		return fmt.Sprintf("%s created driver run %s", actor, entity)
	case "driver_run.claim":
		return fmt.Sprintf("%s claimed driver run %s", actor, entity)
	case "driver_run.heartbeat":
		return fmt.Sprintf("%s heartbeated driver run %s", actor, entity)
	case "driver_run.finish":
		return fmt.Sprintf("%s finished driver run %s", actor, entity)
	case "driver_run.recover":
		return fmt.Sprintf("%s recovered driver run %s", actor, entity)
	case "driver_run.suspend":
		return fmt.Sprintf("%s suspended driver run %s", actor, entity)
	case "driver_run.resume":
		return fmt.Sprintf("%s resumed driver run %s", actor, entity)
	case "role.create":
		return fmt.Sprintf("%s created agent role %s", actor, entity)
	case "role.update":
		return fmt.Sprintf("%s updated agent role %s", actor, entity)
	case "role.delete":
		return fmt.Sprintf("%s deleted agent role %s", actor, entity)
	case "daemon.update":
		return fmt.Sprintf("%s updated daemon settings for %s", actor, entity)
	default:
		return event.Action
	}
}

func dependencyPhrase(actor, entity, dependency, verb string) string {
	if dependency == "" {
		return fmt.Sprintf("dependency %s on %s by %s", verb, entity, actor)
	}
	return fmt.Sprintf("dependency %s -> %s %s by %s", entity, dependency, verb, actor)
}

func namedValuePhrase(kind, value, verb, actor, entity string) string {
	if value == "" {
		return fmt.Sprintf("%s %s on %s by %s", kind, verb, entity, actor)
	}
	return fmt.Sprintf("%s %s %s on %s by %s", kind, value, verb, entity, actor)
}

func silenceResultError(cmd *cobra.Command) {
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
}
