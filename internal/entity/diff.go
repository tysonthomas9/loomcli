package entity

import "fmt"

// FileStatus represents the type of change applied to a file in a diff.
type FileStatus string

// FileStatus constants mirror git's single-character status codes.
const (
	FileStatusModified FileStatus = "M" // File was modified
	FileStatusAdded    FileStatus = "A" // File was added (new)
	FileStatusDeleted  FileStatus = "D" // File was deleted
	FileStatusRenamed  FileStatus = "R" // File was renamed
)

// IsValid returns true for the four well-known constants and empty string (unset).
func (s FileStatus) IsValid() bool {
	switch s {
	case FileStatusModified, FileStatusAdded, FileStatusDeleted, FileStatusRenamed, "":
		return true
	}
	return false
}

// DiffCommit contains metadata for a single commit in a diff range.
type DiffCommit struct {
	// Hash is the full commit SHA.
	Hash string `json:"hash"`
	// ShortHash is the abbreviated commit SHA.
	ShortHash string `json:"short_hash"`
	// Subject is the first line of the commit message.
	Subject string `json:"subject"`
	// Author is the commit author's display name.
	Author string `json:"author"`
	// Email is the commit author's email address. May be empty.
	Email string `json:"email"`
	// Date is the commit date as an ISO 8601 string.
	Date string `json:"date"`
}

// Validate checks structural invariants of the commit metadata.
func (c *DiffCommit) Validate() error {
	if c.Hash == "" {
		return fmt.Errorf("hash is required")
	}
	if c.ShortHash == "" {
		return fmt.Errorf("short_hash is required")
	}
	if c.Subject == "" {
		return fmt.Errorf("subject is required")
	}
	if c.Author == "" {
		return fmt.Errorf("author is required")
	}
	if c.Date == "" {
		return fmt.Errorf("date is required")
	}
	return nil
}

// DiffFile contains the status and stats for a changed file in a diff.
type DiffFile struct {
	// Path is the file path (new path for renames).
	Path string `json:"path"`
	// Status is the change type: M, A, D, or R.
	Status FileStatus `json:"status"`
	// OldPath is the previous path; only populated when Status is FileStatusRenamed.
	OldPath string `json:"old_path,omitempty"`
	// Additions is the number of lines added.
	Additions int `json:"additions"`
	// Deletions is the number of lines removed.
	Deletions int `json:"deletions"`
}

// Validate checks structural invariants of the file diff entry.
func (f *DiffFile) Validate() error {
	if f.Path == "" {
		return fmt.Errorf("path is required")
	}
	if f.Status == "" {
		return fmt.Errorf("status is required")
	}
	if !f.Status.IsValid() {
		return fmt.Errorf("invalid file status: %s", f.Status)
	}
	if f.Status == FileStatusRenamed && f.OldPath == "" {
		return fmt.Errorf("old_path is required for rename status")
	}
	if f.Additions < 0 {
		return fmt.Errorf("additions must not be negative")
	}
	if f.Deletions < 0 {
		return fmt.Errorf("deletions must not be negative")
	}
	return nil
}

// IsRename returns true if the file was renamed.
func (f *DiffFile) IsRename() bool {
	return f.Status == FileStatusRenamed
}

// DiffPatch contains the unified diff patch for a single file.
type DiffPatch struct {
	// Patch is the unified diff content; empty for binary files or files exceeding the size limit.
	Patch string `json:"patch"`
	// IsBinary is true when the file is binary (no textual diff available).
	IsBinary bool `json:"is_binary"`
	// IsTooLarge is true when the diff exceeds the configured size threshold.
	IsTooLarge bool `json:"is_too_large"`
	// Additions is the number of lines added (independent of DiffFile).
	Additions int `json:"additions"`
	// Deletions is the number of lines removed (independent of DiffFile).
	Deletions int `json:"deletions"`
}

// Validate checks structural invariants of the patch.
func (p *DiffPatch) Validate() error {
	if p.IsBinary && p.Patch != "" {
		return fmt.Errorf("binary files must not have patch content")
	}
	if p.IsTooLarge && p.Patch != "" {
		return fmt.Errorf("oversized files must not have patch content")
	}
	if p.Additions < 0 {
		return fmt.Errorf("additions must not be negative")
	}
	if p.Deletions < 0 {
		return fmt.Errorf("deletions must not be negative")
	}
	return nil
}

// HasContent returns true if the patch has textual diff content.
func (p *DiffPatch) HasContent() bool {
	return p.Patch != ""
}

// IsViewable returns true if the patch can be displayed as text.
func (p *DiffPatch) IsViewable() bool {
	return !p.IsBinary && !p.IsTooLarge
}
