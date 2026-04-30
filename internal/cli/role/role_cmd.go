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
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	roleAddDescription string
	roleAddPromptFile  string
	roleAddModel       string
	roleAddBackend     string
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
  prompt_file     string
  model           string
  task_filter     string
  backend         string
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
  description / prompt_file / model / task_filter / backend  (set to "")
  skills / path_patterns / allowed_tools / denied_tools      (set to empty list)
  read_only                                                 (set to false)`,
	Args: cobra.ExactArgs(2),
	RunE: runRoleUnset,
}

func init() {
	roleAddCmd.Flags().StringVar(&roleAddDescription, "description", "", "Description")
	roleAddCmd.Flags().StringVar(&roleAddPromptFile, "prompt-file", "", "Path to prompt file (relative to workspace)")
	roleAddCmd.Flags().StringVar(&roleAddModel, "model", "", "Model identifier")
	roleAddCmd.Flags().StringVar(&roleAddBackend, "backend", "", "AI backend (e.g., claude, codex)")
	roleAddCmd.Flags().StringSliceVar(&roleAddSkills, "skills", nil, "Skills (comma-separated or repeat flag)")
	roleAddCmd.Flags().IntVar(&roleAddMaxConc, "max-concurrency", 0, "Max concurrent agents (0 = unlimited)")
	roleAddCmd.Flags().BoolVar(&roleAddReadOnly, "read-only", false, "Read-only role")

	roleListCmd.Flags().BoolVar(&roleListJSON, "json", false, "JSON output")
	roleShowCmd.Flags().BoolVar(&roleShowJSON, "json", false, "JSON output")

	roleCmd.AddCommand(roleAddCmd, roleListCmd, roleShowCmd, roleRemoveCmd, roleSetCmd, roleUnsetCmd)
	cli.RegisterCommand(roleCmd)
}

func runRoleAdd(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		in := store.RoleCreate{
			WorkspaceKey: ws,
			Name:         args[0],
			Description:  roleAddDescription,
			PromptFile:   roleAddPromptFile,
			Model:        roleAddModel,
			Backend:      roleAddBackend,
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
		if r.Model != "" {
			fmt.Printf("Model:        %s\n", r.Model)
		}
		if r.Backend != "" {
			fmt.Printf("Backend:      %s\n", r.Backend)
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
func buildRolePatch(key, value string, unset bool) (store.RoleUpdate, error) {
	var patch store.RoleUpdate
	switch key {
	case "description":
		patch.Description = strPtr(value)
	case "prompt_file":
		patch.PromptFile = strPtr(value)
	case "model":
		patch.Model = strPtr(value)
	case "task_filter":
		patch.TaskFilter = strPtr(value)
	case "backend":
		patch.Backend = strPtr(value)
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
