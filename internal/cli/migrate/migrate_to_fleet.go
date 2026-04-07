package migrate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// bdRunner abstracts shelling out to bd for testability.
type bdRunner interface {
	Run(args ...string) ([]byte, error)
}

// execBDRunner runs the real bd CLI.
type execBDRunner struct{}

func (e *execBDRunner) Run(args ...string) ([]byte, error) {
	cmd := exec.Command("bd", args...) //nolint:gosec // G204: intentional bd CLI invocation
	return cmd.Output()
}

// runMigrateToFleet orchestrates the migration from beads to fleet.
func runMigrateToFleet(cfg *migrateConfig) error {
	return runMigrateToFleetWithRunner(cfg, &execBDRunner{}, http.DefaultClient)
}

func runMigrateToFleetWithRunner(cfg *migrateConfig, bd bdRunner, client *http.Client) error {
	result := &migrateResult{}

	// Phase 1–3: read from beads
	details, err := migrateReadPhases(cfg, bd, client)
	if err != nil {
		return err
	}
	if details == nil {
		return nil // no issues found
	}

	if cfg.dryRun {
		depCount, commentCount := countDepsAndComments(details)
		fmt.Printf("\n[DRY RUN] Would create %d issues, add %d dependencies, add %d comments.\n", len(details), depCount, commentCount)
		return nil
	}

	// Phase 4–8: write to fleet
	migrateWritePhases(cfg, client, details, result)
	migrateReport(result)
	return nil
}

// migrateReadPhases handles preflight, enumeration, and detail reading.
func migrateReadPhases(cfg *migrateConfig, bd bdRunner, client *http.Client) ([]migrateIssueDetail, error) {
	fmt.Println("[1/8] Preflight checks...")
	if err := migratePreflight(cfg, bd, client); err != nil {
		return nil, fmt.Errorf("preflight failed: %w", err)
	}
	fmt.Println("  ✓ Preflight passed")

	fmt.Println("[2/8] Enumerating issues...")
	issues, err := migrateEnumerate(cfg, bd)
	if err != nil {
		return nil, fmt.Errorf("enumeration failed: %w", err)
	}
	fmt.Printf("  Found %d issues\n", len(issues))
	if len(issues) == 0 {
		fmt.Println("No issues found in beads backend. Nothing to migrate.")
		return nil, nil
	}

	fmt.Println("[3/8] Reading issue details...")
	details := migrateReadDetails(cfg, bd, issues)
	fmt.Printf("  Read %d issue details\n", len(details))
	return details, nil
}

// migrateWritePhases handles create, status, deps, comments, and config.
func migrateWritePhases(cfg *migrateConfig, client *http.Client, details []migrateIssueDetail, result *migrateResult) {
	fmt.Println("[4/8] Creating issues on fleet server...")
	migrateCreateIssues(cfg, client, details, result)
	fmt.Printf("  Created: %d, Skipped: %d, Failed: %d\n", result.created, result.skipped, result.failed)

	fmt.Println("[5/8] Updating issue statuses...")
	migrateUpdateStatuses(cfg, client, details, result)
	fmt.Println("  ✓ Statuses updated")

	fmt.Println("[6/8] Adding dependencies...")
	migrateAddDependencies(cfg, client, details, result)
	fmt.Printf("  Added: %d, Skipped: %d\n", result.depsAdded, result.depsSkipped)

	fmt.Println("[7/8] Adding comments...")
	migrateAddComments(cfg, client, details, result)
	fmt.Printf("  Added: %d comments\n", result.commentsAdded)

	if cfg.updateConfig {
		fmt.Println("[8/8] Updating loom.yaml...")
		if err := migrateUpdateConfig(cfg); err != nil {
			result.errors = append(result.errors, fmt.Sprintf("config update: %s", err))
		} else {
			fmt.Println("  ✓ loom.yaml updated")
		}
	} else {
		fmt.Println("[8/8] Skipping config update (use --update-config to enable)")
	}
}

func migrateReport(result *migrateResult) {
	fmt.Printf("\nMigration complete: %d created, %d skipped, %d failed, %d dependencies, %d comments.\n",
		result.created, result.skipped, result.failed, result.depsAdded, result.commentsAdded)
	if result.failed > 0 {
		fmt.Printf("  %d issues failed. Re-run to retry (successfully created items will be skipped).\n", result.failed)
	}
	if len(result.errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.errors {
			fmt.Printf("  - %s\n", e)
		}
	}
}

// migratePreflight validates bd CLI is available and fleet server is reachable.
func migratePreflight(cfg *migrateConfig, bd bdRunner, client *http.Client) error {
	if _, err := bd.Run("--version"); err != nil {
		return fmt.Errorf("bd CLI not found — required for reading beads data. Ensure beads is installed and bd is on PATH")
	}

	resp, err := fleetGet(cfg, client, "/health")
	if err != nil {
		return fmt.Errorf("cannot reach fleet server at %s: %w. Check the URL and ensure the server is running", cfg.fleetURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fleet server at %s returned HTTP %d on /health", cfg.fleetURL, resp.StatusCode)
	}

	resp, err = fleetGet(cfg, client, fmt.Sprintf("/api/workspaces/%s", cfg.workspace))
	if err != nil {
		return fmt.Errorf("checking workspace: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("workspace %q not found on fleet server. Create it first", cfg.workspace)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fleet server returned HTTP %d when checking workspace %q", resp.StatusCode, cfg.workspace)
	}

	return nil
}

// migrateEnumerate lists all issues from beads across all relevant statuses.
func migrateEnumerate(cfg *migrateConfig, bd bdRunner) ([]migrateIssue, error) {
	statuses := []string{"open", "in_progress", "review", "blocked", "deferred"}
	if cfg.includeClosed {
		statuses = append(statuses, "closed")
	}

	seen := make(map[string]bool)
	var all []migrateIssue

	for _, status := range statuses {
		out, err := bd.Run("list", "--status="+status, "--json", "--limit", "0")
		if err != nil {
			return nil, fmt.Errorf("listing %s issues: %w", status, err)
		}

		var issues []migrateIssue
		if err := json.Unmarshal(out, &issues); err != nil {
			return nil, fmt.Errorf("parsing %s issues: %w", status, err)
		}

		for _, issue := range issues {
			if !seen[issue.ID] {
				seen[issue.ID] = true
				all = append(all, issue)
			}
		}
	}

	return all, nil
}

// migrateReadDetails fetches full details for each issue via bd show.
func migrateReadDetails(cfg *migrateConfig, bd bdRunner, issues []migrateIssue) []migrateIssueDetail {
	details := make([]migrateIssueDetail, 0, len(issues))

	for i, issue := range issues {
		if cfg.batchSize > 0 && i > 0 && i%cfg.batchSize == 0 {
			fmt.Printf("  Reading issue details... %d/%d\n", i, len(issues))
		}

		out, err := bd.Run("show", issue.ID, "--json")
		if err != nil {
			fmt.Printf("  Warning: issue %s no longer exists in beads (may have been deleted). Skipping.\n", issue.ID)
			continue
		}

		var arr []migrateIssueDetail
		if err := json.Unmarshal(out, &arr); err != nil {
			fmt.Printf("  Warning: failed to parse details for %s: %s. Skipping.\n", issue.ID, err)
			continue
		}
		if len(arr) == 0 {
			fmt.Printf("  Warning: empty details for %s. Skipping.\n", issue.ID)
			continue
		}

		details = append(details, arr[0])
	}

	return details
}

// topologicalSort sorts issues so parents come before children.
// Returns the sorted list and a set of IDs involved in circular parent references.
func topologicalSort(issues []migrateIssueDetail) ([]migrateIssueDetail, map[string]bool) {
	byID := make(map[string]*migrateIssueDetail, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}

	circular := make(map[string]bool)
	depths := make(map[string]int)

	for _, issue := range issues {
		depth := 0
		visited := make(map[string]bool)
		current := issue.Parent

		for current != "" {
			if visited[current] {
				for id := range visited {
					circular[id] = true
				}
				circular[issue.ID] = true
				break
			}
			visited[current] = true
			depth++
			if p, ok := byID[current]; ok {
				current = p.Parent
			} else {
				break
			}
		}
		depths[issue.ID] = depth
	}

	sorted := make([]migrateIssueDetail, len(issues))
	copy(sorted, issues)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if depths[sorted[j].ID] < depths[sorted[i].ID] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted, circular
}

// buildIssueCreateBody constructs the JSON request body for creating an issue on fleet.
func buildIssueCreateBody(issue migrateIssueDetail, parent string) map[string]interface{} {
	req := map[string]interface{}{
		"id":         issue.ID,
		"title":      issue.Title,
		"issue_type": issue.IssueType,
		"priority":   issue.Priority,
	}
	if parent != "" {
		req["parent"] = parent
	}
	if issue.Description != "" {
		req["description"] = issue.Description
	}
	if issue.Design != "" {
		req["design"] = issue.Design
	}
	if issue.AcceptanceCriteria != "" {
		req["acceptance_criteria"] = issue.AcceptanceCriteria
	}
	if issue.Notes != "" {
		req["notes"] = issue.Notes
	}
	if issue.Assignee != "" {
		req["assignee"] = issue.Assignee
	}
	if issue.Owner != "" {
		req["owner"] = issue.Owner
	}
	if issue.CreatedBy != "" {
		req["created_by"] = issue.CreatedBy
	}
	if issue.ExternalRef != "" {
		req["external_ref"] = issue.ExternalRef
	}
	if issue.EstimatedMinutes != nil {
		req["estimated_minutes"] = *issue.EstimatedMinutes
	}
	if len(issue.Labels) > 0 {
		req["labels"] = issue.Labels
	}
	if issue.DueAt != nil {
		req["due_at"] = issue.DueAt.Format(time.RFC3339)
	}
	if issue.DeferUntil != nil {
		req["defer_until"] = issue.DeferUntil.Format(time.RFC3339)
	}
	return req
}

// migrateCreateIssues creates issues on the fleet server in topological order.
func migrateCreateIssues(cfg *migrateConfig, client *http.Client, issues []migrateIssueDetail, result *migrateResult) {
	sorted, circular := topologicalSort(issues)

	if len(circular) > 0 {
		ids := make([]string, 0, len(circular))
		for id := range circular {
			ids = append(ids, id)
		}
		fmt.Printf("  Warning: circular parent reference detected involving %s. These issues will be created without parent links.\n", strings.Join(ids, ", "))
	}

	for i, issue := range sorted {
		if cfg.batchSize > 0 && i > 0 && i%cfg.batchSize == 0 {
			fmt.Printf("  Creating issues... %d/%d\n", i, len(sorted))
		}

		parent := issue.Parent
		if circular[issue.ID] {
			parent = ""
		}

		body := buildIssueCreateBody(issue, parent)
		path := fmt.Sprintf("/api/workspaces/%s/issues", cfg.workspace)
		resp, err := fleetPost(cfg, client, path, body)
		if err != nil {
			result.failed++
			result.errors = append(result.errors, fmt.Sprintf("create %s: %s", issue.ID, err))
			continue
		}

		classifyCreateResponse(resp, issue.ID, result)
	}
}

func classifyCreateResponse(resp *http.Response, issueID string, result *migrateResult) {
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		result.created++
	case http.StatusConflict:
		result.skipped++
		fmt.Printf("  Skipped %s (already exists on fleet server)\n", issueID)
	case http.StatusRequestEntityTooLarge:
		result.failed++
		result.errors = append(result.errors, fmt.Sprintf("create %s: request body too large — content may need to be trimmed", issueID))
	default:
		body, _ := io.ReadAll(resp.Body)
		result.failed++
		result.errors = append(result.errors, fmt.Sprintf("create %s: HTTP %d: %s", issueID, resp.StatusCode, string(body)))
	}
}

// migrateUpdateStatuses patches issues to their correct status (non-open only).
func migrateUpdateStatuses(cfg *migrateConfig, client *http.Client, issues []migrateIssueDetail, result *migrateResult) {
	for _, issue := range issues {
		if issue.Status == "" || issue.Status == "open" {
			continue
		}

		req := map[string]interface{}{
			"status": issue.Status,
		}

		path := fmt.Sprintf("/api/workspaces/%s/issues/%s", cfg.workspace, issue.ID)
		resp, err := fleetPatch(cfg, client, path, req)
		if err != nil {
			result.errors = append(result.errors, fmt.Sprintf("status patch %s: %s", issue.ID, err))
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			result.errors = append(result.errors, fmt.Sprintf("status patch %s: HTTP %d", issue.ID, resp.StatusCode))
		}
	}
}

// migrateAddDependencies adds dependency relationships between issues.
func migrateAddDependencies(cfg *migrateConfig, client *http.Client, issues []migrateIssueDetail, result *migrateResult) {
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" || dep.DependencyType == "parent-child" {
				continue
			}

			depType := dep.Type
			if depType == "" {
				depType = "blocks"
			}

			req := map[string]interface{}{
				"depends_on_id": dep.DependsOnID,
				"dep_type":      depType,
			}

			path := fmt.Sprintf("/api/workspaces/%s/issues/%s/dependencies", cfg.workspace, issue.ID)
			resp, err := fleetPost(cfg, client, path, req)
			if err != nil {
				result.errors = append(result.errors, fmt.Sprintf("dependency %s→%s: %s", issue.ID, dep.DependsOnID, err))
				continue
			}
			resp.Body.Close()

			switch resp.StatusCode {
			case http.StatusOK, http.StatusCreated:
				result.depsAdded++
			case http.StatusConflict:
				result.depsSkipped++
			default:
				result.errors = append(result.errors, fmt.Sprintf("dependency %s→%s: HTTP %d", issue.ID, dep.DependsOnID, resp.StatusCode))
			}
		}
	}
}

// migrateAddComments adds comments to issues on the fleet server.
func migrateAddComments(cfg *migrateConfig, client *http.Client, issues []migrateIssueDetail, result *migrateResult) {
	for _, issue := range issues {
		for _, comment := range issue.Comments {
			author := comment.Author
			if author == "" {
				author = "system"
			}

			text := comment.Text
			if author != "web-ui" && author != "system" {
				text = fmt.Sprintf("[%s] %s", author, comment.Text)
			}

			req := map[string]interface{}{
				"text": text,
			}

			path := fmt.Sprintf("/api/workspaces/%s/issues/%s/comments", cfg.workspace, issue.ID)
			resp, err := fleetPost(cfg, client, path, req)
			if err != nil {
				result.errors = append(result.errors, fmt.Sprintf("comment on %s: %s", issue.ID, err))
				continue
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
				result.commentsAdded++
			} else {
				result.errors = append(result.errors, fmt.Sprintf("comment on %s: HTTP %d", issue.ID, resp.StatusCode))
			}
		}
	}
}

// countDepsAndComments counts total dependencies and comments across all issues.
func countDepsAndComments(issues []migrateIssueDetail) (int, int) {
	deps, comments := 0, 0
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			if dep.Type != "parent-child" && dep.DependencyType != "parent-child" {
				deps++
			}
		}
		comments += len(issue.Comments)
	}
	return deps, comments
}
