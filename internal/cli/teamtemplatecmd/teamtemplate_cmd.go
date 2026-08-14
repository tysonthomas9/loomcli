// Package teamtemplatecmd registers the Team Template CLI commands.
package teamtemplatecmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
)

type withActiveWorkspaceFunc func(func(context.Context, *bootstrap.StoreHandle, string) error) error

type commandDeps struct {
	withActiveWorkspace withActiveWorkspaceFunc
	loadStateCache      func() (*bootstrap.StateCache, error)
	localMaterializer   func(store.Store) teamtemplate.LocalMaterializer
	writeJSON           func(any) error
}

func productionCommandDeps() commandDeps {
	return commandDeps{
		withActiveWorkspace: cmdstore.WithActiveWorkspace,
		loadStateCache:      bootstrap.LoadStateCache,
		localMaterializer: func(st store.Store) teamtemplate.LocalMaterializer {
			return cliAgentWorktreeMaterializer(st).Materialize
		},
		writeJSON: cmdstore.WriteJSON,
	}
}

func init() {
	cli.RegisterCommand(newTemplateCommand(productionCommandDeps()))
}

func newTemplateCommand(deps commandDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "template",
		Short:   "Manage Team Templates (built-in agent team presets)",
		GroupID: "workspace",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newTemplateListCommand(deps),
		newTemplateShowCommand(deps),
		newTemplateApplyCommand(deps),
	)
	return cmd
}

type templateListEntry struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	Revision      int    `json:"revision"`
	SchemaVersion int    `json:"schema_version"`
	AgentRoles    int    `json:"roles"`
	Agents        int    `json:"agents"`
}

func newTemplateListCommand(deps commandDeps) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available Team Templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			templates := teamtemplate.All()
			if jsonOutput {
				entries := make([]templateListEntry, 0, len(templates))
				for _, tpl := range templates {
					entries = append(entries, templateListEntry{
						ID:            tpl.ID,
						Label:         tpl.Label,
						Description:   tpl.Description,
						Revision:      tpl.Revision,
						SchemaVersion: tpl.SchemaVersion,
						AgentRoles:    len(tpl.Roles),
						Agents:        len(tpl.Agents),
					})
				}
				return deps.writeJSON(entries)
			}
			renderTemplateList(cmd.OutOrStdout(), templates)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	return cmd
}

func renderTemplateList(w io.Writer, templates []teamtemplate.TeamTemplate) {
	_, _ = fmt.Fprintf(w, "%-18s %-30s %3s %11s %7s\n", "ID", "LABEL", "REV", "AGENT ROLES", "AGENTS")
	for _, tpl := range templates {
		_, _ = fmt.Fprintf(w, "%-18s %-30s %3d %11d %7d\n",
			tpl.ID, tpl.Label, tpl.Revision, len(tpl.Roles), len(tpl.Agents))
	}
}

func newTemplateShowCommand(deps commandDeps) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:               "show <ID>",
		Short:             "Show a Team Template's agent roles and agents",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeTemplateIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tpl, ok := teamtemplate.ByID(args[0])
			if !ok {
				return fmt.Errorf("template %q not found (run 'loom template list')", args[0])
			}
			if jsonOutput {
				return deps.writeJSON(templateJSONFrom(tpl))
			}
			renderTemplate(cmd.OutOrStdout(), tpl)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	return cmd
}

func renderTemplate(w io.Writer, tpl teamtemplate.TeamTemplate) {
	_, _ = fmt.Fprintf(w, "Template:     %s\n", tpl.ID)
	_, _ = fmt.Fprintf(w, "Label:        %s\n", tpl.Label)
	_, _ = fmt.Fprintf(w, "Revision:     %d (schema %d)\n", tpl.Revision, tpl.SchemaVersion)
	_, _ = fmt.Fprintf(w, "Description:  %s\n", tpl.Description)
	_, _ = fmt.Fprintf(w, "\nAgent roles (%d):\n", len(tpl.Roles))
	_, _ = fmt.Fprintf(w, "  %-16s %-12s %-13s %-29s %-12s %s\n",
		"NAME", "KIND", "LABEL", "PROMPT", "TASK FILTER", "LABELS/EXCLUDE")
	for _, role := range tpl.Roles {
		_, _ = fmt.Fprintf(w, "  %-16s %-12s %-13s %-29s %-12s %s\n",
			role.Name,
			valueOrDash(role.Kind),
			valueOrDash(role.DisplayLabel),
			valueOrDash(role.PromptFile),
			valueOrDash(role.TaskFilter),
			routingLabels(role.Labels, role.ExcludeLabels),
		)
	}
	_, _ = fmt.Fprintf(w, "\nAgents (%d):\n", len(tpl.Agents))
	_, _ = fmt.Fprintf(w, "  %-18s %-16s %-6s %s\n", "NAME", "AGENT ROLE", "AUTO", "DESIRED")
	for _, agent := range tpl.Agents {
		_, _ = fmt.Fprintf(w, "  %-18s %-16s %-6t %s\n",
			agent.Name, agent.RoleName, agent.Auto, valueOrDash(agent.DesiredState))
	}
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func routingLabels(labels, excludes []string) string {
	values := append([]string(nil), labels...)
	for _, label := range excludes {
		values = append(values, "-"+label)
	}
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ",")
}

type applyOptions struct {
	dryRun bool
	json   bool
	strict bool
}

func newTemplateApplyCommand(deps commandDeps) *cobra.Command {
	var opts applyOptions
	cmd := &cobra.Command{
		Use:   "apply <ID>",
		Short: "Apply a Team Template to the active workspace",
		Long: `Creates the template's agent roles, then its agents, in the active workspace
(set LOOM_WORKSPACE or pass --workspace). Existing agent roles and agents with
the same name are never modified: they are skipped and reported, so apply is
safe to re-run. A failed step is retried simply by running apply again, and
re-running also re-checks each agent's local worktrees.

Worker agents are created with desired state "running", which is an intent: a
running daemon adopts them on its next config poll. Local git worktrees are
materialized when the workspace has a local checkout; a workspace with no local
checkout gets control-plane rows only, and apply says so.`,
		Example: `  loom template apply fullstack-app
  loom template apply fullstack-app --dry-run
  loom template apply fullstack-app --dry-run --strict
  LOOM_WORKSPACE=MYPROJ loom template apply backend --json
  loom --workspace MYPROJ template apply website`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeTemplateIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTemplateApply(cmd, deps, args[0], opts)
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print what would be created but don't actually create agent roles or agents")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Print the apply report as JSON (suppresses per-step lines)")
	cmd.Flags().BoolVar(&opts.strict, "strict", false, "Exit 1 if any entry exists with different settings (diverged)")
	return cmd
}

func runTemplateApply(cmd *cobra.Command, deps commandDeps, id string, opts applyOptions) error {
	tpl, ok := teamtemplate.ByID(id)
	if !ok {
		return fmt.Errorf("template %q not found (run 'loom template list')", id)
	}
	return deps.withActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, workspaceKey string) error {
		state, err := deps.loadStateCache()
		if err != nil {
			return fmt.Errorf("load local workspace state: %w", err)
		}
		local := state.Workspaces[workspaceKey]
		repoCount := workspaceRepoCount(ctx, h.Store, workspaceKey, len(local.Repos))

		var materialize teamtemplate.LocalMaterializer
		if local.Path != "" {
			materialize = deps.localMaterializer(h.Store)
		}

		var stepOutput strings.Builder
		var onStep func(teamtemplate.StepResult)
		if !opts.json {
			onStep = func(step teamtemplate.StepResult) {
				renderApplyStep(&stepOutput, workspaceKey, tpl, step, opts.dryRun, materialize != nil)
			}
		}

		report, applyErr := teamtemplate.Apply(ctx, teamtemplate.ApplyDeps{
			Store:              h.Store,
			LocalMaterializer:  materialize,
			OnStep:             onStep,
			DryRun:             opts.dryRun,
			LocalPath:          local.Path,
			RunnableAgentCount: runnableAgentCount(ctx, h.Store, workspaceKey),
			MaxAgents:          daemonMaxAgents(ctx, h.Store, workspaceKey),
		}, workspaceKey, tpl)
		if opts.json {
			if err := deps.writeJSON(report); err != nil {
				return err
			}
		} else if applyErr == nil {
			renderApplyReport(cmd.OutOrStdout(), tpl, report, local, repoCount, stepOutput.String())
		}
		if applyErr != nil {
			return applyErr
		}
		return applyResultError(cmd, report, opts.strict)
	})
}

func renderApplyStep(w io.Writer, workspaceKey string, tpl teamtemplate.TeamTemplate, step teamtemplate.StepResult, dryRun, hasMaterializer bool) {
	entity := "agent"
	if step.Entity == "role" {
		entity = "agent role"
	}
	verb := ""
	suffix := ""
	switch step.Action {
	case teamtemplate.StepCreated:
		if dryRun {
			verb = "Would create"
		} else {
			verb = "Created"
		}
		if step.Entity == "agent" {
			suffix = fmt.Sprintf(" (agent role=%s)", templateAgentRole(tpl, step.Name))
		}
	case teamtemplate.StepSkippedMatch:
		if dryRun {
			verb = "Skip"
		} else {
			verb = "Skipped"
		}
		suffix = " (already applied)"
		if step.Entity == "agent" && !dryRun && hasMaterializer {
			suffix = " (already applied; worktrees re-checked)"
		}
	case teamtemplate.StepSkippedDiverged:
		if dryRun {
			verb = "Skip"
		} else {
			verb = "Skipped"
		}
		suffix = fmt.Sprintf(" (diverged: %s)", strings.Join(step.Fields, ", "))
	case teamtemplate.StepFailed:
		verb = "Failed"
		suffix = ": " + step.Error
	default:
		verb = string(step.Action)
	}
	_, _ = fmt.Fprintf(w, "  %s %s %s/%s%s\n", verb, entity, workspaceKey, step.Name, suffix)
}

func templateAgentRole(tpl teamtemplate.TeamTemplate, name string) string {
	for _, agent := range tpl.Agents {
		if agent.Name == name {
			return agent.RoleName
		}
	}
	return "-"
}

func renderApplyReport(w io.Writer, tpl teamtemplate.TeamTemplate, report teamtemplate.ApplyReport, local bootstrap.WorkspaceLocalState, repoCount int, stepOutput string) {
	if report.DryRun {
		_, _ = fmt.Fprintf(w, "Plan for Team Template %q (%s rev %d) on workspace %s\n",
			tpl.Label, tpl.ID, tpl.Revision, report.WorkspaceKey)
	} else {
		_, _ = fmt.Fprintf(w, "Applying Team Template %q (%s rev %d) to workspace %s\n",
			tpl.Label, tpl.ID, tpl.Revision, report.WorkspaceKey)
	}
	for _, warning := range report.Warnings {
		_, _ = fmt.Fprintf(w, "Warning: %s\n", warning)
	}
	_, _ = io.WriteString(w, stepOutput)

	matched := report.Skipped - report.Diverged
	if report.DryRun {
		_, _ = fmt.Fprintf(w, "Plan: %d to create, %d skipped", report.Created, report.Skipped)
		if report.Skipped > 0 {
			renderSkipCounts(w, matched, report.Diverged)
		}
		_, _ = fmt.Fprintln(w, ". No changes made.")
		return
	}

	_, _ = fmt.Fprintf(w, "Applied %s to %s: %d created, %d skipped",
		tpl.ID, report.WorkspaceKey, report.Created, report.Skipped)
	if report.Skipped > 0 {
		renderSkipCounts(w, matched, report.Diverged)
	}
	_, _ = fmt.Fprintf(w, ", %d failed\n", report.Failed)

	switch {
	case report.Failed > 0:
		_, _ = fmt.Fprintf(w, "Created entries were kept. Re-run 'loom template apply %s' to retry; existing entries are skipped, and only failed steps are created.\n", tpl.ID)
	case report.Diverged > 0:
		_, _ = fmt.Fprintln(w, "Note: diverged entries were left untouched (apply never overwrites).")
	case report.Created == 0:
		_, _ = fmt.Fprintln(w, "Workspace already matches this template.")
	case local.Path == "":
		_, _ = fmt.Fprintf(w, "%d agents configured to run (0 worktrees; no local checkout). See warnings above.\n", len(tpl.Agents))
	default:
		worktrees := report.Materialized * repoCount
		_, _ = fmt.Fprintf(w, "%d agents configured to run (%d worktrees). A running daemon adopts them on its next poll.\n",
			len(tpl.Agents), worktrees)
	}
}

func workspaceRepoCount(ctx context.Context, st store.Store, workspaceKey string, fallback int) int {
	repos, err := st.Repos().List(ctx, workspaceKey)
	if err != nil {
		return fallback
	}
	count := 0
	for _, repo := range repos {
		if repo != nil {
			count++
		}
	}
	return count
}

func renderSkipCounts(w io.Writer, matched, diverged int) {
	if diverged == 0 {
		_, _ = fmt.Fprintf(w, " (%d match)", matched)
		return
	}
	_, _ = fmt.Fprintf(w, " (%d match, %d diverged)", matched, diverged)
}

func applyResultError(cmd *cobra.Command, report teamtemplate.ApplyReport, strict bool) error {
	total := len(report.Steps)
	if report.Failed > 0 {
		silenceResultError(cmd)
		return fmt.Errorf("template apply: %d of %d steps failed", report.Failed, total)
	}
	if strict && report.Diverged > 0 {
		silenceResultError(cmd)
		return fmt.Errorf("template apply: %d of %d entries differ from the template (--strict)", report.Diverged, total)
	}
	return nil
}

// silenceResultError marks the returned error as a result, not a usage
// mistake: the report already explained it, so cobra must not re-print the
// error or dump usage.
func silenceResultError(cmd *cobra.Command) {
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
}

func daemonMaxAgents(ctx context.Context, st store.Store, workspaceKey string) int {
	maxAgents := teamtemplate.DefaultMaxAgents
	profile, err := st.Daemon().Get(ctx, workspaceKey)
	if err == nil && profile != nil && profile.MaxAgents != nil {
		maxAgents = *profile.MaxAgents
	}
	return maxAgents
}

func runnableAgentCount(ctx context.Context, st store.Store, workspaceKey string) int {
	agents, err := st.Agents().List(ctx, workspaceKey)
	if err != nil {
		return 0
	}
	roles, rolesErr := st.Roles().List(ctx, workspaceKey)
	roleByName := make(map[string]*domain.Role, len(roles))
	if rolesErr == nil {
		for _, role := range roles {
			if role != nil {
				roleByName[role.Name] = role
			}
		}
	}
	runnable := 0
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		switch agent.DesiredState {
		case domain.AgentDesiredStopped, domain.AgentDesiredDraining:
			continue
		}
		if domain.ResolveRoleKind(roleByName[agent.RoleName], agent.RoleName) == domain.RoleKindInteractive {
			continue
		}
		runnable++
	}
	return runnable
}

// cliAgentWorktreeMaterializer binds the CLI's local state and store-backed
// repository list to the shared materializer. Apply owns the interactive-agent
// decision, so SkipAgent is deliberately unset.
func cliAgentWorktreeMaterializer(st store.Store) localworkspace.AgentWorktreeMaterializer {
	return localworkspace.AgentWorktreeMaterializer{
		ResolveWorkspace: func(ctx context.Context, workspaceKey string) (localworkspace.LocalWorkspaceView, error) {
			state, err := bootstrap.LoadStateCache()
			if err != nil {
				return localworkspace.LocalWorkspaceView{}, fmt.Errorf("load local workspace state: %w", err)
			}
			local := state.Workspaces[workspaceKey]
			if local.Path == "" {
				return localworkspace.LocalWorkspaceView{}, nil
			}
			repos, err := st.Repos().List(ctx, workspaceKey)
			if err != nil {
				return localworkspace.LocalWorkspaceView{}, fmt.Errorf("list workspace repos: %w", err)
			}
			localRepos := make([]localworkspace.Repo, 0, len(repos))
			for _, repo := range repos {
				if repo == nil {
					continue
				}
				localRepos = append(localRepos, localworkspace.Repo{
					Name:   repo.Name,
					Path:   localworkspace.RepoPath(local, repo.Name),
					Groups: append([]string(nil), repo.Groups...),
				})
			}
			return localworkspace.LocalWorkspaceView{Root: local.Path, Repos: localRepos}, nil
		},
	}
}

func completeTemplateIDs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var ids []string
	for _, tpl := range teamtemplate.All() {
		if strings.HasPrefix(tpl.ID, toComplete) {
			ids = append(ids, tpl.ID)
		}
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

type templateJSON struct {
	SchemaVersion int                 `json:"schema_version"`
	ID            string              `json:"id"`
	Label         string              `json:"label"`
	Description   string              `json:"description"`
	Revision      int                 `json:"revision"`
	Roles         []templateRoleJSON  `json:"roles"`
	Agents        []templateAgentJSON `json:"agents"`
}

type templateRoleJSON struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Description    string   `json:"description"`
	PromptFile     string   `json:"prompt_file"`
	Model          string   `json:"model"`
	TaskFilter     string   `json:"task_filter"`
	Effort         string   `json:"effort"`
	Skills         []string `json:"skills"`
	Labels         []string `json:"labels"`
	ExcludeLabels  []string `json:"exclude_labels"`
	ReadOnly       bool     `json:"read_only"`
	AllowedTools   []string `json:"allowed_tools"`
	DeniedTools    []string `json:"denied_tools"`
	MaxConcurrency *int     `json:"max_concurrency"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd"`
	MaxRunDuration *int     `json:"max_run_duration"`
	DisplayLabel   string   `json:"display_label"`
}

type templateAgentJSON struct {
	Name         string `json:"name"`
	RoleName     string `json:"role_name"`
	Auto         bool   `json:"auto"`
	DesiredState string `json:"desired_state"`
	CrossRepo    bool   `json:"cross_repo"`
}

func templateJSONFrom(tpl teamtemplate.TeamTemplate) templateJSON {
	out := templateJSON{
		SchemaVersion: tpl.SchemaVersion,
		ID:            tpl.ID,
		Label:         tpl.Label,
		Description:   tpl.Description,
		Revision:      tpl.Revision,
		Roles:         make([]templateRoleJSON, 0, len(tpl.Roles)),
		Agents:        make([]templateAgentJSON, 0, len(tpl.Agents)),
	}
	for _, role := range tpl.Roles {
		out.Roles = append(out.Roles, templateRoleJSON{
			Name:           role.Name,
			Kind:           role.Kind,
			Description:    role.Description,
			PromptFile:     role.PromptFile,
			Model:          role.Model,
			TaskFilter:     role.TaskFilter,
			Effort:         role.Effort,
			Skills:         role.Skills,
			Labels:         role.Labels,
			ExcludeLabels:  role.ExcludeLabels,
			ReadOnly:       role.ReadOnly,
			AllowedTools:   role.AllowedTools,
			DeniedTools:    role.DeniedTools,
			MaxConcurrency: role.MaxConcurrency,
			MaxBudgetUSD:   role.MaxBudgetUSD,
			MaxRunDuration: role.MaxRunDuration,
			DisplayLabel:   role.DisplayLabel,
		})
	}
	for _, agent := range tpl.Agents {
		out.Agents = append(out.Agents, templateAgentJSON{
			Name:         agent.Name,
			RoleName:     agent.RoleName,
			Auto:         agent.Auto,
			DesiredState: agent.DesiredState,
			CrossRepo:    agent.CrossRepo,
		})
	}
	return out
}
