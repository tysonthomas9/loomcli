package skill

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type skillPackAddFlags struct {
	path string
}

func newSkillPackCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Manage registered GitHub skill packs",
	}
	cmd.AddCommand(
		newSkillPackAddCommand(),
		newSkillPackListCommand(),
		newSkillPackRemoveCommand(),
	)
	return cmd
}

func newSkillPackAddCommand() *cobra.Command {
	var flags skillPackAddFlags
	cmd := &cobra.Command{
		Use:   "add <NAME> <GITHUB-SOURCE>",
		Short: "Register a GitHub skill pack",
		Args:  skillWriteExactArgs("loom skill pack add", 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillPackAdd(cmd, args[0], args[1], flags)
		},
	}
	cmd.Flags().StringVar(&flags.path, "path", "", "Discovery root within the repository")
	return cmd
}

func newSkillPackListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered skill packs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSkillPackList(cmd)
		},
	}
}

func newSkillPackRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <NAME>",
		Short: "Remove a registered skill pack",
		Args:  skillWriteArgs("loom skill pack remove"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillPackRemove(cmd, args[0])
		},
	}
}

func newSkillSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync [PACK]",
		Short: "Sync one or all registered skill packs",
		Args:  skillWriteMaximumArgs("loom skill sync", 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pack := ""
			if len(args) == 1 {
				pack = args[0]
			}
			return runSkillSync(cmd, pack)
		},
	}
}

func runSkillPackAdd(cmd *cobra.Command, name, rawSource string, flags skillPackAddFlags) error {
	if err := refuseAgentSkillWrite("loom skill pack add"); err != nil {
		return err
	}
	if err := domain.ValidateSkillPackName(name); err != nil {
		return err
	}
	source, err := parseGitHubSource(rawSource)
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("path") {
		packPath, err := normalizeSkillPackPath(flags.path)
		if err != nil {
			return err
		}
		if source.Subpath != "" {
			return fmt.Errorf("GitHub source already includes path %q; pass the discovery root in either GITHUB-SOURCE or --path, not both", source.Subpath)
		}
		source.Subpath = packPath
	}
	repoURL := "https://github.com/" + source.Owner + "/" + source.Repo
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		pack, err := h.Store.SkillPacks().Create(ctx, store.SkillPackCreate{
			WorkspaceKey: ws,
			Name:         name,
			RepoURL:      repoURL,
			Ref:          source.Ref,
			Path:         source.Subpath,
		})
		if err != nil {
			return fmt.Errorf("add skill pack: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added skill pack %s/%s\n", pack.WorkspaceKey, pack.Name)
		return nil
	})
}

func normalizeSkillPackPath(value string) (string, error) {
	value = strings.Trim(value, "/")
	if value == "" {
		return "", nil
	}
	segments, err := sourcePathSegments(value)
	if err != nil {
		return "", fmt.Errorf("--path: %w", err)
	}
	return strings.Join(segments, "/"), nil
}

func runSkillPackList(cmd *cobra.Command) error {
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		packs, err := h.Store.SkillPacks().List(ctx, ws)
		if err != nil {
			return fmt.Errorf("list skill packs: %w", err)
		}
		if len(packs) == 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No skill packs in workspace %s\n", ws)
			return nil
		}
		for _, pack := range packs {
			if pack == nil {
				continue
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s source=%s last_sync=%s\n",
				pack.Name, formatSkillPackSource(pack), formatSkillPackSync(pack))
		}
		return nil
	})
}

func formatSkillPackSource(pack *domain.SkillPack) string {
	source := strings.TrimRight(pack.RepoURL, "/")
	if pack.Path != "" {
		source += "/" + pack.Path
	}
	if pack.Ref != "" {
		source += "@" + pack.Ref
	}
	return source
}

func formatSkillPackSync(pack *domain.SkillPack) string {
	if pack.LastSyncStatus == "" {
		return "never"
	}
	state := pack.LastSyncStatus
	if !pack.LastSyncedAt.IsZero() {
		state += " at=" + pack.LastSyncedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if pack.LastSyncedCommit != "" {
		state += " commit=" + pack.LastSyncedCommit
	}
	if pack.LastSyncError != "" {
		state += " error=" + fmt.Sprintf("%q", pack.LastSyncError)
	}
	return state
}

func runSkillPackRemove(cmd *cobra.Command, name string) error {
	if err := refuseAgentSkillWrite("loom skill pack remove"); err != nil {
		return err
	}
	if err := domain.ValidateSkillPackName(name); err != nil {
		return err
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := h.Store.SkillPacks().Delete(ctx, ws, name); err != nil {
			return fmt.Errorf("remove skill pack: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed skill pack %s/%s\n", ws, name)
		return nil
	})
}

func runSkillSync(cmd *cobra.Command, packName string) error {
	if err := refuseAgentSkillWrite("loom skill sync"); err != nil {
		return err
	}
	if packName != "" {
		if err := domain.ValidateSkillPackName(packName); err != nil {
			return err
		}
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		var packs []*domain.SkillPack
		if packName != "" {
			pack, err := h.Store.SkillPacks().Get(ctx, ws, packName)
			if err != nil {
				return fmt.Errorf("get skill pack %q: %w", packName, err)
			}
			packs = []*domain.SkillPack{pack}
		} else {
			var err error
			packs, err = h.Store.SkillPacks().List(ctx, ws)
			if err != nil {
				return fmt.Errorf("list skill packs: %w", err)
			}
		}
		if len(packs) == 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No skill packs in workspace %s\n", ws)
			return nil
		}

		var syncErrors []error
		for _, pack := range packs {
			if pack == nil {
				continue
			}
			if err := syncSkillPack(ctx, cmd, h, ws, pack); err != nil {
				syncErrors = append(syncErrors, err)
			}
		}
		return errors.Join(syncErrors...)
	})
}

// packSkillOutcome is what one skill in a pack sync ended up as. Skipped is not
// a failure: a name owned by another source, or by another actor, is left alone
// deliberately, and a pack whose skills are all skipped still syncs OK.
type packSkillOutcome int

const (
	packSkillSkipped packSkillOutcome = iota
	packSkillFailed
	packSkillWritten
)

// reportUpsertFailure prints why one skill could not be written and classifies
// it. A provenance conflict is a skip, not a failure: the name belongs to
// another actor and leaving it alone is the correct outcome, so it must not
// count towards the every-skill-failed check that fails the whole pack.
func (r packSyncRun) reportUpsertFailure(name string, err error) packSkillOutcome {
	out := r.cmd.OutOrStdout()
	var conflict *domain.SkillProvenanceConflictError
	if errors.As(err, &conflict) || errors.Is(err, domain.ErrSkillProvenanceConflict) {
		owner, existingSource := "(unknown)", "(unknown)"
		if conflict != nil {
			owner = valueOrUnknown(conflict.ExistingCreatedBy)
			existingSource = valueOrUnknown(conflict.ExistingSource)
		}
		_, _ = fmt.Fprintf(out, "pack %s: %s: skipped: provenance belongs to %s from source %s\n",
			r.pack.Name, name, owner, existingSource)
		return packSkillSkipped
	}
	_, _ = fmt.Fprintf(out, "pack %s: %s: failed: %v\n", r.pack.Name, name, err)
	return packSkillFailed
}

// packSyncRun is the invariant half of one pack sync: everything the per-skill
// step needs that does not change between skills.
type packSyncRun struct {
	cmd    *cobra.Command
	h      *bootstrap.StoreHandle
	ws     string
	pack   *domain.SkillPack
	source string
	commit string
}

// syncSkill writes one discovered skill and reports what happened, printing its
// line as it goes. Callers iterate in discovery order and the lines must stay in
// that order — the pack sync output is read top to bottom.
func (r packSyncRun) syncSkill(ctx context.Context, candidate fetchedGitHubSkillResult) (packSkillOutcome, string) {
	out := r.cmd.OutOrStdout()
	if candidate.Err != nil {
		label := candidate.Directory
		if label == "" {
			label = "."
		}
		_, _ = fmt.Fprintf(out, "pack %s: %s: failed: %v\n", r.pack.Name, label, candidate.Err)
		return packSkillFailed, ""
	}
	name := candidate.Skill.Name
	if len(candidate.Skill.SkippedHiddenPaths) > 0 {
		_, _ = fmt.Fprintf(r.cmd.ErrOrStderr(), "Notice: pack %s skill %s skipped hidden skill paths: %s\n",
			r.pack.Name, name, strings.Join(candidate.Skill.SkippedHiddenPaths, ", "))
	}

	ref := domain.WorkspaceSkillRef(name)
	existing, err := r.h.Store.Skills().Get(ctx, r.ws, ref)
	if err == nil && existing.Source != r.source {
		_, _ = fmt.Fprintf(out, "pack %s: %s: skipped: exists with source %s (not this pack)\n",
			r.pack.Name, name, valueOrUnknown(existing.Source))
		return packSkillSkipped, ""
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		_, _ = fmt.Fprintf(out, "pack %s: %s: failed: get existing skill: %v\n", r.pack.Name, name, err)
		return packSkillFailed, ""
	}

	_, created, err := r.h.Store.Skills().Upsert(ctx, store.SkillUpsert{Skill: store.SkillCreate{
		WorkspaceKey: r.ws,
		Ref:          ref,
		Description:  candidate.Skill.Description,
		Content:      candidate.Skill.Content,
		Files:        candidate.Skill.Files,
		Source:       r.source,
		SourceRef:    r.commit,
	}})
	if err != nil {
		return r.reportUpsertFailure(name, err), ""
	}

	outcome := "updated"
	if created {
		outcome = "created"
	}
	_, _ = fmt.Fprintf(out, "pack %s: %s: %s\n", r.pack.Name, name, outcome)
	return packSkillWritten, name
}

func syncSkillPack(ctx context.Context, cmd *cobra.Command, h *bootstrap.StoreHandle, ws string, pack *domain.SkillPack) error {
	source, err := sourceForSkillPack(pack)
	if err != nil {
		syncErr := fmt.Errorf("parse source: %w", err)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pack %s: failed: %v\n", pack.Name, syncErr)
		return recordFailedSkillPackSync(ctx, h, ws, pack.Name, syncErr)
	}
	fetched, err := skillGitHubInstaller.FetchAllSource(ctx, source)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pack %s: failed: %v\n", pack.Name, err)
		return recordFailedSkillPackSync(ctx, h, ws, pack.Name, err)
	}
	if err := duplicatePackSkillNames(fetched.Skills); err != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pack %s: failed: %v\n", pack.Name, err)
		return recordFailedSkillPackSync(ctx, h, ws, pack.Name, err)
	}

	run := packSyncRun{cmd: cmd, h: h, ws: ws, pack: pack, source: pack.Source(), commit: fetched.Commit}
	written := make([]string, 0, len(fetched.Skills))
	failed := 0
	for _, candidate := range fetched.Skills {
		switch outcome, name := run.syncSkill(ctx, candidate); outcome {
		case packSkillFailed:
			failed++
		case packSkillWritten:
			written = append(written, name)
		case packSkillSkipped:
		}
	}

	if failed == len(fetched.Skills) {
		err := fmt.Errorf("every discovered skill failed validation or import")
		return recordFailedSkillPackSync(ctx, h, ws, pack.Name, err)
	}
	sort.Strings(written)
	if _, err := h.Store.SkillPacks().Update(ctx, ws, pack.Name, store.SkillPackUpdate{
		RecordSync: &domain.SkillPackSync{
			Status: domain.SkillPackSyncOK,
			Commit: fetched.Commit,
			Skills: written,
		},
	}); err != nil {
		return fmt.Errorf("record sync for skill pack %q: %w", pack.Name, err)
	}
	return nil
}

func duplicatePackSkillNames(skills []fetchedGitHubSkillResult) error {
	firstDirectory := make(map[string]string)
	var duplicates []string
	for _, candidate := range skills {
		if candidate.Err != nil || candidate.Skill.Name == "" {
			continue
		}
		directory := displaySkillDirectory(candidate.Directory)
		if first, ok := firstDirectory[candidate.Skill.Name]; ok {
			duplicates = append(duplicates, fmt.Sprintf("%q in %q and %q", candidate.Skill.Name, first, directory))
			continue
		}
		firstDirectory[candidate.Skill.Name] = directory
	}
	if len(duplicates) == 0 {
		return nil
	}
	sort.Strings(duplicates)
	return fmt.Errorf("duplicate skill names discovered: %s", strings.Join(duplicates, "; "))
}

func displaySkillDirectory(directory string) string {
	if directory == "" {
		return "."
	}
	return directory
}

func sourceForSkillPack(pack *domain.SkillPack) (githubSource, error) {
	source, err := parseGitHubSource(pack.RepoURL)
	if err != nil {
		return githubSource{}, err
	}
	if pack.Ref != "" {
		if err := validateGitHubRef(pack.Ref); err != nil {
			return githubSource{}, err
		}
		source.Ref = pack.Ref
	}
	if pack.Path != "" {
		if source.Subpath != "" {
			return githubSource{}, fmt.Errorf("stored skill pack has subpath %q embedded in repo source and nonempty Path %q; keep the discovery root in only one field", source.Subpath, pack.Path)
		}
		packPath, err := normalizeSkillPackPath(pack.Path)
		if err != nil {
			return githubSource{}, err
		}
		source.Subpath = packPath
	}
	return source, nil
}

func recordFailedSkillPackSync(ctx context.Context, h *bootstrap.StoreHandle, ws, name string, syncErr error) error {
	if _, err := h.Store.SkillPacks().Update(ctx, ws, name, store.SkillPackUpdate{
		RecordSync: &domain.SkillPackSync{Status: domain.SkillPackSyncFailed, Error: syncErr.Error()},
	}); err != nil {
		return errors.Join(
			fmt.Errorf("sync skill pack %q: %w", name, syncErr),
			fmt.Errorf("record failed sync for skill pack %q: %w", name, err),
		)
	}
	return fmt.Errorf("sync skill pack %q: %w", name, syncErr)
}
