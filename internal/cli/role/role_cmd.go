// Package role registers the `loom role` noun-verb commands for
// fleet-db-backed Role CRUD within the active workspace.
package role

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	roleAddDescription string
	roleAddKind        string
	roleAddPrompt      string
	roleAddPromptFile  string
	roleAddModel       string
	roleAddBackend     string
	roleAddEffort      string
	roleAddSkills      []string
	roleAddMaxConc     int
	roleAddReadOnly    bool

	roleListJSON bool
	roleShowJSON bool
)

var roleCmd = &cobra.Command{
	Use:     "role",
	Short:   "Manage roles within the active workspace",
	GroupID: "workspace",
}

var roleAddCmd = &cobra.Command{
	Use:   "add <NAME>",
	Short: "Create a role definition in the active workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runRoleAdd,
}

var roleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List roles in the active workspace",
	Args:  cobra.NoArgs,
	RunE:  runRoleList,
}

var roleShowCmd = &cobra.Command{
	Use:   "show <NAME>",
	Short: "Show role details",
	Args:  cobra.ExactArgs(1),
	RunE:  runRoleShow,
}

var roleRemoveCmd = &cobra.Command{
	Use:   "remove <NAME>",
	Short: "Delete a role from the active workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runRoleRemove,
}

var roleSetCmd = &cobra.Command{
	Use:   "set <NAME> <KEY> <VALUE>",
	Short: "Set a single field on an existing role",
	Long: `Set a role field by key. Supported keys:
  description     string
  kind            string (interactive/worker)
  prompt          inline prompt text
  prompt_file     string
  model           string
  task_filter     string
  backend         string
  effort          string (low/medium/high/xhigh/max)
  read_only       bool ("true"/"false")
  max_priority    integer
  max_concurrency integer
  max_budget_usd  float
  skills          comma-separated list
  path_patterns   comma-separated list
  allowed_tools   comma-separated list
  denied_tools    comma-separated list`,
	Args: cobra.ExactArgs(3),
	RunE: runRoleSet,
}

var roleUnsetCmd = &cobra.Command{
	Use:   "unset <NAME> <KEY>",
	Short: "Clear a clearable role field back to its default",
	Long: `Revert a role field to nil/empty. Supported keys:
  max_priority    *int     (clear)
  max_concurrency *int     (clear)
  max_budget_usd  *float64 (clear)
  description / kind / prompt / prompt_file / model / task_filter / backend / effort  (set to "")
  skills / path_patterns / allowed_tools / denied_tools      (set to empty list)
  read_only                                                 (set to false)`,
	Args: cobra.ExactArgs(2),
	RunE: runRoleUnset,
}

func init() {
	roleAddCmd.Flags().StringVar(&roleAddDescription, "description", "", "Description")
	roleAddCmd.Flags().StringVar(&roleAddKind, "kind", "", "Role runtime kind (interactive or worker)")
	roleAddCmd.Flags().StringVar(&roleAddPrompt, "prompt", "", "Inline prompt text")
	roleAddCmd.Flags().StringVar(&roleAddPromptFile, "prompt-file", "", "Path to prompt file (relative to workspace)")
	roleAddCmd.Flags().StringVar(&roleAddModel, "model", "", "Model identifier")
	roleAddCmd.Flags().StringVar(&roleAddBackend, "backend", "", "AI backend (e.g., claude, codex)")
	roleAddCmd.Flags().StringVar(&roleAddEffort, "effort", "", "Agent effort (low, medium, high, xhigh, max)")
	roleAddCmd.Flags().StringSliceVar(&roleAddSkills, "skills", nil, "Skills (comma-separated or repeat flag)")
	roleAddCmd.Flags().IntVar(&roleAddMaxConc, "max-concurrency", 0, "Max concurrent agents (0 = unlimited)")
	roleAddCmd.Flags().BoolVar(&roleAddReadOnly, "read-only", false, "Read-only role")

	roleListCmd.Flags().BoolVar(&roleListJSON, "json", false, "JSON output")
	roleShowCmd.Flags().BoolVar(&roleShowJSON, "json", false, "JSON output")

	roleCmd.AddCommand(roleAddCmd, roleListCmd, roleShowCmd, roleRemoveCmd, roleSetCmd, roleUnsetCmd)
	cli.RegisterCommand(roleCmd)
}

func runRoleAdd(_ *cobra.Command, args []string) error {
	// Kind first: an invalid kind is the more fundamental error, and reporting
	// the prompt problem ahead of it suggests `--kind interactive` on a value
	// that was never going to be accepted.
	if err := validateRoleKindValue(roleAddKind); err != nil {
		return err
	}
	if err := validateRolePromptFile(args[0], roleAddKind, roleAddPromptFile); err != nil {
		return err
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		in := store.RoleCreate{
			WorkspaceKey: ws,
			Name:         args[0],
			Kind:         normalizeRoleKindValue(roleAddKind),
			Description:  roleAddDescription,
			Prompt:       roleAddPrompt,
			PromptFile:   roleAddPromptFile,
			Model:        roleAddModel,
			Backend:      roleAddBackend,
			Effort:       roleAddEffort,
			Skills:       roleAddSkills,
			ReadOnly:     roleAddReadOnly,
		}
		if roleAddMaxConc > 0 {
			v := roleAddMaxConc
			in.MaxConcurrency = &v
		}
		r, err := h.Store.Roles().Create(ctx, in)
		if err != nil {
			return fmt.Errorf("create role: %w", err)
		}
		fmt.Printf("Created role %s/%s\n", r.WorkspaceKey, r.Name)
		return nil
	})
}

func runRoleList(_ *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		roles, err := h.Store.Roles().List(ctx, ws)
		if err != nil {
			return fmt.Errorf("list roles: %w", err)
		}
		if roleListJSON {
			return cmdstore.WriteJSON(roles)
		}
		if len(roles) == 0 {
			fmt.Printf("No roles in workspace %s\n", ws)
			return nil
		}
		for _, r := range roles {
			desc := r.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			fmt.Printf("%-20s %s\n", r.Name, desc)
		}
		return nil
	})
}

func runRoleShow(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		r, err := h.Store.Roles().Get(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get role: %w", err)
		}
		if roleShowJSON {
			return cmdstore.WriteJSON(r)
		}
		fmt.Printf("Workspace:    %s\n", r.WorkspaceKey)
		fmt.Printf("Name:         %s\n", r.Name)
		if r.Description != "" {
			fmt.Printf("Description:  %s\n", r.Description)
		}
		if r.Kind != "" {
			fmt.Printf("Kind:         %s\n", r.Kind)
		}
		if r.Model != "" {
			fmt.Printf("Model:        %s\n", r.Model)
		}
		if r.Backend != "" {
			fmt.Printf("Backend:      %s\n", r.Backend)
		}
		if r.Effort != "" {
			fmt.Printf("Effort:       %s\n", r.Effort)
		}
		if r.Prompt != "" {
			fmt.Printf("Prompt:       %s\n", r.Prompt)
		}
		if r.PromptFile != "" {
			fmt.Printf("Prompt file:  %s\n", r.PromptFile)
		}
		if len(r.Skills) > 0 {
			fmt.Printf("Skills:       %s\n", strings.Join(r.Skills, ", "))
		}
		if r.MaxConcurrency != nil {
			fmt.Printf("Max concurrency: %d\n", *r.MaxConcurrency)
		}
		if r.ReadOnly {
			fmt.Printf("Read-only:    true\n")
		}
		return nil
	})
}

func runRoleRemove(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := h.Store.Roles().Delete(ctx, ws, args[0]); err != nil {
			return fmt.Errorf("remove role: %w", err)
		}
		fmt.Printf("Removed role %s/%s\n", ws, args[0])
		return nil
	})
}

func runRoleSet(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		name, key, value := args[0], args[1], args[2]
		patch, err := buildRolePatch(key, value, false /* unset */)
		if err != nil {
			return err
		}
		if err := validateRoleUpdate(ctx, h.Store.Roles(), ws, name, key, value); err != nil {
			return err
		}
		if _, err := h.Store.Roles().Update(ctx, ws, name, patch); err != nil {
			return fmt.Errorf("update role: %w", err)
		}
		fmt.Printf("Set %s/%s.%s = %s\n", ws, name, key, value)
		return nil
	})
}

func runRoleUnset(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		name, key := args[0], args[1]
		patch, err := buildRolePatch(key, "" /* value */, true /* unset */)
		if err != nil {
			return err
		}
		// Clearing the kind is a mutation like any other: it drops an
		// interactive role back to the worker default, builtin: prompt and all.
		if err := validateRoleUpdate(ctx, h.Store.Roles(), ws, name, key, "" /* value */); err != nil {
			return err
		}
		if _, err := h.Store.Roles().Update(ctx, ws, name, patch); err != nil {
			return fmt.Errorf("update role: %w", err)
		}
		fmt.Printf("Cleared %s/%s.%s\n", ws, name, key)
		return nil
	})
}

// buildRolePatch produces a store.RoleUpdate for a single key. When unset
// is true, *int / *float64 fields use the clear-via-double-pointer
// signal (&nil); string fields go to "" / empty slice; bool to false.
//
//nolint:cyclop,funlen // Mirrors the supported role patch fields one-to-one.
func buildRolePatch(key, value string, unset bool) (store.RoleUpdate, error) {
	var patch store.RoleUpdate
	switch key {
	case "description":
		patch.Description = strPtr(value)
	case "kind":
		if err := validateRoleKindValue(value); err != nil {
			return patch, err
		}
		patch.Kind = strPtr(normalizeRoleKindValue(value))
	case "prompt":
		patch.Prompt = strPtr(value)
	case "prompt_file":
		patch.PromptFile = strPtr(value)
	case "model":
		patch.Model = strPtr(value)
	case "task_filter":
		// The role's filter is the one the daemon router actually reads, so an
		// unrecognized value here degrades routing silently. Reject it at input
		// time and store the canonical spelling.
		canonical, err := cli.ValidateTaskFilter(value)
		if err != nil {
			return patch, err
		}
		patch.TaskFilter = strPtr(canonical)
	case "backend":
		patch.Backend = strPtr(value)
	case "effort":
		patch.Effort = strPtr(value)
	case "read_only":
		if unset {
			b := false
			patch.ReadOnly = &b
			return patch, nil
		}
		b, err := strconv.ParseBool(value)
		if err != nil {
			return patch, fmt.Errorf("read_only must be true/false: %w", err)
		}
		patch.ReadOnly = &b
	case "max_priority":
		if unset {
			var nilInt *int
			patch.MaxPriority = &nilInt
			return patch, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return patch, fmt.Errorf("max_priority must be an integer: %w", err)
		}
		ptr := &n
		patch.MaxPriority = &ptr
	case "max_concurrency":
		if unset {
			var nilInt *int
			patch.MaxConcurrency = &nilInt
			return patch, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return patch, fmt.Errorf("max_concurrency must be an integer: %w", err)
		}
		ptr := &n
		patch.MaxConcurrency = &ptr
	case "max_budget_usd":
		if unset {
			var nilF *float64
			patch.MaxBudgetUSD = &nilF
			return patch, nil
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return patch, fmt.Errorf("max_budget_usd must be a number: %w", err)
		}
		ptr := &f
		patch.MaxBudgetUSD = &ptr
	case "skills":
		patch.Skills = sliceCSVPtr(value)
	case "path_patterns":
		patch.PathPatterns = sliceCSVPtr(value)
	case "allowed_tools":
		patch.AllowedTools = sliceCSVPtr(value)
	case "denied_tools":
		patch.DeniedTools = sliceCSVPtr(value)
	default:
		return patch, fmt.Errorf("unknown key %q (run 'loom role set --help' for supported keys)", key)
	}
	return patch, nil
}

// strPtr → *string for the simple "set this string" path. Empty input
// yields a non-nil pointer to "" so unset of string fields lands as
// "set to empty" on the wire.
func strPtr(s string) *string { return &s }

func normalizeRoleKindValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateRoleKindValue(value string) error {
	switch normalizeRoleKindValue(value) {
	case "", string(domain.RoleKindInteractive), string(domain.RoleKindWorker):
		return nil
	default:
		return fmt.Errorf("kind must be interactive or worker")
	}
}

// validateRolePromptFile rejects a builtin: prompt a role cannot actually use.
//
// The builtin registry holds interactive terminal prompts only (lead,
// pr-review, pr-review-checkout), and only GenerateTerminalPrompt resolves the
// prefix. The daemon's role resolver does not: it joins the literal string onto
// the project dir, so a worker role carrying builtin:pr-review stores fine and
// then fails daemon startup with
//
//	prompt file ".../builtin:pr-review" not found
//
// which names a path nobody wrote and takes the whole supervisor down with it —
// every agent in the workspace, not just the misconfigured one. Refuse it here,
// where the mistake is still attributable to the command that made it.
//
// The kind is resolved the way the daemon resolves it (domain.ResolveRoleKind),
// so a legacy interactive role name with no explicit --kind is judged the same
// on both sides.
//
// An unknown id is refused too, on interactive roles as well: the role stores
// fine and then fails at terminal spawn with "unknown built-in interactive
// prompt", far from the typo that caused it, and the valid ids are already in
// hand here.
func validateRolePromptFile(roleName, kind, promptFile string) error {
	promptFile = strings.TrimSpace(promptFile)
	if !strings.HasPrefix(promptFile, builtinPromptPrefix) {
		return nil
	}
	role := &domain.Role{Kind: domain.RoleKind(normalizeRoleKindValue(kind))}
	if domain.ResolveRoleKind(role, roleName) != domain.RoleKindInteractive {
		return fmt.Errorf(
			"prompt-file %q is a built-in interactive prompt and cannot be used by a worker role; "+
				"pass --kind interactive to use it, or give a prompt file path relative to the workspace root (built-ins: %s)",
			promptFile, strings.Join(builtinPromptIDs(), ", "))
	}
	id := strings.TrimSpace(strings.TrimPrefix(promptFile, builtinPromptPrefix))
	if !domain.IsBuiltinInteractivePrompt(id) {
		return fmt.Errorf(
			"prompt-file %q names no built-in interactive prompt; "+
				"use one of the built-ins (%s), or give a prompt file path relative to the workspace root",
			promptFile, strings.Join(builtinPromptIDs(), ", "))
	}
	return nil
}

// validateRoleUpdate applies that same rule to the update path.
//
// buildRolePatch sees one key at a time and knows nothing about what the role
// already holds, so three separate updates can each assemble the
// daemon-killing pair from a valid starting point: setting prompt_file to a
// builtin: on a worker role, setting kind to worker on an interactive role that
// legitimately carries one, or unsetting the kind so it falls back to worker.
// Validating the value alone cannot see any of them — this validates the
// combination the store would be left holding.
func validateRoleUpdate(ctx context.Context, roles store.RoleStore, ws, name, key, value string) error {
	if key != "kind" && key != "prompt_file" {
		return nil
	}
	current, err := roles.Get(ctx, ws, name)
	if err != nil {
		return fmt.Errorf("read role %q: %w", name, err)
	}
	var kind, promptFile string
	if current != nil {
		kind, promptFile = string(current.Kind), current.PromptFile
	}
	if key == "kind" {
		kind = value
	} else {
		promptFile = value
	}
	return validateRolePromptFile(name, kind, promptFile)
}

const builtinPromptPrefix = "builtin:"

func builtinPromptIDs() []string {
	prompts := domain.BuiltinInteractivePrompts()
	ids := make([]string, 0, len(prompts))
	for _, p := range prompts {
		if !p.Hidden {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// sliceCSVPtr returns a non-nil *[]string for the patch. Empty input
// becomes an empty slice (which fleet-db treats as "set to empty list",
// equivalent to clearing the field).
func sliceCSVPtr(csv string) *[]string {
	if csv == "" {
		empty := []string{}
		return &empty
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return &out
}
