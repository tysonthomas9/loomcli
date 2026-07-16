package git

import (
	"fmt"
	"path/filepath"
	"strings"
)

const emptyTreeObjectID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func cleanGitFilePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be repository-relative")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("path must stay within the repository")
	}
	return clean, nil
}

func validateHistoryRev(rev string) error {
	if rev == "" {
		return fmt.Errorf("git revision is required")
	}
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("invalid git revision %q", rev)
	}
	if strings.Contains(rev, "..") || strings.Contains(rev, ":") || strings.ContainsAny(rev, " \t\r\n\x00") {
		return fmt.Errorf("invalid git revision %q", rev)
	}
	for _, r := range rev {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '/' || r == '.' || r == '~' || r == '^' {
			continue
		}
		return fmt.Errorf("invalid git revision %q", rev)
	}
	return nil
}
