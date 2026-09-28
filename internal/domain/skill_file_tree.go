package domain

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	// MaxSkillFiles is the number of bundled files allowed in addition to SKILL.md.
	MaxSkillFiles = 256
	// MaxSkillFileBytes bounds each bundled file while allowing arbitrary bytes.
	MaxSkillFileBytes = 128 << 10
	// MaxSkillFilesTotalBytes bounds all bundled files together.
	MaxSkillFilesTotalBytes = 1_000_000
)

// SkillFileTreeFile is one byte-bearing file in a complete Skill tree.
// Callers use it before publication and after downloading an immutable tree.
type SkillFileTreeFile struct {
	Path       string
	Bytes      []byte
	MediaType  string
	Executable bool
}

// ValidateSkillFileTree validates an already-complete tree without rewriting
// any bytes. Import/install callers use the parsed identity while preserving
// valid third-party SKILL.md frontmatter verbatim.
func ValidateSkillFileTree(files []SkillFileTreeFile) (*SkillFileTreeSnapshot, error) {
	detached := cloneSkillFileTreeFiles(files)
	if err := validateSkillFileTreeManifest(detached); err != nil {
		return nil, err
	}
	sort.Slice(detached, func(i, j int) bool { return detached[i].Path < detached[j].Path })
	var document []byte
	bundledCount := 0
	bundledBytes := 0
	for _, file := range detached {
		if file.Path == SkillFileNameSKILLMD {
			if document != nil {
				return nil, fmt.Errorf("skill tree must contain exactly one %s: %w", SkillFileNameSKILLMD, ErrInvalid)
			}
			if file.Executable {
				return nil, fmt.Errorf("skill tree %s must not be executable: %w", SkillFileNameSKILLMD, ErrInvalid)
			}
			if len(file.Bytes) > MaxSkillContentBytes {
				return nil, fmt.Errorf("skill tree %s must be at most %d bytes: %w", SkillFileNameSKILLMD, MaxSkillContentBytes, ErrInvalid)
			}
			document = file.Bytes
			continue
		}
		if err := ValidateSkillFilePath(file.Path); err != nil {
			return nil, err
		}
		bundledCount++
		if len(file.Bytes) > MaxSkillFileBytes {
			return nil, fmt.Errorf("skill tree file %q must be at most %d bytes: %w", file.Path, MaxSkillFileBytes, ErrInvalid)
		}
		bundledBytes += len(file.Bytes)
	}
	if document == nil {
		return nil, fmt.Errorf("skill tree must contain exactly one %s: %w", SkillFileNameSKILLMD, ErrInvalid)
	}
	if bundledCount > MaxSkillFiles {
		return nil, fmt.Errorf("skill tree must contain at most %d bundled files: %w", MaxSkillFiles, ErrInvalid)
	}
	if bundledBytes > MaxSkillFilesTotalBytes {
		return nil, fmt.Errorf("skill tree bundled files must total at most %d bytes: %w", MaxSkillFilesTotalBytes, ErrInvalid)
	}
	name, description, body, err := parseCompleteSkillDocument(document)
	if err != nil {
		return nil, err
	}
	return &SkillFileTreeSnapshot{
		Name: name, Description: description, Body: append([]byte(nil), body...), Files: detached,
	}, nil
}

func validateSkillFileTreeManifest(files []SkillFileTreeFile) error {
	manifest := make([]WorkspaceFile, 0, len(files))
	for _, file := range files {
		digest := sha256.Sum256(file.Bytes)
		manifest = append(manifest, WorkspaceFile{
			Path: file.Path, BlobRef: fmt.Sprintf("pending:%x", digest),
			ContentHash: fmt.Sprintf("sha256:%x", digest), SizeBytes: int64(len(file.Bytes)),
			MediaType: file.MediaType, Executable: file.Executable,
		})
	}
	if _, err := CanonicalWorkspaceFileManifest(manifest); err != nil {
		return fmt.Errorf("validate skill file tree paths: %w", err)
	}
	return nil
}

// SkillFileTreeSnapshot is a validated, detached Skill tree. Files contains
// the complete tree, including the root SKILL.md, in deterministic path order.
type SkillFileTreeSnapshot struct {
	Name        string
	Description string
	Body        []byte
	Files       []SkillFileTreeFile
}

// BuildSkillFileTree constructs the canonical SKILL.md used by CLI-authored
// Skills and combines it with the supplied bundled files.
func BuildSkillFileTree(name, description string, body []byte, bundled []SkillFileTreeFile) (*SkillFileTreeSnapshot, error) {
	if err := ValidateSkillName(name); err != nil {
		return nil, err
	}
	if err := ValidateSkillDescription(description); err != nil {
		return nil, err
	}
	frontmatter, err := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}{Name: name, Description: description})
	if err != nil {
		return nil, fmt.Errorf("render skill frontmatter: %w", err)
	}
	document := make([]byte, 0, len(frontmatter)+len(body)+8)
	document = append(document, "---\n"...)
	document = append(document, frontmatter...)
	document = append(document, "---\n"...)
	document = append(document, body...)
	files := cloneSkillFileTreeFiles(bundled)
	files = append(files, SkillFileTreeFile{Path: SkillFileNameSKILLMD, Bytes: document, MediaType: "text/markdown"})
	return ValidateSkillFileTree(files)
}

func cloneSkillFileTreeFiles(files []SkillFileTreeFile) []SkillFileTreeFile {
	out := make([]SkillFileTreeFile, len(files))
	for i, file := range files {
		out[i] = file
		out[i].Bytes = append([]byte(nil), file.Bytes...)
	}
	return out
}

func parseCompleteSkillDocument(document []byte) (string, string, []byte, error) {
	if !utf8.Valid(document) || bytes.IndexByte(document, 0) >= 0 {
		return "", "", nil, fmt.Errorf("%s must be UTF-8 text: %w", SkillFileNameSKILLMD, ErrInvalid)
	}
	first, next, ok := nextSkillDocumentLine(document, 0)
	if !ok || !bytes.Equal(bytes.TrimSuffix(first, []byte("\r")), []byte("---")) {
		return "", "", nil, fmt.Errorf("%s must begin with YAML frontmatter: %w", SkillFileNameSKILLMD, ErrInvalid)
	}
	frontmatterStart := next
	for offset := next; ; {
		line, following, found := nextSkillDocumentLine(document, offset)
		if !found {
			return "", "", nil, fmt.Errorf("%s frontmatter is missing its closing --- delimiter: %w", SkillFileNameSKILLMD, ErrInvalid)
		}
		if bytes.Equal(bytes.TrimSuffix(line, []byte("\r")), []byte("---")) {
			var metadata struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if err := yaml.Unmarshal(document[frontmatterStart:offset], &metadata); err != nil {
				return "", "", nil, fmt.Errorf("parse %s frontmatter: %v: %w", SkillFileNameSKILLMD, err, ErrInvalid)
			}
			if err := ValidateSkillName(metadata.Name); err != nil {
				return "", "", nil, err
			}
			if err := ValidateSkillDescription(metadata.Description); err != nil {
				return "", "", nil, err
			}
			return metadata.Name, metadata.Description, document[following:], nil
		}
		offset = following
	}
}

func nextSkillDocumentLine(document []byte, offset int) (line []byte, next int, ok bool) {
	if offset >= len(document) {
		return nil, offset, false
	}
	if newline := bytes.IndexByte(document[offset:], '\n'); newline >= 0 {
		end := offset + newline
		return document[offset:end], end + 1, true
	}
	return document[offset:], len(document), true
}
