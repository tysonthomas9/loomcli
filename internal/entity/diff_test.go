package entity

import (
	"strings"
	"testing"
)

func TestFileStatus_IsValid(t *testing.T) {
	tests := []struct {
		status FileStatus
		want   bool
	}{
		{FileStatusModified, true},
		{FileStatusAdded, true},
		{FileStatusDeleted, true},
		{FileStatusRenamed, true},
		{"", true},
		{"C", false},
		{"X", false},
		{"modified", false},
	}
	for _, tt := range tests {
		name := string(tt.status)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("FileStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestDiffCommit_Validate(t *testing.T) {
	validCommit := func() *DiffCommit {
		return &DiffCommit{
			Hash:      "abc123def456",
			ShortHash: "abc123d",
			Subject:   "fix: resolve nil pointer",
			Author:    "Test User",
			Email:     "test@example.com",
			Date:      "2026-03-31T12:00:00Z",
		}
	}

	t.Run("valid commit passes", func(t *testing.T) {
		if err := validCommit().Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid commit with empty email passes", func(t *testing.T) {
		c := validCommit()
		c.Email = ""
		if err := c.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty hash fails", func(t *testing.T) {
		c := validCommit()
		c.Hash = ""
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "hash is required") {
			t.Errorf("error %q should contain %q", err, "hash is required")
		}
	})

	t.Run("empty short_hash fails", func(t *testing.T) {
		c := validCommit()
		c.ShortHash = ""
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "short_hash is required") {
			t.Errorf("error %q should contain %q", err, "short_hash is required")
		}
	})

	t.Run("empty subject fails", func(t *testing.T) {
		c := validCommit()
		c.Subject = ""
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "subject is required") {
			t.Errorf("error %q should contain %q", err, "subject is required")
		}
	})

	t.Run("empty author fails", func(t *testing.T) {
		c := validCommit()
		c.Author = ""
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "author is required") {
			t.Errorf("error %q should contain %q", err, "author is required")
		}
	})

	t.Run("empty date fails", func(t *testing.T) {
		c := validCommit()
		c.Date = ""
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "date is required") {
			t.Errorf("error %q should contain %q", err, "date is required")
		}
	})
}

func TestDiffFile_Validate(t *testing.T) {
	validFile := func() *DiffFile {
		return &DiffFile{
			Path:      "internal/entity/diff.go",
			Status:    FileStatusModified,
			Additions: 10,
			Deletions: 5,
		}
	}

	t.Run("valid modified file passes", func(t *testing.T) {
		if err := validFile().Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid added file passes", func(t *testing.T) {
		f := validFile()
		f.Status = FileStatusAdded
		if err := f.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid deleted file passes", func(t *testing.T) {
		f := validFile()
		f.Status = FileStatusDeleted
		if err := f.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid renamed file passes", func(t *testing.T) {
		f := &DiffFile{
			Path:      "new/path.go",
			Status:    FileStatusRenamed,
			OldPath:   "old/path.go",
			Additions: 0,
			Deletions: 0,
		}
		if err := f.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty path fails", func(t *testing.T) {
		f := validFile()
		f.Path = ""
		err := f.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("error %q should contain %q", err, "path is required")
		}
	})

	t.Run("empty status fails", func(t *testing.T) {
		f := validFile()
		f.Status = ""
		err := f.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "status is required") {
			t.Errorf("error %q should contain %q", err, "status is required")
		}
	})

	t.Run("invalid status fails", func(t *testing.T) {
		f := validFile()
		f.Status = "X"
		err := f.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "invalid file status") {
			t.Errorf("error %q should contain %q", err, "invalid file status")
		}
	})

	t.Run("rename with empty old_path fails", func(t *testing.T) {
		f := &DiffFile{
			Path:   "new/path.go",
			Status: FileStatusRenamed,
		}
		err := f.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "old_path is required for rename status") {
			t.Errorf("error %q should contain %q", err, "old_path is required for rename status")
		}
	})

	t.Run("modified with non-empty old_path passes", func(t *testing.T) {
		f := validFile()
		f.OldPath = "some/old/path.go"
		if err := f.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("negative additions fails", func(t *testing.T) {
		f := validFile()
		f.Additions = -1
		err := f.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "additions must not be negative") {
			t.Errorf("error %q should contain %q", err, "additions must not be negative")
		}
	})

	t.Run("negative deletions fails", func(t *testing.T) {
		f := validFile()
		f.Deletions = -1
		err := f.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "deletions must not be negative") {
			t.Errorf("error %q should contain %q", err, "deletions must not be negative")
		}
	})

	t.Run("zero additions and deletions passes", func(t *testing.T) {
		f := validFile()
		f.Additions = 0
		f.Deletions = 0
		if err := f.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestDiffFile_IsRename(t *testing.T) {
	tests := []struct {
		name   string
		status FileStatus
		want   bool
	}{
		{"renamed", FileStatusRenamed, true},
		{"modified", FileStatusModified, false},
		{"added", FileStatusAdded, false},
		{"deleted", FileStatusDeleted, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &DiffFile{Status: tt.status}
			if got := f.IsRename(); got != tt.want {
				t.Errorf("DiffFile{Status: %q}.IsRename() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestDiffPatch_Validate(t *testing.T) {
	t.Run("valid patch with content passes", func(t *testing.T) {
		p := &DiffPatch{
			Patch:     "diff --git a/foo.go b/foo.go\n+line",
			Additions: 1,
			Deletions: 0,
		}
		if err := p.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty patch non-binary non-too-large passes", func(t *testing.T) {
		p := &DiffPatch{}
		if err := p.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("binary with empty patch passes", func(t *testing.T) {
		p := &DiffPatch{IsBinary: true}
		if err := p.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("binary with non-empty patch fails", func(t *testing.T) {
		p := &DiffPatch{IsBinary: true, Patch: "+line"}
		err := p.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "binary files must not have patch content") {
			t.Errorf("error %q should contain %q", err, "binary files must not have patch content")
		}
	})

	t.Run("too large with empty patch passes", func(t *testing.T) {
		p := &DiffPatch{IsTooLarge: true}
		if err := p.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("too large with non-empty patch fails", func(t *testing.T) {
		p := &DiffPatch{IsTooLarge: true, Patch: "+line"}
		err := p.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "oversized files must not have patch content") {
			t.Errorf("error %q should contain %q", err, "oversized files must not have patch content")
		}
	})

	t.Run("binary and too large with empty patch passes", func(t *testing.T) {
		p := &DiffPatch{IsBinary: true, IsTooLarge: true}
		if err := p.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("binary and too large with non-empty patch fails", func(t *testing.T) {
		p := &DiffPatch{IsBinary: true, IsTooLarge: true, Patch: "+line"}
		err := p.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "binary files must not have patch content") {
			t.Errorf("error %q should contain %q", err, "binary files must not have patch content")
		}
	})

	t.Run("negative additions fails", func(t *testing.T) {
		p := &DiffPatch{Additions: -1}
		err := p.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "additions must not be negative") {
			t.Errorf("error %q should contain %q", err, "additions must not be negative")
		}
	})

	t.Run("negative deletions fails", func(t *testing.T) {
		p := &DiffPatch{Deletions: -1}
		err := p.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "deletions must not be negative") {
			t.Errorf("error %q should contain %q", err, "deletions must not be negative")
		}
	})
}

func TestDiffPatch_HasContent(t *testing.T) {
	tests := []struct {
		name  string
		patch string
		want  bool
	}{
		{"non-empty patch", "diff --git a/foo.go b/foo.go", true},
		{"empty patch", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &DiffPatch{Patch: tt.patch}
			if got := p.HasContent(); got != tt.want {
				t.Errorf("DiffPatch{Patch: %q}.HasContent() = %v, want %v", tt.patch, got, tt.want)
			}
		})
	}
}

func TestDiffPatch_IsViewable(t *testing.T) {
	tests := []struct {
		name       string
		isBinary   bool
		isTooLarge bool
		want       bool
	}{
		{"not binary not too large", false, false, true},
		{"binary", true, false, false},
		{"too large", false, true, false},
		{"binary and too large", true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &DiffPatch{IsBinary: tt.isBinary, IsTooLarge: tt.isTooLarge}
			if got := p.IsViewable(); got != tt.want {
				t.Errorf("DiffPatch{IsBinary: %v, IsTooLarge: %v}.IsViewable() = %v, want %v",
					tt.isBinary, tt.isTooLarge, got, tt.want)
			}
		})
	}
}
