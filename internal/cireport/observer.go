package cireport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const observerCheckName = "Skills compatibility outcome classifier"

var (
	repositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	shaPattern            = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

type ObserverConfig struct {
	APIURL     string
	Token      string
	Repository string
	RunID      int64
	HeadSHA    string
	Client     *http.Client
}

type workflowRunResponse struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"head_sha"`
	CheckSuite int64  `json:"check_suite_id"`
}

type jobsResponse struct {
	TotalCount int   `json:"total_count"`
	Jobs       []Job `json:"jobs"`
}

type checkRunsResponse struct {
	CheckRuns []struct {
		Name   string `json:"name"`
		Output struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Text    string `json:"text"`
		} `json:"output"`
	} `json:"check_runs"`
}

func ObserveAndReport(ctx context.Context, config ObserverConfig) (Result, error) {
	baseURL, repoPath, err := validateObserverConfig(config)
	if err != nil {
		return Result{}, err
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}

	observation, err := collectObservation(ctx, client, config, baseURL, repoPath)
	if err != nil {
		return Result{}, err
	}
	result := Classify(observation)
	request := map[string]any{
		"name":       observerCheckName,
		"head_sha":   config.HeadSHA,
		"status":     "completed",
		"conclusion": result.Conclusion,
		"output": map[string]string{
			"title":   result.Title,
			"summary": result.Summary + " Category: " + string(result.Category) + ".",
		},
	}
	if err := postJSON(ctx, client, config.Token, baseURL+"/repos/"+repoPath+"/check-runs", request); err != nil {
		return Result{}, fmt.Errorf("publish observer check: %w", err)
	}
	return result, nil
}

func collectObservation(ctx context.Context, client *http.Client, config ObserverConfig, baseURL, repoPath string) (Observation, error) {
	var run workflowRunResponse
	if err := getJSON(ctx, client, config.Token, baseURL+"/repos/"+repoPath+"/actions/runs/"+strconv.FormatInt(config.RunID, 10), &run); err != nil {
		return Observation{}, fmt.Errorf("read workflow run: %w", err)
	}
	if !strings.EqualFold(run.HeadSHA, config.HeadSHA) {
		return Observation{}, fmt.Errorf("workflow head %q does not match requested head %q", run.HeadSHA, config.HeadSHA)
	}
	if run.CheckSuite <= 0 {
		return Observation{}, fmt.Errorf("workflow run has no check suite")
	}

	var jobs jobsResponse
	if err := getJSON(ctx, client, config.Token, baseURL+"/repos/"+repoPath+"/actions/runs/"+strconv.FormatInt(config.RunID, 10)+"/jobs?filter=latest&per_page=100", &jobs); err != nil {
		return Observation{}, fmt.Errorf("read workflow jobs: %w", err)
	}
	if jobs.TotalCount == 0 {
		jobs.Jobs = nil
	}

	var checks checkRunsResponse
	checksURL := baseURL + "/repos/" + repoPath + "/check-suites/" + strconv.FormatInt(run.CheckSuite, 10) + "/check-runs?per_page=100"
	if err := getJSON(ctx, client, config.Token, checksURL, &checks); err != nil {
		return Observation{}, fmt.Errorf("read workflow checks: %w", err)
	}
	observation := Observation{Run: Run{Status: run.Status, Conclusion: run.Conclusion}, Jobs: jobs.Jobs}
	for _, check := range checks.CheckRuns {
		if check.Name == observerCheckName {
			continue
		}
		observation.Diagnostics = append(observation.Diagnostics, check.Output.Title, check.Output.Summary, check.Output.Text)
	}
	return observation, nil
}

func validateObserverConfig(config ObserverConfig) (string, string, error) {
	parts := strings.Split(config.Repository, "/")
	if len(parts) != 2 || !repositoryPartPattern.MatchString(parts[0]) || !repositoryPartPattern.MatchString(parts[1]) {
		return "", "", fmt.Errorf("repository must be owner/name")
	}
	if config.RunID <= 0 {
		return "", "", fmt.Errorf("run ID must be positive")
	}
	if !shaPattern.MatchString(config.HeadSHA) {
		return "", "", fmt.Errorf("head SHA must be 40 hexadecimal characters")
	}
	if strings.TrimSpace(config.Token) == "" {
		return "", "", fmt.Errorf("GitHub token is required")
	}
	parsed, err := url.Parse(config.APIURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", fmt.Errorf("GitHub API URL must be absolute HTTP(S)")
	}
	repoPath := url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	return strings.TrimRight(parsed.String(), "/"), repoPath, nil
}

func getJSON(ctx context.Context, client *http.Client, token, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("GitHub API status %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func postJSON(ctx context.Context, client *http.Client, token, endpoint string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("GitHub API status %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return nil
}
