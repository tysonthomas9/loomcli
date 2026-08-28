package skill

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func publishSkillSnapshot(ctx context.Context, files store.WorkspaceFileStore, workspace string, snapshot domain.SkillFileTreeSnapshot) (string, error) {
	inputs := make([]domain.WorkspaceFileInput, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		inputs = append(inputs, domain.WorkspaceFileInput(file))
	}
	published, err := files.Publish(ctx, workspace, inputs)
	if err != nil {
		return "", fmt.Errorf("publish skill file tree: %w", err)
	}
	if published == nil || published.Tree == nil || published.Tree.Revision == "" {
		return "", fmt.Errorf("publish skill file tree returned no revision: %w", domain.ErrIntegrity)
	}
	return published.Tree.Revision, nil
}

func loadSkillSnapshot(ctx context.Context, files store.WorkspaceFileStore, skill *domain.Skill) (domain.SkillFileTreeSnapshot, error) {
	if skill == nil || skill.FileTreeRevision == "" {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("skill file tree revision is required: %w", domain.ErrIntegrity)
	}
	tree, err := files.GetTree(ctx, skill.WorkspaceKey, skill.FileTreeRevision)
	if err != nil {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("get skill file tree %q: %w", skill.FileTreeRevision, err)
	}
	if tree == nil || tree.Revision != skill.FileTreeRevision {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("skill file tree identity mismatch: %w", domain.ErrIntegrity)
	}
	manifest := make([]domain.SkillFileTreeFile, 0, len(tree.Files))
	for _, file := range tree.Files {
		body, err := files.Download(ctx, skill.WorkspaceKey, tree.Revision, file.Path)
		if err != nil {
			return domain.SkillFileTreeSnapshot{}, fmt.Errorf("download skill file %q: %w", file.Path, err)
		}
		manifest = append(manifest, domain.SkillFileTreeFile{
			Path: file.Path, Bytes: body, MediaType: file.MediaType, Executable: file.Executable,
		})
	}
	snapshot, err := domain.ValidateSkillFileTree(manifest)
	if err != nil {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("validate skill file tree %q: %w", tree.Revision, err)
	}
	if snapshot.Name != skill.Name || snapshot.Description != skill.Description {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("skill metadata does not match SKILL.md: %w", domain.ErrIntegrity)
	}
	return *snapshot, nil
}

func bundledTreeFiles(files []domain.SkillFileTreeFile) []domain.SkillFileTreeFile {
	out := make([]domain.SkillFileTreeFile, 0, len(files))
	for _, file := range files {
		if file.Path != domain.SkillFileNameSKILLMD {
			out = append(out, file)
		}
	}
	return out
}

func replaceSkillBundles(snapshot domain.SkillFileTreeSnapshot, bundles []domain.SkillFileTreeFile) (domain.SkillFileTreeSnapshot, error) {
	complete := make([]domain.SkillFileTreeFile, 0, len(bundles)+1)
	for _, file := range snapshot.Files {
		if file.Path == domain.SkillFileNameSKILLMD {
			complete = append(complete, file)
			break
		}
	}
	complete = append(complete, bundles...)
	validated, err := domain.ValidateSkillFileTree(complete)
	if err != nil {
		return domain.SkillFileTreeSnapshot{}, err
	}
	return *validated, nil
}

func replaceSkillBody(snapshot domain.SkillFileTreeSnapshot, body []byte) (domain.SkillFileTreeSnapshot, error) {
	complete := make([]domain.SkillFileTreeFile, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if file.Path != domain.SkillFileNameSKILLMD {
			complete = append(complete, file)
			continue
		}
		if !bytes.HasSuffix(file.Bytes, snapshot.Body) {
			return domain.SkillFileTreeSnapshot{}, fmt.Errorf("SKILL.md parsed body is not an exact byte suffix: %w", domain.ErrIntegrity)
		}
		prefix := file.Bytes[:len(file.Bytes)-len(snapshot.Body)]
		replacement := file
		replacement.Bytes = make([]byte, 0, len(prefix)+len(body))
		replacement.Bytes = append(replacement.Bytes, prefix...)
		replacement.Bytes = append(replacement.Bytes, body...)
		complete = append(complete, replacement)
	}
	validated, err := domain.ValidateSkillFileTree(complete)
	if err != nil {
		return domain.SkillFileTreeSnapshot{}, err
	}
	return *validated, nil
}

// importedSkillSnapshot keeps an already-complete valid SKILL.md byte-for-byte
// when no identity override was requested. Only an explicit identity override
// permits rebuilding the document.
func importedSkillSnapshot(document []byte, documentExecutable bool, bundles []domain.SkillFileTreeFile, identity skillIdentity, override bool) (domain.SkillFileTreeSnapshot, error) {
	if !override {
		complete := make([]domain.SkillFileTreeFile, 0, len(bundles)+1)
		complete = append(complete, domain.SkillFileTreeFile{Path: domain.SkillFileNameSKILLMD, Bytes: document, MediaType: "text/markdown", Executable: documentExecutable})
		complete = append(complete, bundles...)
		snapshot, err := domain.ValidateSkillFileTree(complete)
		if err != nil {
			return domain.SkillFileTreeSnapshot{}, err
		}
		return *snapshot, nil
	}
	snapshot, err := domain.BuildSkillFileTree(identity.Name, identity.Description, []byte(identity.Content), bundles)
	if err != nil {
		return domain.SkillFileTreeSnapshot{}, err
	}
	return *snapshot, nil
}

func skillFileMediaType(filePath string, body []byte) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt", ".log":
		return "text/plain"
	case ".sh", ".bash", ".zsh":
		return "text/x-shellscript"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".zip":
		return "application/zip"
	}
	return http.DetectContentType(body)
}
