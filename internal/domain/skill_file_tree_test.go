package domain

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func TestBuildSkillFileTreeRendersCanonicalDocumentAndPreservesBinaryFiles(t *testing.T) {
	t.Parallel()

	archive := []byte{'P', 'K', 0x03, 0x04, 0x00, 0xff}
	snapshot, err := BuildSkillFileTree("deploy-tool", "Deploy an application", []byte("# Deploy\n"), []SkillFileTreeFile{
		{Path: "Archive.zip", Bytes: archive, MediaType: "application/zip", Executable: true},
	})
	if err != nil {
		t.Fatalf("BuildSkillFileTree: %v", err)
	}
	if got, want := snapshot.Name, "deploy-tool"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got, want := snapshot.Description, "Deploy an application"; got != want {
		t.Fatalf("Description = %q, want %q", got, want)
	}
	if !bytes.Equal(snapshot.Body, []byte("# Deploy\n")) {
		t.Fatalf("Body = %q", snapshot.Body)
	}
	if got, want := len(snapshot.Files), 2; got != want {
		t.Fatalf("len(Files) = %d, want %d", got, want)
	}
	if got, want := snapshot.Files[0].Path, "Archive.zip"; got != want {
		t.Fatalf("Files[0].Path = %q, want deterministic path order %q", got, want)
	}
	if !bytes.Equal(snapshot.Files[0].Bytes, archive) || snapshot.Files[0].MediaType != "application/zip" || !snapshot.Files[0].Executable {
		t.Fatalf("binary file metadata was not preserved: %#v", snapshot.Files[0])
	}
	wantDocument := []byte("---\nname: deploy-tool\ndescription: Deploy an application\n---\n# Deploy\n")
	if got := snapshot.Files[1]; got.Path != SkillFileNameSKILLMD || !bytes.Equal(got.Bytes, wantDocument) || got.Executable {
		t.Fatalf("SKILL.md = %#v, want canonical non-executable document %q", got, wantDocument)
	}
}

func TestBuildSkillFileTreeReturnsDefensiveDeterministicCopies(t *testing.T) {
	t.Parallel()

	body := []byte("body\n")
	binary := []byte{0xff, 0x00, 0x01}
	bundled := []SkillFileTreeFile{
		{Path: "z.bin", Bytes: binary},
		{Path: "a.txt", Bytes: []byte("a")},
	}
	first, err := BuildSkillFileTree("copy-tool", "Copy tool", body, bundled)
	if err != nil {
		t.Fatalf("first BuildSkillFileTree: %v", err)
	}
	second, err := BuildSkillFileTree("copy-tool", "Copy tool", body, bundled)
	if err != nil {
		t.Fatalf("second BuildSkillFileTree: %v", err)
	}
	if got, want := []string{first.Files[0].Path, first.Files[1].Path, first.Files[2].Path}, []string{SkillFileNameSKILLMD, "a.txt", "z.bin"}; !slices.Equal(got, want) {
		t.Fatalf("file order = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated builds differ:\nfirst=%#v\nsecond=%#v", first, second)
	}
	body[0] = 'X'
	binary[0] = 'X'
	bundled[1].Bytes[0] = 'X'
	if !bytes.Equal(first.Body, []byte("body\n")) || !bytes.Equal(first.Files[1].Bytes, []byte("a")) || !bytes.Equal(first.Files[2].Bytes, []byte{0xff, 0x00, 0x01}) {
		t.Fatal("built snapshot aliases caller-owned bytes")
	}
}

func TestValidateSkillFileTreeRequiresOneNonExecutableRootDocument(t *testing.T) {
	t.Parallel()

	validDocument := []byte("---\nname: valid-tool\ndescription: Valid tool\n---\nbody\n")
	tests := []struct {
		name  string
		files []SkillFileTreeFile
	}{
		{name: "missing", files: []SkillFileTreeFile{{Path: "README.md", Bytes: []byte("readme")}}},
		{name: "executable", files: []SkillFileTreeFile{{Path: SkillFileNameSKILLMD, Bytes: validDocument, Executable: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateSkillFileTree(tt.files); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ValidateSkillFileTree error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateSkillFileTreeEnforcesSkillSizeLimits(t *testing.T) {
	t.Parallel()

	documentPrefix := []byte("---\nname: sized-tool\ndescription: Sized tool\n---\n")
	tests := []struct {
		name  string
		files []SkillFileTreeFile
	}{
		{
			name: "SKILL.md too large",
			files: []SkillFileTreeFile{{
				Path: SkillFileNameSKILLMD, Bytes: append(documentPrefix, bytes.Repeat([]byte("s"), MaxSkillContentBytes-len(documentPrefix)+1)...),
			}},
		},
		{
			name: "bundled file too large",
			files: []SkillFileTreeFile{
				{Path: SkillFileNameSKILLMD, Bytes: documentPrefix},
				{Path: "large.bin", Bytes: bytes.Repeat([]byte{0x01}, MaxSkillFileBytes+1)},
			},
		},
		{
			name: "bundled total too large",
			files: append(
				[]SkillFileTreeFile{{Path: SkillFileNameSKILLMD, Bytes: documentPrefix}},
				bundleFiles(8, 125_001)...,
			),
		},
		{
			name: "too many bundled files",
			files: append(
				[]SkillFileTreeFile{{Path: SkillFileNameSKILLMD, Bytes: documentPrefix}},
				bundleFiles(MaxSkillFiles+1, 0)...,
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateSkillFileTree(tt.files); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ValidateSkillFileTree error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateSkillFileTreeReusesWorkspacePathCollisionRules(t *testing.T) {
	t.Parallel()

	document := []byte("---\nname: paths-tool\ndescription: Paths tool\n---\n")
	tests := []struct {
		name  string
		files []SkillFileTreeFile
	}{
		{
			name: "case folded SKILL.md",
			files: []SkillFileTreeFile{
				{Path: SkillFileNameSKILLMD, Bytes: document},
				{Path: "skill.md", Bytes: []byte("collision")},
			},
		},
		{
			name: "unicode normalized bundle",
			files: []SkillFileTreeFile{
				{Path: SkillFileNameSKILLMD, Bytes: document},
				{Path: "references/café.md", Bytes: []byte("one")},
				{Path: "references/café.md", Bytes: []byte("two")},
			},
		},
		{
			name: "file and directory",
			files: []SkillFileTreeFile{
				{Path: SkillFileNameSKILLMD, Bytes: document},
				{Path: "references", Bytes: []byte("file")},
				{Path: "references/api.md", Bytes: []byte("nested")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateSkillFileTree(tt.files); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ValidateSkillFileTree error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateSkillFileTreeAcceptsExactLimits(t *testing.T) {
	t.Parallel()

	documentPrefix := []byte("---\nname: limits-tool\ndescription: Limits tool\n---\n")
	document := append([]byte(nil), documentPrefix...)
	document = append(document, bytes.Repeat([]byte("s"), MaxSkillContentBytes-len(documentPrefix))...)
	bundled := bundleFiles(MaxSkillFiles, 0)
	for i := range bundled {
		size := MaxSkillFilesTotalBytes / MaxSkillFiles
		if i < MaxSkillFilesTotalBytes%MaxSkillFiles {
			size++
		}
		bundled[i].Bytes = bytes.Repeat([]byte{byte(i)}, size)
	}
	files := append([]SkillFileTreeFile{{Path: SkillFileNameSKILLMD, Bytes: document}}, bundled...)
	if _, err := ValidateSkillFileTree(files); err != nil {
		t.Fatalf("ValidateSkillFileTree exact limits: %v", err)
	}
}

func bundleFiles(count, size int) []SkillFileTreeFile {
	files := make([]SkillFileTreeFile, count)
	for i := range files {
		files[i] = SkillFileTreeFile{
			Path:  fmt.Sprintf("files/%03d.bin", i),
			Bytes: bytes.Repeat([]byte{byte(i)}, size),
		}
	}
	return files
}

func TestValidateSkillFileTreePreservesCompleteImportedDocument(t *testing.T) {
	t.Parallel()

	document := []byte("---\r\nname: imported-tool\r\ndescription: Use <ViewTransition> without rewriting metadata\r\nlicense: MIT\r\nmetadata:\r\n  owner: tools\r\n---\r\n# Imported\r\n")
	wantDocument := append([]byte(nil), document...)
	archive := []byte{0x00, 0xff, 0x10, 0x80}
	wantArchive := append([]byte(nil), archive...)
	input := []SkillFileTreeFile{
		{Path: SkillFileNameSKILLMD, Bytes: document, MediaType: "text/markdown"},
		{Path: "assets/data.bin", Bytes: archive, MediaType: "application/octet-stream"},
	}
	snapshot, err := ValidateSkillFileTree(input)
	if err != nil {
		t.Fatalf("ValidateSkillFileTree: %v", err)
	}
	if got, want := snapshot.Name, "imported-tool"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got, want := snapshot.Description, "Use <ViewTransition> without rewriting metadata"; got != want {
		t.Fatalf("Description = %q, want %q", got, want)
	}
	if !bytes.Equal(snapshot.Body, []byte("# Imported\r\n")) {
		t.Fatalf("Body = %q", snapshot.Body)
	}
	var importedDocument []byte
	var importedArchive []byte
	for _, file := range snapshot.Files {
		if file.Path == SkillFileNameSKILLMD {
			importedDocument = file.Bytes
		}
		if file.Path == "assets/data.bin" {
			importedArchive = file.Bytes
		}
	}
	if !bytes.Equal(importedDocument, wantDocument) {
		t.Fatalf("imported SKILL.md was rewritten:\n got %q\nwant %q", importedDocument, wantDocument)
	}
	input[0].Bytes[0] = 'X'
	input[1].Bytes[0] = 'X'
	if !bytes.Equal(importedDocument, wantDocument) || !bytes.Equal(importedArchive, wantArchive) {
		t.Fatal("validated snapshot aliases caller-owned bytes")
	}
}

func TestValidateSkillFileTreeRejectsInvalidInstructionDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		document []byte
	}{
		{name: "not text", document: []byte{0xff, 0xfe}},
		{name: "missing frontmatter", document: []byte("# Body\n")},
		{name: "unclosed frontmatter", document: []byte("---\nname: tool\ndescription: Tool\n")},
		{name: "invalid yaml", document: []byte("---\nname: [\ndescription: Tool\n---\n")},
		{name: "missing name", document: []byte("---\ndescription: Tool\n---\n")},
		{name: "missing description", document: []byte("---\nname: tool\n---\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateSkillFileTree([]SkillFileTreeFile{{Path: SkillFileNameSKILLMD, Bytes: tt.document}})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ValidateSkillFileTree error = %v, want ErrInvalid", err)
			}
		})
	}
}
