// Package role registers the `loom role` noun-verb commands for
// fleet-db-backed Role CRUD within the active workspace.
package role

import (
	"context"
	"fmt"
	"os"
	"sort"
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
	roleAddDescription    string
	roleAddKind           string
	roleAddPrompt         string
	roleAddPromptFile     string
	roleAddModel          string
	roleAddBackend        string
	roleAddEffort         string
	roleAddSkills         []string
	roleAddLabels         []string
	roleAddExcludeLabels  []string
	roleAddMaxConc        int
	roleAddReadOnly       bool
	roleAddInputPolicy    []string
	roleAddInputPolicyDef string

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
  executor        string (turn/conversation)
  backend         string
  effort          string (low/medium/high/xhigh/max)
  read_only       bool ("true"/"false")
  max_priority    integer
  max_concurrency integer
  max_budget_usd  float
  max_run_duration integer (seconds; 0 disables the run-duration cap)
  skills          comma-separated list
  labels          comma-separated list (issue must carry ALL of these; evaluated after exclude_labels)
  exclude_labels  comma-separated list (issue rejected if it carries ANY of these; evaluated before labels)
  path_patterns   comma-separated list
  allowed_tools   comma-separated list
  denied_tools    comma-separated list
  input_policy    comma-separated KIND=DISPOSITION pairs

input_policy controls which interactive harness prompts an agent in this role
may auto-answer. DISPOSITION is one of deny, allow, ask. The reserved KIND
"default" sets the disposition for every kind not named; anything unnamed with
no default is denied, and so is a role with no policy at all. "ask" has no
human attached yet and currently behaves as deny (the agent logs when it does).

  loom role set task input_policy "default=deny,trust_prompt=allow"`,
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
  max_run_duration *int    (clear — the role falls back to the daemon default)
  input_policy    (clear — the role then auto-answers no harness prompt)
  description / kind / prompt / prompt_file / model / task_filter / backend / effort  (set to "")
  skills / labels / exclude_labels / path_patterns / allowed_tools / denied_tools      (set to empty list)
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
	roleAddCmd.Flags().StringSliceVar(&roleAddLabels, "labels", nil, "Issue must carry ALL of these labels (comma-separated or repeat flag)")
	roleAddCmd.Flags().StringSliceVar(&roleAddExcludeLabels, "exclude-labels", nil, "Reject issue if it carries ANY of these labels (comma-separated or repeat flag)")
	roleAddCmd.Flags().IntVar(&roleAddMaxConc, "max-concurrency", 0, "Max concurrent agents (0 = unlimited)")
	roleAddCmd.Flags().BoolVar(&roleAddReadOnly, "read-only", false, "Read-only role (hard on claude/codex/gemini; prompt-only elsewhere, and the daemon logs which one you get)")
	roleAddCmd.Flags().StringSliceVar(&roleAddInputPolicy, "input-policy", nil, "Auto-answer disposition per harness prompt kind, KIND=deny|allow|ask (comma-separated or repeat flag)")
	roleAddCmd.Flags().StringVar(&roleAddInputPolicyDef, "input-policy-default", "", "Disposition for prompt kinds --input-policy does not name (deny|allow|ask; default deny)")

	roleListCmd.Flags().BoolVar(&roleListJSON, "json", false, "JSON output")
	roleShowCmd.Flags().BoolVar(&roleShowJSON, "json", false, "JSON output")

	roleCmd.AddCommand(roleAddCmd, roleListCmd, roleShowCmd, roleRemoveCmd, roleSetCmd, roleUnsetCmd)
	cli.RegisterCommand(roleCmd)
}

func runRoleAdd(_ *cobra.Command, args []string) error {
	if err := validateRoleKindValue(roleAddKind); err != nil {
		return err
	}
	inputPolicy, err := buildAddInputPolicy(roleAddInputPolicyDef, roleAddInputPolicy)
	if err != nil {
		return err
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		in := store.RoleCreate{
			WorkspaceKey:  ws,
			Name:          args[0],
			Kind:          normalizeRoleKindValue(roleAddKind),
			Description:   roleAddDescription,
			Prompt:        roleAddPrompt,
			PromptFile:    roleAddPromptFile,
			Model:         roleAddModel,
			Backend:       roleAddBackend,
			Effort:        roleAddEffort,
			Skills:        roleAddSkills,
			Labels:        trimFilterLabels(roleAddLabels),
			ExcludeLabels: trimFilterLabels(roleAddExcludeLabels),
			ReadOnly:      roleAddReadOnly,
			InputPolicy:   inputPolicy,
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
		warnDroppedLabelConstraints(r, in.Labels, in.ExcludeLabels)
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

// printRoleRouting prints the fields that decide which tasks a role claims.
// Split out of runRoleShow: label routing added two more optional lines to a
// function that was already one long chain of "print it if it is set", which
// tipped it over the cognitive-complexity limit.
func printRoleRouting(r *domain.Role) {
	if len(r.Skills) > 0 {
		fmt.Printf("Skills:       %s\n", strings.Join(r.Skills, ", "))
	}
	if len(r.Labels) > 0 {
		fmt.Printf("Labels:       %s\n", strings.Join(r.Labels, ", "))
	}
	if len(r.ExcludeLabels) > 0 {
		fmt.Printf("Exclude labels: %s\n", strings.Join(r.ExcludeLabels, ", "))
	}
	if r.MaxConcurrency != nil {
		fmt.Printf("Max concurrency: %d\n", *r.MaxConcurrency)
	}
	if r.ReadOnly {
		fmt.Printf("Read-only:    true\n")
	}
	// Printed only when set. A role with no policy denies every prompt, which
	// is also what the whole rest of the fleet does, so a line on every role
	// would be noise; the interesting state is a role that has opted
	// something in.
	if r.InputPolicy != nil {
		fmt.Printf("Input policy: %s\n", formatInputPolicy(r.InputPolicy))
	}
}

// printRoleIdentity prints the descriptive fields: who the role is and how it
// is executed, as opposed to what it claims.
func printRoleIdentity(r *domain.Role) {
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
	if r.Executor != "" {
		fmt.Printf("Executor:     %s\n", r.Executor)
	}
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
		printRoleIdentity(r)
		printRoleRouting(r)
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
		r, err := h.Store.Roles().Update(ctx, ws, name, patch)
		if err != nil {
			return fmt.Errorf("update role: %w", err)
		}
		fmt.Printf("Set %s/%s.%s = %s\n", ws, name, key, value)
		warnDroppedLabelConstraints(r, derefSlice(patch.Labels), derefSlice(patch.ExcludeLabels))
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
	case "executor":
		// Closed vocabulary, validated client-side so a typo fails here with
		// the accepted values instead of as a server 400: "" (clear, same as
		// turn), "turn", or "conversation".
		if value != "" && value != "turn" && value != "conversation" {
			return store.RoleUpdate{}, fmt.Errorf("executor must be %q or %q (empty clears it)", "turn", "conversation")
		}
		patch.Executor = strPtr(value)
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
	case "max_run_duration":
		if unset {
			var nilInt *int
			patch.MaxRunDuration = &nilInt
			return patch, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return patch, fmt.Errorf("max_run_duration must be an integer number of seconds: %w", err)
		}
		// 0 is accepted, not rejected: it is how a role says "no wall-clock cap
		// on my runs". Clearing the field (unset) means something different —
		// inherit the daemon default — which is why both spellings exist.
		ptr := &n
		patch.MaxRunDuration = &ptr
	case "skills":
		patch.Skills = sliceCSVPtr(value)
	case "labels":
		patch.Labels = sliceCSVPtr(value)
	case "exclude_labels":
		patch.ExcludeLabels = sliceCSVPtr(value)
	case "path_patterns":
		patch.PathPatterns = sliceCSVPtr(value)
	case "allowed_tools":
		patch.AllowedTools = sliceCSVPtr(value)
	case "denied_tools":
		patch.DeniedTools = sliceCSVPtr(value)
	case "input_policy":
		if unset {
			// Clearing lands as the deny-everything zero value, not as an
			// empty-but-present policy: a role with `{}` and a role with
			// nothing must resolve identically, and both must resolve to deny.
			var nilPolicy *domain.RoleInputPolicy
			patch.InputPolicy = &nilPolicy
			return patch, nil
		}
		policy, err := parseInputPolicySpec(splitCSV(value))
		if err != nil {
			return patch, err
		}
		patch.InputPolicy = &policy
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

// warnDroppedLabelConstraints reports label constraints that were written but
// did not come back on the stored role.
//
// The wire encoding is fail-open by construction: the create/patch bodies carry
// `labels` / `exclude_labels` as ordinary JSON, and a fleet-db deployment that
// predates label constraints ignores the unknown fields and answers 200. The
// write "succeeds", `loom role show` comes back without the constraints, and
// the routing gate the operator just configured is simply absent — the role
// goes on claiming every issue, which is the exact failure this feature exists
// to prevent. Silence there is the worst outcome, so say it out loud.
//
// stored == nil is treated as "not persisted": every backend returns the stored
// role on success, so a nil here means we cannot confirm the write either way.
func warnDroppedLabelConstraints(stored *domain.Role, wantLabels, wantExclude []string) {
	var missing []string
	if len(wantLabels) > 0 && (stored == nil || len(stored.Labels) == 0) {
		missing = append(missing, "labels")
	}
	if len(wantExclude) > 0 && (stored == nil || len(stored.ExcludeLabels) == 0) {
		missing = append(missing, "exclude_labels")
	}
	if len(missing) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"warning: %s was accepted but not stored - label routing is NOT active for this role.\n"+
			"  The backend is most likely older than label-constraint support; deploy fleet-db first,\n"+
			"  then re-apply. Confirm with: loom role show <name>\n",
		strings.Join(missing, " and "))
}

// derefSlice returns the pointed-to slice, or nil when the patch leaves the
// field alone.
func derefSlice(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func validateRoleKindValue(value string) error {
	switch normalizeRoleKindValue(value) {
	case "", string(domain.RoleKindInteractive), string(domain.RoleKindWorker):
		return nil
	default:
		return fmt.Errorf("kind must be interactive or worker")
	}
}

// trimFilterLabels trims whitespace and drops empty elements from a
// --labels/--exclude-labels flag value. Labels are hard-reject constraints
// (unlike Skills' soft demote), so a stray whitespace-only element must not
// survive: it would never match a real issue label, silently starving a
// required-labels role or leaving an exclude-labels-only role with an
// effectively empty constraint list (falling through the routing-check
// activation guard, the exact bug this constraint exists to prevent).
func trimFilterLabels(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// sliceCSVPtr returns a non-nil *[]string for the patch. Empty input
// becomes an empty slice (which fleet-db treats as "set to empty list",
// equivalent to clearing the field).
func sliceCSVPtr(csv string) *[]string {
	out := splitCSV(csv)
	if out == nil {
		out = []string{}
	}
	return &out
}

// splitCSV trims and drops empties from a comma-separated value.
func splitCSV(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// inputPolicyDefaultKind is the reserved KIND that sets RoleInputPolicy.Default
// in the flat KIND=DISPOSITION spelling the CLI accepts. It is the same word the
// JSON field uses, and no harness names a prompt kind "default" — a role that
// somehow needs one has to go through the API or the YAML, which is a documented
// limit rather than a silent collision.
const inputPolicyDefaultKind = "default"

// parseInputPolicySpec turns KIND=DISPOSITION entries into a policy.
//
// It validates against the same closed vocabulary and the same bounds the
// server enforces, with the server's wording, so a typo fails here instead of
// after a round trip. Returning nil for an empty spec keeps "said nothing" and
// "denies everything" the same value all the way down.
func parseInputPolicySpec(entries []string) (*domain.RoleInputPolicy, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	policy := &domain.RoleInputPolicy{}
	for _, entry := range entries {
		kind, disposition, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("input_policy entry %q must be KIND=DISPOSITION (one of deny, allow, ask)", entry)
		}
		kind = strings.TrimSpace(kind)
		disposition = strings.ToLower(strings.TrimSpace(disposition))
		if kind == "" {
			return nil, fmt.Errorf("role input_policy kind is required")
		}
		// An explicitly empty disposition is rejected rather than accepted as
		// the deny it would resolve to. Resolution has to treat unset as deny
		// (a half-written policy must fail closed), but a human typing
		// "trust_prompt=" at a terminal has made a mistake, and quietly
		// accepting it hides which of the two they meant.
		valid := disposition != "" && domain.ValidateRoleInputDisposition(disposition)
		// The reserved kind is checked before the message is chosen so a bad
		// value there reports as a bad DEFAULT — the field it actually sets,
		// and the wording the server would have used for it.
		if kind == inputPolicyDefaultKind {
			if !valid {
				return nil, fmt.Errorf("role input_policy default %q must be one of deny, allow, ask", disposition)
			}
			policy.Default = disposition
			continue
		}
		if !valid {
			return nil, fmt.Errorf("role input_policy kind %q disposition %q must be one of deny, allow, ask", kind, disposition)
		}
		if policy.Kinds == nil {
			policy.Kinds = make(map[string]string, len(entries))
		}
		if _, dup := policy.Kinds[kind]; dup {
			return nil, fmt.Errorf("input_policy names kind %q twice", kind)
		}
		policy.Kinds[kind] = disposition
	}
	if err := domain.ValidateRoleInputPolicy(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

// buildAddInputPolicy merges `role add`'s two policy flags. The dedicated
// --input-policy-default is applied first so an explicit `default=` inside
// --input-policy wins; they are the same knob, so last-one-wins is the only
// answer that does not need the operator to remember a precedence rule.
func buildAddInputPolicy(defaultDisposition string, entries []string) (*domain.RoleInputPolicy, error) {
	defaultDisposition = strings.ToLower(strings.TrimSpace(defaultDisposition))
	if defaultDisposition != "" {
		entries = append([]string{inputPolicyDefaultKind + "=" + defaultDisposition}, entries...)
	}
	return parseInputPolicySpec(entries)
}

// formatInputPolicy renders a policy for `loom role show` in the same
// KIND=DISPOSITION spelling `loom role set` accepts, so what is displayed can
// be pasted back. Kinds are sorted because Go map order is not stable and a
// role's displayed policy should not change between two reads of the same role.
func formatInputPolicy(p *domain.RoleInputPolicy) string {
	if p == nil {
		return ""
	}
	parts := make([]string, 0, len(p.Kinds)+1)
	if p.Default != "" {
		parts = append(parts, inputPolicyDefaultKind+"="+p.Default)
	}
	kinds := make([]string, 0, len(p.Kinds))
	for kind := range p.Kinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		parts = append(parts, kind+"="+p.Kinds[kind])
	}
	if len(parts) == 0 {
		// A present-but-empty policy resolves exactly like no policy, so say
		// what it does rather than printing a blank line the reader has to
		// interpret.
		return "deny (empty policy)"
	}
	return strings.Join(parts, ", ")
}
