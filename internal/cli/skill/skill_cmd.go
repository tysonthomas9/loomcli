// Package skill registers the `loom skill` noun-verb commands for
// fleet-db-backed Skill CRUD within the active workspace.
package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const manualSkillSource = "manual"

// Kept replaceable so command tests can exercise the real command handlers
// against memstore without opening a fleet-db connection.
var skillWithActiveWorkspace = cmdstore.WithActiveWorkspace

// Kept replaceable alongside skillWithActiveWorkspace so command tests can
// use the real fetch path through a local httptest codeload endpoint.
var skillGitHubInstaller = githubSkillInstaller{}

type skillCreateFlags struct {
	description string
	content     string
	scope       string
	files       []string
}

type skillInstallFlags struct {
	scope string
	name  string
}

type skillListFlags struct {
	role string
	json bool
}

type skillShowFlags struct {
	scope string
	json  bool
}

type skillUpdateFlags struct {
	description string
	content     string
	scope       string
	files       []string
}

type skillDeleteFlags struct {
	scope string
	force bool
}

func init() {
	cli.RegisterCommand(newSkillCommand())
}

func newSkillCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "skill",
		Short:   "Manage skills within the active workspace",
		GroupID: "workspace",
	}
	cmd.AddCommand(
		newSkillCreateCommand(),
		newSkillImportCommand(),
		newSkillInstallCommand(),
		newSkillListCommand(),
		newSkillPackCommand(),
		newSkillShowCommand(),
		newSkillSyncCommand(),
		newSkillUpdateCommand(),
		newSkillDeleteCommand(),
	)
	return cmd
}

func newSkillInstallCommand() *cobra.Command {
	var flags skillInstallFlags
	cmd := &cobra.Command{
		Use:   "install <SOURCE>",
		Short: "Install a skill from GitHub",
		Long: `Install a skill from a GitHub repository or subpath.

In a GitHub /tree/ URL, Loom treats exactly one path segment after /tree/ as
the ref. For branches containing /, use owner/repo/sub/path@<ref> instead.`,
		Args: skillWriteArgs("loom skill install"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillInstall(cmd, args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.scope, "scope", string(domain.SkillScopeWorkspace), "Skill scope: workspace or role=<name>")
	cmd.Flags().StringVar(&flags.name, "name", "", "Override the skill name from SKILL.md")
	return cmd
}

func newSkillCreateCommand() *cobra.Command {
	var flags skillCreateFlags
	cmd := &cobra.Command{
		Use:   "create <NAME>",
		Short: "Create a skill in the active workspace",
		Args:  skillWriteArgs("loom skill create"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillCreate(cmd, args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.description, "description", "", "Description of when to use the skill")
	cmd.Flags().StringVar(&flags.content, "content", "-", "Path to the SKILL.md body (- for stdin)")
	cmd.Flags().StringVar(&flags.scope, "scope", string(domain.SkillScopeWorkspace), "Skill scope: workspace or role=<name>")
	cmd.Flags().StringArrayVar(&flags.files, "file", nil, "Bundled text file as <source>[:<destination>] (repeatable)")
	return cmd
}

func newSkillListCommand() *cobra.Command {
	var flags skillListFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List skills in the active workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSkillList(cmd, flags)
		},
	}
	cmd.Flags().StringVar(&flags.role, "role", "", "Resolve the skill chain for this role and mark workspace overrides")
	cmd.Flags().BoolVar(&flags.json, "json", false, "JSON output")
	return cmd
}

func newSkillShowCommand() *cobra.Command {
	var flags skillShowFlags
	cmd := &cobra.Command{
		Use:   "show <NAME>",
		Short: "Show skill details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillShow(cmd, args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.scope, "scope", string(domain.SkillScopeWorkspace), "Skill scope: workspace or role=<name>")
	cmd.Flags().BoolVar(&flags.json, "json", false, "JSON output")
	return cmd
}

func newSkillUpdateCommand() *cobra.Command {
	var flags skillUpdateFlags
	cmd := &cobra.Command{
		Use:   "update <NAME>",
		Short: "Update a skill in the active workspace",
		Long: `Update one or more fields on a skill. --file replaces the complete
bundled file set when present; omit it to leave bundled files unchanged.`,
		Args: skillWriteArgs("loom skill update"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillUpdate(cmd, args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.description, "description", "", "Replacement description")
	cmd.Flags().StringVar(&flags.content, "content", "", "Path to the replacement SKILL.md body (- for stdin)")
	cmd.Flags().StringVar(&flags.scope, "scope", string(domain.SkillScopeWorkspace), "Skill scope: workspace or role=<name>")
	cmd.Flags().StringArrayVar(&flags.files, "file", nil, "Replacement bundled text file as <source>[:<destination>] (repeatable)")
	return cmd
}

func newSkillDeleteCommand() *cobra.Command {
	var flags skillDeleteFlags
	cmd := &cobra.Command{
		Use:   "delete <NAME>",
		Short: "Delete a skill from the active workspace",
		Args:  skillWriteArgs("loom skill delete"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillDelete(cmd, args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.scope, "scope", string(domain.SkillScopeWorkspace), "Skill scope: workspace or role=<name>")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Delete a skill owned by another actor (requires skill.force_overwrite)")
	return cmd
}

func runSkillCreate(cmd *cobra.Command, name string, flags skillCreateFlags) error {
	if err := refuseAgentSkillWrite("loom skill create"); err != nil {
		return err
	}
	ref, err := parseSkillRef(name, flags.scope)
	if err != nil {
		return err
	}
	if strings.TrimSpace(flags.description) == "" {
		return fmt.Errorf("--description must not be empty")
	}
	content, err := readSkillText(flags.content, cmd.InOrStdin(), "skill content")
	if err != nil {
		return err
	}
	files, err := readSkillFiles(flags.files)
	if err != nil {
		return err
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		sk, err := h.Store.Skills().Create(ctx, store.SkillCreate{
			WorkspaceKey: ws,
			Ref:          ref,
			Description:  flags.description,
			Content:      content,
			Files:        files,
			Source:       manualSkillSource,
		})
		if err != nil {
			return skillWriteError("create", err, false)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created skill %s/%s\n", sk.WorkspaceKey, sk.Ref())
		return nil
	})
}

func runSkillInstall(cmd *cobra.Command, source string, flags skillInstallFlags) error {
	if err := refuseAgentSkillWrite("loom skill install"); err != nil {
		return err
	}
	if _, _, err := parseSkillScope(flags.scope); err != nil {
		return err
	}
	if cmd.Flags().Changed("name") {
		if err := domain.ValidateSkillName(flags.name); err != nil {
			return err
		}
	}
	fetched, err := skillGitHubInstaller.Fetch(cmd.Context(), source, flags.name)
	if err != nil {
		return fmt.Errorf("fetch skill from GitHub: %w", err)
	}
	if len(fetched.DroppedFrontmatterKeys) > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Notice: dropped SKILL.md frontmatter keys: %s\n", strings.Join(fetched.DroppedFrontmatterKeys, ", "))
	}
	if len(fetched.SkippedHiddenPaths) > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Notice: skipped hidden skill paths: %s\n", strings.Join(fetched.SkippedHiddenPaths, ", "))
	}
	ref, err := parseSkillRef(fetched.Name, flags.scope)
	if err != nil {
		return err
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		sk, err := h.Store.Skills().Create(ctx, store.SkillCreate{
			WorkspaceKey: ws,
			Ref:          ref,
			Description:  fetched.Description,
			Content:      fetched.Content,
			Files:        fetched.Files,
			Source:       fetched.Source,
		})
		if err != nil {
			return skillInstallWriteError(ref, err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed skill %s/%s\n", sk.WorkspaceKey, sk.Ref())
		return nil
	})
}

func runSkillList(cmd *cobra.Command, flags skillListFlags) error {
	role := strings.TrimSpace(flags.role)
	if cmd.Flags().Changed("role") && role == "" {
		return fmt.Errorf("--role must not be empty")
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		skills, err := h.Store.Skills().List(ctx, ws, store.SkillFilter{})
		if err != nil {
			return fmt.Errorf("list skills: %w", err)
		}
		entries := skillListEntries(skills, role)
		if flags.json {
			return writeSkillJSON(cmd.OutOrStdout(), entries)
		}
		if len(entries) == 0 {
			if role != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No skills resolve for role %s in workspace %s\n", role, ws)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No skills in workspace %s\n", ws)
			}
			return nil
		}
		for _, entry := range entries {
			sk := entry.Skill
			status := ""
			if entry.Status != "" {
				status = " status=" + entry.Status
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s scope=%-20s provenance=%s%s\n",
				sk.Name, formatSkillScope(sk.Ref()), formatSkillProvenance(sk), status)
		}
		return nil
	})
}

func runSkillShow(cmd *cobra.Command, name string, flags skillShowFlags) error {
	ref, err := parseSkillRef(name, flags.scope)
	if err != nil {
		return err
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		sk, err := h.Store.Skills().Get(ctx, ws, ref)
		if err != nil {
			return fmt.Errorf("get skill: %w", err)
		}
		if flags.json {
			return writeSkillJSON(cmd.OutOrStdout(), sk)
		}
		renderSkill(cmd.OutOrStdout(), sk)
		return nil
	})
}

func runSkillUpdate(cmd *cobra.Command, name string, flags skillUpdateFlags) error {
	if err := refuseAgentSkillWrite("loom skill update"); err != nil {
		return err
	}
	ref, err := parseSkillRef(name, flags.scope)
	if err != nil {
		return err
	}
	var patch store.SkillUpdate
	if cmd.Flags().Changed("description") {
		patch.Description = &flags.description
	}
	if cmd.Flags().Changed("content") {
		content, err := readSkillText(flags.content, cmd.InOrStdin(), "skill content")
		if err != nil {
			return err
		}
		patch.Content = &content
	}
	if cmd.Flags().Changed("file") {
		files, err := readSkillFiles(flags.files)
		if err != nil {
			return err
		}
		patch.Files = &files
	}
	if patch.Description == nil && patch.Content == nil && patch.Files == nil {
		return fmt.Errorf("nothing to update; pass --description, --content, or --file")
	}
	patch.Source = manualSkillSource
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		sk, err := h.Store.Skills().Update(ctx, ws, ref, patch)
		if err != nil {
			return skillWriteError("update", err, false)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated skill %s/%s\n", sk.WorkspaceKey, sk.Ref())
		return nil
	})
}

func runSkillDelete(cmd *cobra.Command, name string, flags skillDeleteFlags) error {
	if err := refuseAgentSkillWrite("loom skill delete"); err != nil {
		return err
	}
	ref, err := parseSkillRef(name, flags.scope)
	if err != nil {
		return err
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		err := h.Store.Skills().Delete(ctx, ws, ref, store.SkillDelete{
			Source: manualSkillSource,
			Force:  flags.force,
		})
		if err != nil {
			return skillWriteError("delete", err, true)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted skill %s/%s\n", ws, ref)
		return nil
	})
}

func refuseAgentSkillWrite(command string) error {
	agent, set := os.LookupEnv("LOOM_AGENT_NAME")
	if !set {
		return nil
	}
	if agent == "" {
		agent = "(empty)"
	}
	return fmt.Errorf("%s refused because LOOM_AGENT_NAME is set to %q: agent-initiated skill writes are deferred by amendment A1; run this command from a human operator shell with LOOM_AGENT_NAME unset", command, agent)
}

// skillWriteArgs runs the A1 gate before ordinary positional validation. This
// keeps even an incomplete agent-originated invocation on the deliberate
// "writes are deferred" path instead of leaking through to a generic usage
// error that hides the product decision.
func skillWriteArgs(command string) cobra.PositionalArgs {
	return skillWriteExactArgs(command, 1)
}

func skillWriteExactArgs(command string, count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := refuseAgentSkillWrite(command); err != nil {
			return err
		}
		return cobra.ExactArgs(count)(cmd, args)
	}
}

func skillWriteMaximumArgs(command string, count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := refuseAgentSkillWrite(command); err != nil {
			return err
		}
		return cobra.MaximumNArgs(count)(cmd, args)
	}
}

func parseSkillRef(name, scope string) (domain.SkillRef, error) {
	if err := domain.ValidateSkillName(name); err != nil {
		return domain.SkillRef{}, err
	}
	skillScope, role, err := parseSkillScope(scope)
	if err != nil {
		return domain.SkillRef{}, err
	}
	if skillScope == domain.SkillScopeRole {
		return domain.RoleSkillRef(role, name), nil
	}
	return domain.WorkspaceSkillRef(name), nil
}

func parseSkillScope(scope string) (domain.SkillScope, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == string(domain.SkillScopeWorkspace) {
		return domain.SkillScopeWorkspace, "", nil
	}
	if role, ok := strings.CutPrefix(scope, string(domain.SkillScopeRole)+"="); ok && strings.TrimSpace(role) != "" {
		return domain.SkillScopeRole, strings.TrimSpace(role), nil
	}
	return "", "", fmt.Errorf("--scope must be %q or %q, got %q", "workspace", "role=<name>", scope)
}

func readSkillText(filePath string, stdin io.Reader, label string) (string, error) {
	var (
		data []byte
		err  error
	)
	if filePath == "" || filePath == "-" {
		data, err = io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read %s from stdin: %w", label, err)
		}
	} else {
		data, err = os.ReadFile(filePath) //nolint:gosec // G304 — the operator names the file to read
		if err != nil {
			return "", fmt.Errorf("read %s from %q: %w", label, filePath, err)
		}
	}
	if !isSkillText(data) {
		return "", fmt.Errorf("%s must be UTF-8 text; binary content is not supported", label)
	}
	return string(data), nil
}

func isSkillText(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

func readSkillFiles(specs []string) ([]domain.SkillFile, error) {
	files := make([]domain.SkillFile, 0, len(specs))
	destinations := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		source, destination, hasDestination := strings.Cut(spec, ":")
		if strings.TrimSpace(source) == "" {
			return nil, fmt.Errorf("--file must be <source>[:<destination>], got %q", spec)
		}
		if !hasDestination {
			destination = filepath.Base(source)
		} else if destination == "" {
			return nil, fmt.Errorf("--file destination must not be empty in %q", spec)
		}
		if err := domain.ValidateSkillFilePath(destination); err != nil {
			return nil, fmt.Errorf("--file %q: %w", spec, err)
		}
		if _, duplicate := destinations[destination]; duplicate {
			return nil, fmt.Errorf("--file destination %q is specified more than once", destination)
		}
		info, err := os.Stat(source)
		if err != nil {
			return nil, fmt.Errorf("stat bundled skill file %q: %w", source, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("bundled skill file %q must be a regular file", source)
		}
		content, err := readSkillText(source, nil, "bundled skill file")
		if err != nil {
			return nil, err
		}
		files = append(files, domain.SkillFile{
			Path:       destination,
			Content:    content,
			Executable: info.Mode().Perm()&0o111 != 0,
		})
		destinations[destination] = struct{}{}
	}
	return files, nil
}

type skillListEntry struct {
	*domain.Skill
	Status string `json:"status,omitempty"`
}

func skillListEntries(skills []*domain.Skill, role string) []skillListEntry {
	entries := make([]skillListEntry, 0, len(skills)+1)
	if role == "" {
		for _, sk := range skills {
			if sk != nil {
				entries = append(entries, skillListEntry{Skill: sk})
			}
		}
	} else {
		for _, resolved := range domain.ResolveSkillChainDetail(skills, role) {
			if resolved.Shadowed != nil {
				entries = append(entries, skillListEntry{Skill: resolved.Shadowed, Status: "overridden"})
			}
			entries = append(entries, skillListEntry{Skill: resolved.Skill})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Ref().String() < entries[j].Ref().String()
	})
	return entries
}

func formatSkillScope(ref domain.SkillRef) string {
	if ref.Scope == domain.SkillScopeRole {
		return "role=" + ref.RoleName
	}
	return string(ref.Scope)
}

func formatSkillProvenance(sk *domain.Skill) string {
	createdBy := sk.CreatedBy
	if createdBy == "" {
		createdBy = "(unknown)"
	}
	source := sk.Source
	if source == "" {
		source = "(unknown)"
	}
	if sk.SourceRef != "" {
		source += "@" + sk.SourceRef
	}
	return "created_by=" + createdBy + " source=" + source
}

func renderSkill(w io.Writer, sk *domain.Skill) {
	_, _ = fmt.Fprintf(w, "Workspace:         %s\n", sk.WorkspaceKey)
	_, _ = fmt.Fprintf(w, "Name:              %s\n", sk.Name)
	_, _ = fmt.Fprintf(w, "Scope:             %s\n", formatSkillScope(sk.Ref()))
	_, _ = fmt.Fprintf(w, "Description:       %s\n", sk.Description)
	_, _ = fmt.Fprintf(w, "Created by:        %s\n", valueOrUnknown(sk.CreatedBy))
	_, _ = fmt.Fprintf(w, "Updated by:        %s\n", valueOrUnknown(sk.UpdatedBy))
	_, _ = fmt.Fprintf(w, "Source:            %s\n", valueOrUnknown(sk.Source))
	if sk.SourceRef != "" {
		_, _ = fmt.Fprintf(w, "Source ref:        %s\n", sk.SourceRef)
	}
	if !sk.CreatedAt.IsZero() {
		_, _ = fmt.Fprintf(w, "Created at:        %s\n", sk.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
	if !sk.UpdatedAt.IsZero() {
		_, _ = fmt.Fprintf(w, "Updated at:        %s\n", sk.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
	_, _ = fmt.Fprintf(w, "Content revision:  %s\n", valueOrUnknown(sk.ContentRevision))
	_, _ = fmt.Fprintln(w, "Content:")
	_, _ = fmt.Fprintln(w, sk.Content)
	if len(sk.Files) == 0 {
		_, _ = fmt.Fprintln(w, "Files:             (none)")
		return
	}
	files := append([]domain.SkillFile(nil), sk.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	_, _ = fmt.Fprintln(w, "Files:")
	for _, file := range files {
		_, _ = fmt.Fprintf(w, "  Path:            %s\n", file.Path)
		_, _ = fmt.Fprintf(w, "  Executable:      %t\n", file.Executable)
		_, _ = fmt.Fprintf(w, "  Revision:        %s\n", valueOrUnknown(file.Revision))
		_, _ = fmt.Fprintln(w, "  Content:")
		for _, line := range strings.Split(file.Content, "\n") {
			_, _ = fmt.Fprintf(w, "    %s\n", line)
		}
	}
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "(unknown)"
	}
	return value
}

func writeSkillJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

func skillWriteError(action string, err error, forceAvailable bool) error {
	var conflict *domain.SkillProvenanceConflictError
	if errors.As(err, &conflict) || errors.Is(err, domain.ErrSkillProvenanceConflict) {
		ref := conflictRef(conflict)
		owner, provenance := "(unknown)", ""
		if conflict != nil {
			owner = valueOrUnknown(conflict.ExistingCreatedBy)
			if conflict.ExistingSource != "" {
				provenance = " from source " + conflict.ExistingSource
			}
		}
		advice := "a force-capable overwrite is required; this command does not expose --force"
		if forceAvailable {
			advice = "re-run with --force if you have the skill.force_overwrite permission"
		}
		return fmt.Errorf("%s skill: provenance conflict for %s: existing skill is owned by %s%s; %s: %w",
			action, ref, owner, provenance, advice, err)
	}

	var stale *domain.SkillPreconditionError
	if errors.As(err, &stale) || errors.Is(err, domain.ErrSkillPreconditionFailed) {
		ref, document, revisions := "the skill", "the requested document", ""
		showCommand := "loom skill show <name> --scope <scope>"
		if stale != nil {
			if stale.Ref.String() != "" {
				ref = stale.Ref.String()
				showCommand = fmt.Sprintf("loom skill show %s --scope %s", stale.Ref.Name, formatSkillScope(stale.Ref))
			}
			if stale.Path != "" {
				document = stale.Path
			}
			if stale.Expected != "" || stale.Stored != "" {
				revisions = fmt.Sprintf(" (expected %s, stored %s)", valueOrUnknown(stale.Expected), valueOrUnknown(stale.Stored))
			}
		}
		return fmt.Errorf("%s skill: stale revision for %s document %s%s; re-read with %q, merge the latest content, and retry: %w",
			action, ref, document, revisions, showCommand, err)
	}

	return fmt.Errorf("%s skill: %w", action, err)
}

func skillInstallWriteError(ref domain.SkillRef, err error) error {
	if errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("install skill: %w; choose a different name with --name <new-name>, or update the existing skill with %q",
			err, fmt.Sprintf("loom skill update %s --scope %s", ref.Name, formatSkillScope(ref)))
	}
	return skillWriteError("install", err, false)
}

func conflictRef(conflict *domain.SkillProvenanceConflictError) string {
	if conflict == nil || conflict.Ref.String() == "" {
		return "the requested skill"
	}
	return conflict.Ref.String()
}
