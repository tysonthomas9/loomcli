package worker

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

var (
	workerProfileAddName         string
	workerProfileAddRole         string
	workerProfileAddBackend      string
	workerProfileAddRepos        []string
	workerProfileAddMaxPriority  int
	workerProfileAddParentEpic   string
	workerProfileAddLabels       []string
	workerProfileAddCapabilities []string
	workerProfileAddMetadata     []string
	workerProfileAddDisabled     bool

	workerProfileListRole    string
	workerProfileListBackend string
	workerProfileListEnabled string
	workerProfileListLimit   int
	workerProfileListJSON    bool
	workerProfileShowJSON    bool
)

var workerProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage worker profiles in the active workspace",
}

var workerProfileAddCmd = &cobra.Command{
	Use:   "add <PROFILE_ID>",
	Short: "Create a worker profile",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkerProfileAdd,
}

var workerProfileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worker profiles",
	Args:  cobra.NoArgs,
	RunE:  runWorkerProfileList,
}

var workerProfileShowCmd = &cobra.Command{
	Use:   "show <PROFILE_ID>",
	Short: "Show worker profile details",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkerProfileShow,
}

var workerProfileSetCmd = &cobra.Command{
	Use:   "set <PROFILE_ID> <KEY> <VALUE>",
	Short: "Set a worker profile field",
	Long: `Set a worker profile field by key. Supported keys:
  name          string
  role          string
  backend       string
  parent_epic   string
  enabled       bool ("true"/"false")
  max_priority  integer 0..4
  repos         comma-separated list
  labels        comma-separated list
  capabilities  comma-separated list
  metadata      comma-separated key=value list`,
	Args: cobra.ExactArgs(3),
	RunE: runWorkerProfileSet,
}

var workerProfileUnsetCmd = &cobra.Command{
	Use:   "unset <PROFILE_ID> <KEY>",
	Short: "Clear a worker profile field",
	Long: `Clear a worker profile field. Supported keys:
  backend
  parent_epic
  max_priority
  repos
  labels
  capabilities
  metadata`,
	Args: cobra.ExactArgs(2),
	RunE: runWorkerProfileUnset,
}

var workerProfileRemoveCmd = &cobra.Command{
	Use:   "remove <PROFILE_ID>",
	Short: "Delete a worker profile",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkerProfileRemove,
}

func initWorkerProfileCommands() {
	workerProfileAddCmd.Flags().StringVar(&workerProfileAddName, "name", "", "Display name (default: profile ID)")
	workerProfileAddCmd.Flags().StringVar(&workerProfileAddRole, "role", "", "Worker role")
	workerProfileAddCmd.Flags().StringVar(&workerProfileAddBackend, "backend", "", "Preferred backend")
	workerProfileAddCmd.Flags().StringSliceVar(&workerProfileAddRepos, "repo", nil, "Repo scope (comma-separated or repeat flag)")
	workerProfileAddCmd.Flags().IntVar(&workerProfileAddMaxPriority, "max-priority", -1, "Maximum priority 0..4 (-1 = unset)")
	workerProfileAddCmd.Flags().StringVar(&workerProfileAddParentEpic, "parent-epic", "", "Parent epic scope")
	workerProfileAddCmd.Flags().StringSliceVar(&workerProfileAddLabels, "label", nil, "Profile label (comma-separated or repeat flag)")
	workerProfileAddCmd.Flags().StringSliceVar(&workerProfileAddCapabilities, "capability", nil, "Capability (comma-separated or repeat flag)")
	workerProfileAddCmd.Flags().StringArrayVar(&workerProfileAddMetadata, "metadata", nil, "Metadata key=value (repeatable)")
	workerProfileAddCmd.Flags().BoolVar(&workerProfileAddDisabled, "disabled", false, "Create disabled")
	_ = workerProfileAddCmd.MarkFlagRequired("role")

	workerProfileListCmd.Flags().StringVar(&workerProfileListRole, "role", "", "Filter by role")
	workerProfileListCmd.Flags().StringVar(&workerProfileListBackend, "backend", "", "Filter by backend")
	workerProfileListCmd.Flags().StringVar(&workerProfileListEnabled, "enabled", "", "Filter by enabled true/false")
	workerProfileListCmd.Flags().IntVar(&workerProfileListLimit, "limit", 0, "Maximum rows")
	workerProfileListCmd.Flags().BoolVar(&workerProfileListJSON, "json", false, "JSON output")
	workerProfileShowCmd.Flags().BoolVar(&workerProfileShowJSON, "json", false, "JSON output")

	workerProfileCmd.AddCommand(workerProfileAddCmd, workerProfileListCmd, workerProfileShowCmd, workerProfileSetCmd, workerProfileUnsetCmd, workerProfileRemoveCmd)
	workerCmd.AddCommand(workerProfileCmd)
}

func runWorkerProfileAdd(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		metadata, err := parseWorkerProfileMetadata(workerProfileAddMetadata)
		if err != nil {
			return err
		}
		enabled := !workerProfileAddDisabled
		in := store.WorkerProfileCreate{
			WorkspaceKey: ws,
			ProfileID:    args[0],
			Name:         workerProfileAddName,
			Role:         workerProfileAddRole,
			Backend:      workerProfileAddBackend,
			Repos:        workerProfileAddRepos,
			ParentEpic:   workerProfileAddParentEpic,
			Labels:       workerProfileAddLabels,
			Capabilities: workerProfileAddCapabilities,
			Enabled:      &enabled,
			Metadata:     metadata,
		}
		if workerProfileAddMaxPriority >= 0 {
			in.MaxPriority = &workerProfileAddMaxPriority
		}
		profile, err := h.Store.WorkerProfiles().Create(ctx, in)
		if err != nil {
			return fmt.Errorf("create worker profile: %w", err)
		}
		fmt.Printf("Created worker profile %s/%s\n", profile.WorkspaceKey, profile.ProfileID)
		return nil
	})
}

func runWorkerProfileList(_ *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		enabled, err := parseOptionalBool(workerProfileListEnabled)
		if err != nil {
			return fmt.Errorf("enabled must be true/false: %w", err)
		}
		profiles, err := h.Store.WorkerProfiles().List(ctx, ws, store.WorkerProfileFilter{
			Role:    workerProfileListRole,
			Backend: workerProfileListBackend,
			Enabled: enabled,
			Limit:   workerProfileListLimit,
		})
		if err != nil {
			return fmt.Errorf("list worker profiles: %w", err)
		}
		if workerProfileListJSON {
			return cmdstore.WriteJSON(profiles)
		}
		if len(profiles) == 0 {
			fmt.Printf("No worker profiles in workspace %s\n", ws)
			return nil
		}
		for _, p := range profiles {
			fmt.Printf("%-24s %-12s %-12s %-8t %s\n", p.ProfileID, p.Role, p.Backend, p.Enabled, strings.Join(p.Repos, ","))
		}
		return nil
	})
}

func runWorkerProfileShow(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		profile, err := h.Store.WorkerProfiles().Get(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get worker profile: %w", err)
		}
		if workerProfileShowJSON {
			return cmdstore.WriteJSON(profile)
		}
		printWorkerProfile(profile)
		return nil
	})
}

func runWorkerProfileSet(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		profileID, key, value := args[0], args[1], args[2]
		patch, err := buildWorkerProfilePatch(key, value, false)
		if err != nil {
			return err
		}
		if _, err := h.Store.WorkerProfiles().Update(ctx, ws, profileID, patch); err != nil {
			return fmt.Errorf("update worker profile: %w", err)
		}
		fmt.Printf("Set %s/%s.%s = %s\n", ws, profileID, key, value)
		return nil
	})
}

func runWorkerProfileUnset(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		profileID, key := args[0], args[1]
		patch, err := buildWorkerProfilePatch(key, "", true)
		if err != nil {
			return err
		}
		if _, err := h.Store.WorkerProfiles().Update(ctx, ws, profileID, patch); err != nil {
			return fmt.Errorf("update worker profile: %w", err)
		}
		fmt.Printf("Cleared %s/%s.%s\n", ws, profileID, key)
		return nil
	})
}

func runWorkerProfileRemove(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := h.Store.WorkerProfiles().Delete(ctx, ws, args[0]); err != nil {
			return fmt.Errorf("remove worker profile: %w", err)
		}
		fmt.Printf("Removed worker profile %s/%s\n", ws, args[0])
		return nil
	})
}

func printWorkerProfile(p *domain.WorkerProfile) {
	fmt.Printf("Workspace:    %s\n", p.WorkspaceKey)
	fmt.Printf("Profile ID:   %s\n", p.ProfileID)
	fmt.Printf("Name:         %s\n", p.Name)
	fmt.Printf("Role:         %s\n", p.Role)
	if p.Backend != "" {
		fmt.Printf("Backend:      %s\n", p.Backend)
	}
	if len(p.Repos) > 0 {
		fmt.Printf("Repos:        %s\n", strings.Join(p.Repos, ", "))
	}
	if p.MaxPriority != nil {
		fmt.Printf("Max priority: %d\n", *p.MaxPriority)
	}
	if p.ParentEpic != "" {
		fmt.Printf("Parent epic:  %s\n", p.ParentEpic)
	}
	if len(p.Labels) > 0 {
		fmt.Printf("Labels:       %s\n", strings.Join(p.Labels, ", "))
	}
	if len(p.Capabilities) > 0 {
		fmt.Printf("Capabilities: %s\n", strings.Join(p.Capabilities, ", "))
	}
	fmt.Printf("Enabled:      %t\n", p.Enabled)
}

type workerProfilePatchBuilder func(value string, unset bool) (store.WorkerProfileUpdate, error)

var workerProfilePatchBuilders = map[string]workerProfilePatchBuilder{
	"name":         workerProfileRequiredStringPatch("name", func(p *store.WorkerProfileUpdate, v string) { p.Name = &v }),
	"role":         workerProfileRequiredStringPatch("role", func(p *store.WorkerProfileUpdate, v string) { p.Role = &v }),
	"backend":      workerProfileStringPatch(func(p *store.WorkerProfileUpdate, v string) { p.Backend = &v }),
	"parent_epic":  workerProfileStringPatch(func(p *store.WorkerProfileUpdate, v string) { p.ParentEpic = &v }),
	"enabled":      buildWorkerProfileEnabledPatch,
	"max_priority": buildWorkerProfileMaxPriorityPatch,
	"repos":        workerProfileListPatch(func(p *store.WorkerProfileUpdate, v []string) { p.Repos = &v }),
	"labels":       workerProfileListPatch(func(p *store.WorkerProfileUpdate, v []string) { p.Labels = &v }),
	"capabilities": workerProfileListPatch(func(p *store.WorkerProfileUpdate, v []string) { p.Capabilities = &v }),
	"metadata":     buildWorkerProfileMetadataPatch,
}

func buildWorkerProfilePatch(key, value string, unset bool) (store.WorkerProfileUpdate, error) {
	builder, ok := workerProfilePatchBuilders[key]
	if !ok {
		return store.WorkerProfileUpdate{}, fmt.Errorf("unsupported worker profile field %q", key)
	}
	return builder(value, unset)
}

func workerProfileRequiredStringPatch(field string, set func(*store.WorkerProfileUpdate, string)) workerProfilePatchBuilder {
	return func(value string, unset bool) (store.WorkerProfileUpdate, error) {
		if unset {
			return store.WorkerProfileUpdate{}, fmt.Errorf("%s cannot be unset", field)
		}
		var patch store.WorkerProfileUpdate
		set(&patch, value)
		return patch, nil
	}
}

func workerProfileStringPatch(set func(*store.WorkerProfileUpdate, string)) workerProfilePatchBuilder {
	return func(value string, _ bool) (store.WorkerProfileUpdate, error) {
		var patch store.WorkerProfileUpdate
		set(&patch, value)
		return patch, nil
	}
}

func workerProfileListPatch(set func(*store.WorkerProfileUpdate, []string)) workerProfilePatchBuilder {
	return func(value string, _ bool) (store.WorkerProfileUpdate, error) {
		var patch store.WorkerProfileUpdate
		list := splitWorkerProfileList(value)
		set(&patch, list)
		return patch, nil
	}
}

func buildWorkerProfileEnabledPatch(value string, unset bool) (store.WorkerProfileUpdate, error) {
	if unset {
		return store.WorkerProfileUpdate{}, fmt.Errorf("enabled cannot be unset")
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return store.WorkerProfileUpdate{}, fmt.Errorf("enabled must be true/false: %w", err)
	}
	return store.WorkerProfileUpdate{Enabled: &b}, nil
}

func buildWorkerProfileMaxPriorityPatch(value string, unset bool) (store.WorkerProfileUpdate, error) {
	if unset {
		return store.WorkerProfileUpdate{ClearMaxPriority: true}, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return store.WorkerProfileUpdate{}, fmt.Errorf("max_priority must be an integer: %w", err)
	}
	if n < 0 || n > 4 {
		return store.WorkerProfileUpdate{}, fmt.Errorf("max_priority must be between 0 and 4")
	}
	return store.WorkerProfileUpdate{MaxPriority: &n}, nil
}

func buildWorkerProfileMetadataPatch(value string, unset bool) (store.WorkerProfileUpdate, error) {
	if unset {
		metadata := map[string]string{}
		return store.WorkerProfileUpdate{Metadata: &metadata}, nil
	}
	metadata, err := parseWorkerProfileMetadata(strings.Split(value, ","))
	if err != nil {
		return store.WorkerProfileUpdate{}, err
	}
	return store.WorkerProfileUpdate{Metadata: &metadata}, nil
}

func parseWorkerProfileMetadata(items []string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("metadata must be key=value")
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func parseOptionalBool(raw string) (*bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func splitWorkerProfileList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}
