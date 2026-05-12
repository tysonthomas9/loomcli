package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/runtimectx"
)

// SyncLatestCodexRollout mirrors the newest Codex rollout for workDir into the
// Loom session as agent_transcript.jsonl. It is best-effort and returns an
// empty path when no matching rollout is available.
func (s *Store) SyncLatestCodexRollout(sessionID, workDir string, since time.Time) (string, error) {
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.SyncLatestCodexRollout",
		attrLoomSessionID(sessionID),
		attrLoomBackend("codex"),
	)
	defer span.End()

	root := codexSessionsRoot()
	if root == "" {
		return "", nil
	}
	bestPath, err := findLatestCodexRollout(root, workDir, since)
	if err != nil {
		recordErr(span, err)
		return "", err
	}
	if bestPath == "" {
		return "", nil
	}
	if err := s.SyncNativeTranscript(sessionID, bestPath); err != nil {
		recordErr(span, err)
		return "", err
	}
	return bestPath, nil
}

func findLatestCodexRollout(root, workDir string, since time.Time) (string, error) {
	cutoff := since.Add(-1 * time.Minute)
	var bestPath string
	var bestMod time.Time
	for _, walkRoot := range codexSessionWalkRoots(root, since, time.Now()) {
		err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil || info.ModTime().Before(cutoff) {
				return nil
			}
			if workDir != "" && !codexRolloutMatchesWorkDir(path, workDir) {
				return nil
			}
			if bestPath == "" || info.ModTime().After(bestMod) {
				bestPath = path
				bestMod = info.ModTime()
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return bestPath, nil
}

func codexSessionsRoot() string {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil || userHome == "" {
			return ""
		}
		home = filepath.Join(userHome, ".codex")
	}
	root := filepath.Join(home, "sessions")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}

func codexSessionWalkRoots(root string, since, now time.Time) []string {
	if root == "" {
		return nil
	}
	cutoff := since.Add(-1 * time.Minute)
	if now.Before(cutoff) {
		now = cutoff
	}
	start := dateOnly(cutoff)
	end := dateOnly(now)
	if end.Sub(start) > 14*24*time.Hour {
		return []string{root}
	}
	var roots []string
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		dir := filepath.Join(root, fmt.Sprintf("%04d", day.Year()), fmt.Sprintf("%02d", int(day.Month())), fmt.Sprintf("%02d", day.Day()))
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			roots = append(roots, dir)
		}
	}
	if len(roots) == 0 {
		if hasCodexDateLayout(root) {
			return nil
		}
		return []string{root}
	}
	return roots
}

func hasCodexDateLayout(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 4 {
			continue
		}
		allDigits := true
		for _, r := range entry.Name() {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	return false
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func codexRolloutMatchesWorkDir(path, workDir string) bool {
	f, err := os.Open(path) //nolint:gosec // rollout path discovered under CODEX_HOME
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		var env struct {
			Type    string `json:"type"`
			Payload struct {
				CWD string `json:"cwd"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			continue
		}
		if env.Type != "session_meta" {
			continue
		}
		return sameCleanPath(env.Payload.CWD, workDir)
	}
	return false
}

func sameCleanPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}
