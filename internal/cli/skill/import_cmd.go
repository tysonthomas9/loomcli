package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type skillImportFlags struct {
	scope string
	name  string
	force bool
}

type localSkillFile struct {
	relative   string
	executable bool
}

type assembledLocalSkill struct {
	Name                   string
	Description            string
	Snapshot               domain.SkillFileTreeSnapshot
	CanonicalRoot          string
	DroppedFrontmatterKeys []string
	SkippedHiddenPaths     []string
}

func newSkillImportCommand() *cobra.Command {
	var flags skillImportFlags
	cmd := &cobra.Command{
		Use:   "import <DIR>",
		Short: "Import a local skill directory into the active workspace",
		Args:  skillWriteArgs("loom skill import"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillImport(cmd, args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.scope, "scope", string(domain.SkillScopeWorkspace), "Skill scope: workspace or role=<name>")
	cmd.Flags().StringVar(&flags.name, "name", "", "Override the skill name from SKILL.md")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite a skill owned by another actor (requires skill.force_overwrite)")
	return cmd
}

func runSkillImport(cmd *cobra.Command, directory string, flags skillImportFlags) error {
	if err := checkSkillIngestFlags(cmd, "loom skill import", flags.scope, flags.name); err != nil {
		return err
	}
	local, err := readLocalSkillDirectory(directory, flags.name)
	if err != nil {
		return err
	}
	writeSkillImportNotices(cmd, local)
	ref, err := parseSkillRef(local.Name, flags.scope)
	if err != nil {
		return err
	}
	return skillWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		revision, err := publishSkillSnapshot(ctx, h.Store.WorkspaceFiles(), ws, local.Snapshot)
		if err != nil {
			return err
		}
		source := "import:" + local.CanonicalRoot
		existing, getErr := h.Store.Skills().Get(ctx, ws, ref)
		created := errors.Is(getErr, domain.ErrNotFound)
		if getErr != nil && !created {
			return fmt.Errorf("get skill before import: %w", getErr)
		}
		var sk *domain.Skill
		if created {
			sk, err = h.Store.Skills().Create(ctx, store.SkillCreate{
				WorkspaceKey: ws, Ref: ref, Description: local.Description,
				FileTreeRevision: revision, Source: source,
			})
			if errors.Is(err, domain.ErrAlreadyExists) {
				// Another importer may create the same Skill after our initial
				// read. Re-enter the ordinary guarded-update path so identical
				// imports converge while provenance and CAS checks still apply.
				created = false
				sk, err = updateImportedSkill(ctx, h.Store.Skills(), ws, ref, local.Description, revision, source, flags.force, nil)
			}
		} else {
			sk, err = updateImportedSkill(ctx, h.Store.Skills(), ws, ref, local.Description, revision, source, flags.force, existing)
		}
		if err != nil {
			return skillWriteError("import", err, true)
		}
		writeSkillImportResult(cmd, sk, created)
		return nil
	})
}

func updateImportedSkill(
	ctx context.Context,
	skills store.SkillStore,
	workspace string,
	ref domain.SkillRef,
	description, revision, source string,
	force bool,
	existing *domain.Skill,
) (*domain.Skill, error) {
	if force {
		// Fleet's privileged force-upsert route intentionally has no CAS
		// form; it is the explicit operator override.
		skill, _, err := skills.Upsert(ctx, store.SkillUpsert{Force: true, Skill: store.SkillCreate{
			WorkspaceKey: workspace, Ref: ref, Description: description,
			FileTreeRevision: revision, Source: source,
		}})
		return skill, err
	}
	if existing == nil {
		var err error
		existing, err = skills.Get(ctx, workspace, ref)
		if err != nil {
			return nil, err
		}
	}
	return skills.Update(ctx, workspace, ref, store.SkillUpdate{
		Description: &description, FileTreeRevision: &revision,
		ExpectedFileTreeRevision: existing.FileTreeRevision, Source: source,
	})
}

func writeSkillImportResult(cmd *cobra.Command, skill *domain.Skill, created bool) {
	outcome := "Updated"
	if created {
		outcome = "Imported"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s skill %s/%s\n", outcome, skill.WorkspaceKey, skill.Ref())
}

func writeSkillImportNotices(cmd *cobra.Command, local assembledLocalSkill) {
	if len(local.DroppedFrontmatterKeys) > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Notice: dropped SKILL.md frontmatter keys: %s\n", strings.Join(local.DroppedFrontmatterKeys, ", "))
	}
	if len(local.SkippedHiddenPaths) > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Notice: skipped hidden skill paths: %s\n", strings.Join(local.SkippedHiddenPaths, ", "))
	}
}

func readLocalSkillDirectory(directory, nameOverride string) (assembledLocalSkill, error) {
	return readLocalSkillDirectoryWithHook(directory, nameOverride, nil)
}

//nolint:gocognit,cyclop,funlen // The directory walk applies every import safety gate in one auditable pass.
func readLocalSkillDirectoryWithHook(directory, nameOverride string, beforeRead func(string) error) (assembledLocalSkill, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return assembledLocalSkill{}, fmt.Errorf("resolve skill directory %q: %w", directory, err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return assembledLocalSkill{}, fmt.Errorf("resolve symlinks in skill directory %q: %w", directory, err)
	}
	root, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return assembledLocalSkill{}, fmt.Errorf("open skill directory root %q: %w", canonicalRoot, err)
	}
	defer func() { _ = root.Close() }()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return assembledLocalSkill{}, fmt.Errorf("stat skill directory root %q: %w", canonicalRoot, err)
	}
	if !rootInfo.IsDir() {
		return assembledLocalSkill{}, fmt.Errorf("skill import path %q must be a directory", directory)
	}

	var files []localSkillFile
	var symlinks, unsupported, invalidPaths, skippedHidden []string
	var totalBytes int64
	regularFileCount := 0
	err = fs.WalkDir(root.FS(), ".", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			symlinks = append(symlinks, filePath)
			return nil
		}
		if hidden, ok := hiddenSkillPath(filePath); ok {
			skippedHidden = append(skippedHidden, hidden)
			if entry.IsDir() {
				return fs.SkipDir
			}
			info, err := root.Lstat(filePath)
			if err != nil {
				return fmt.Errorf("stat hidden skill file %q: %w", filePath, err)
			}
			if info.Mode().IsRegular() {
				regularFileCount++
				if regularFileCount > maxSkillFileCount {
					return fmt.Errorf("skill directory contains more than %d regular files", maxSkillFileCount)
				}
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := root.Lstat(filePath)
		if err != nil {
			return fmt.Errorf("stat skill file %q: %w", filePath, err)
		}
		if !info.Mode().IsRegular() {
			unsupported = append(unsupported, filePath)
			return nil
		}
		regularFileCount++
		if regularFileCount > maxSkillFileCount {
			return fmt.Errorf("skill directory contains more than %d regular files", maxSkillFileCount)
		}
		if filePath != domain.SkillFileNameSKILLMD {
			if err := domain.ValidateSkillFilePath(filePath); err != nil {
				invalidPaths = append(invalidPaths, fmt.Sprintf("%s (%v)", filePath, err))
				return nil
			}
		}
		totalBytes += info.Size()
		if totalBytes > maxSkillArchiveDecompressedBytes {
			return fmt.Errorf("local skill content exceeds the %d-byte size limit", maxSkillArchiveDecompressedBytes)
		}
		files = append(files, localSkillFile{
			relative:   filePath,
			executable: info.Mode().Perm()&0o111 != 0,
		})
		return nil
	})
	if err != nil {
		return assembledLocalSkill{}, fmt.Errorf("walk skill directory %q: %w", directory, err)
	}
	if len(symlinks) > 0 {
		sort.Strings(symlinks)
		return assembledLocalSkill{}, fmt.Errorf("skill directory contains symlinks, which are not supported: %s", strings.Join(symlinks, ", "))
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return assembledLocalSkill{}, fmt.Errorf("skill directory contains non-regular files, which are not supported: %s", strings.Join(unsupported, ", "))
	}
	if len(invalidPaths) > 0 {
		sort.Strings(invalidPaths)
		return assembledLocalSkill{}, fmt.Errorf("skill directory contains unsafe destination paths: %s", strings.Join(invalidPaths, ", "))
	}
	if beforeRead != nil {
		if err := beforeRead(canonicalRoot); err != nil {
			return assembledLocalSkill{}, fmt.Errorf("prepare skill directory read: %w", err)
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	contents := make(map[string][]byte, len(files))
	remaining := maxSkillArchiveDecompressedBytes
	for index := range files {
		file := &files[index]
		info, err := root.Lstat(file.relative)
		if err != nil {
			return assembledLocalSkill{}, fmt.Errorf("recheck skill file %q before read: %w", file.relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return assembledLocalSkill{}, fmt.Errorf("skill file %q became a symlink after traversal", file.relative)
		}
		opened, err := root.Open(file.relative)
		if err != nil {
			return assembledLocalSkill{}, fmt.Errorf("open skill file %q within root: %w", file.relative, err)
		}
		openedInfo, statErr := opened.Stat()
		if statErr != nil {
			_ = opened.Close()
			return assembledLocalSkill{}, fmt.Errorf("stat opened skill file %q: %w", file.relative, statErr)
		}
		if !openedInfo.Mode().IsRegular() {
			_ = opened.Close()
			return assembledLocalSkill{}, fmt.Errorf("opened skill file %q must remain a regular file", file.relative)
		}
		data, readErr := readWithLimit(opened, remaining, fmt.Sprintf("local skill file %q", file.relative))
		closeErr := opened.Close()
		if readErr != nil {
			return assembledLocalSkill{}, readErr
		}
		if closeErr != nil {
			return assembledLocalSkill{}, fmt.Errorf("close skill file %q: %w", file.relative, closeErr)
		}
		file.executable = openedInfo.Mode().Perm()&0o111 != 0
		remaining -= int64(len(data))
		contents[file.relative] = data
	}
	document, ok := contents[domain.SkillFileNameSKILLMD]
	if !ok {
		return assembledLocalSkill{}, fmt.Errorf("skill directory must contain %s", domain.SkillFileNameSKILLMD)
	}
	// The directory the user pointed at is the only fallback here: a local
	// import has no repository to borrow a name from.
	identity, err := resolveSkillIdentity(document, filepath.Base(absolute), nameOverride)
	if err != nil {
		return assembledLocalSkill{}, err
	}

	bundled := make([]domain.SkillFileTreeFile, 0, len(files)-1)
	documentExecutable := false
	for _, file := range files {
		if file.relative == domain.SkillFileNameSKILLMD {
			documentExecutable = file.executable
			continue
		}
		bundled = append(bundled, domain.SkillFileTreeFile{
			Path:       file.relative,
			Bytes:      contents[file.relative],
			MediaType:  skillFileMediaType(file.relative, contents[file.relative]),
			Executable: file.executable,
		})
	}
	snapshot, err := importedSkillSnapshot(document, documentExecutable, bundled, identity, nameOverride != "")
	if err != nil {
		return assembledLocalSkill{}, err
	}
	skippedHidden = uniqueSortedStrings(skippedHidden)
	droppedKeys := identity.DroppedFrontmatterKeys
	if nameOverride == "" {
		droppedKeys = nil
	}
	return assembledLocalSkill{
		Name:                   snapshot.Name,
		Description:            snapshot.Description,
		Snapshot:               snapshot,
		CanonicalRoot:          canonicalRoot,
		DroppedFrontmatterKeys: droppedKeys,
		SkippedHiddenPaths:     skippedHidden,
	}, nil
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
