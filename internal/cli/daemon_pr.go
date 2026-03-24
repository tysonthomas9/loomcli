package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/events"
)

// ghAvailable checks if the gh CLI is installed.
// Cached after first call via sync.Once.
var ghAvailable = defaultGhAvailable

var (
	ghOnce      sync.Once
	ghInstalled bool
)

func defaultGhAvailable() bool {
	ghOnce.Do(func() {
		result := execCommand("", "gh", "--version")
		ghInstalled = result.Err == nil
	})
	return ghInstalled
}

// resetGhAvailableCache resets the sync.Once so ghAvailable re-evaluates.
// Only used in tests.
func resetGhAvailableCache() {
	ghOnce = sync.Once{}
	ghInstalled = false
}

// remoteBranchPushed checks if a branch exists on the remote by querying
// the remote directly (not local refs). Returns true if the branch has been pushed.
func remoteBranchPushed(dir, branch string) bool {
	result := execCommand(dir, "git", "ls-remote", "--heads", "origin", branch)
	if result.Err != nil {
		return false
	}
	return strings.TrimSpace(result.Stdout) != ""
}

// getOpenPRForBranch checks if an open PR exists for the given branch.
// Returns the PR URL if found, empty string if no open PR exists.
func getOpenPRForBranch(dir, branch string) (string, error) {
	result := execCommand(dir, "gh", "pr", "list", "--head", branch, "--state", "open", "--json", "url", "--limit", "1")
	if result.Err != nil {
		return "", fmt.Errorf("gh pr list failed: %w", result.Err)
	}

	var prs []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &prs); err != nil {
		return "", fmt.Errorf("failed to parse PR list: %w", err)
	}
	if len(prs) == 0 {
		return "", nil
	}
	return prs[0].URL, nil
}

// epicPRInfo holds parsed epic data for PR creation.
type epicPRInfo struct {
	Title    string
	ID       string
	Children []epicChild
}

// epicChild holds child task data for PR body generation.
type epicChild struct {
	ID     string
	Title  string
	Status string
}

// getEpicInfo queries bd for epic details including child tasks.
func getEpicInfo(epicID string) (*epicPRInfo, error) {
	result := execCommand(GetBeadsDir(), "bd", "show", epicID, "--json")
	if result.Err != nil {
		return nil, fmt.Errorf("bd show failed: %w", result.Err)
	}

	// bd show --json returns an array
	var issues []struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Dependents []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"dependents"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse epic info: %w", err)
	}
	if len(issues) == 0 {
		return nil, fmt.Errorf("no results for epic %s", epicID)
	}

	epic := issues[0]
	info := &epicPRInfo{
		Title: epic.Title,
		ID:    epic.ID,
	}
	for _, dep := range epic.Dependents {
		info.Children = append(info.Children, epicChild{
			ID:     dep.ID,
			Title:  dep.Title,
			Status: dep.Status,
		})
	}
	return info, nil
}

// buildPRBody generates the PR body markdown with child task checkboxes.
func buildPRBody(info *epicPRInfo) string {
	var b strings.Builder
	b.WriteString("## Epic: ")
	b.WriteString(info.Title)
	b.WriteString("\n\n### Tasks\n")

	if len(info.Children) == 0 {
		b.WriteString("No tasks yet.\n")
	} else {
		for _, child := range info.Children {
			if child.Status == "closed" {
				b.WriteString("- [x] ")
			} else {
				b.WriteString("- [ ] ")
			}
			b.WriteString(child.ID)
			b.WriteString(": ")
			b.WriteString(child.Title)
			b.WriteString(" (")
			b.WriteString(child.Status)
			b.WriteString(")\n")
		}
	}

	b.WriteString("\n---\n*Auto-created by loom daemon*\n")
	return b.String()
}

// createEpicPR creates a GitHub PR for an epic branch.
// Returns the PR URL on success.
func createEpicPR(resolver *Resolver, dir, epicID, branch string, info *epicPRInfo) (string, error) {
	title := fmt.Sprintf("%s (%s)", info.Title, epicID)
	body := buildPRBody(info)

	baseBranch := GetDefaultBranch()
	if resolver != nil {
		baseBranch = resolver.GetDefaultBranch()
	}

	result := execCommand(dir, "gh", "pr", "create",
		"--base", baseBranch,
		"--head", branch,
		"--title", title,
		"--body", body,
	)
	if result.Err != nil {
		return "", fmt.Errorf("gh pr create failed: %w", result.Err)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// storeExternalRef saves the PR URL in the epic's external_ref field.
func storeExternalRef(epicID, prURL string) error {
	result := execCommand(GetBeadsDir(), "bd", "update", epicID, "--external-ref", prURL)
	if result.Err != nil {
		return fmt.Errorf("bd update external-ref failed: %w", result.Err)
	}
	return nil
}

// EnsureEpicPR checks if a PR exists for the epic branch and creates one if needed.
// This is non-fatal — errors are returned but should not block agent restarts.
func EnsureEpicPR(resolver *Resolver, worktreePath, epicID string, eventBus events.Emitter) error {
	if eventBus == nil {
		eventBus = events.NopBus{}
	}
	// 1. Check if gh CLI is available
	if !ghAvailable() {
		log.Printf("[daemon] gh CLI not available, skipping PR creation for epic %s", epicID)
		return nil
	}

	// 2. Build branch name
	branch := epicBranchName(epicID)

	// 3. Check if branch has been pushed to remote
	if !remoteBranchPushed(worktreePath, branch) {
		log.Printf("[daemon] Branch %s not yet pushed, skipping PR creation", branch)
		return nil
	}

	// 4. Check if an open PR already exists
	prURL, err := getOpenPRForBranch(worktreePath, branch)
	if err != nil {
		return fmt.Errorf("failed to check for existing PR: %w", err)
	}
	if prURL != "" {
		// PR exists — ensure external_ref is stored
		if err := storeExternalRef(epicID, prURL); err != nil {
			log.Printf("[daemon] Warning: failed to store external_ref for epic %s: %v", epicID, err)
		}
		return nil
	}

	// 5. Get epic info for PR title and body
	info, err := getEpicInfo(epicID)
	if err != nil {
		return fmt.Errorf("failed to get epic info: %w", err)
	}

	// 6. Create the PR
	newURL, err := createEpicPR(resolver, worktreePath, epicID, branch, info)
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}
	log.Printf("[daemon] Created PR for epic %s: %s", epicID, newURL)

	// Emit pr_created event
	if evt, err := events.NewEvent(events.PRCreated, "", "", epicID, events.PRCreatedData{EpicID: epicID, URL: newURL}); err == nil {
		if emitErr := eventBus.Emit(evt); emitErr != nil {
			log.Printf("[daemon] Failed to emit pr_created event: %v", emitErr)
		}
	}

	// 7. Store PR URL in epic's external_ref
	if err := storeExternalRef(epicID, newURL); err != nil {
		log.Printf("[daemon] Warning: failed to store external_ref for epic %s: %v", epicID, err)
		// Non-fatal — PR was already created
	}

	return nil
}
