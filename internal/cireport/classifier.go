// Package cireport classifies completed GitHub Actions runs without turning
// absence of an infrastructure error into a product-test pass.
package cireport

import "strings"

type Category string

const (
	CategoryZeroJobs           Category = "zero-jobs"
	CategoryActionRequired     Category = "action-required"
	CategoryBilling            Category = "billing"
	CategoryInfrastructure     Category = "infrastructure"
	CategoryProductFailure     Category = "product-test-failure"
	CategoryObservedCompletion Category = "observed-completion"
)

type CheckConclusion string

const (
	ConclusionNeutral CheckConclusion = "neutral"
	ConclusionFailure CheckConclusion = "failure"
)

type Run struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type Job struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type Observation struct {
	Run         Run      `json:"run"`
	Jobs        []Job    `json:"jobs"`
	Diagnostics []string `json:"diagnostics,omitempty"`
}

type Result struct {
	Category   Category
	Conclusion CheckConclusion
	Title      string
	Summary    string
}

func Classify(observation Observation) Result {
	if hasBillingDiagnostic(observation.Diagnostics) {
		return infrastructureResult(CategoryBilling, "Billing prevented the workflow from running")
	}
	if observation.Run.Conclusion == "action_required" {
		return infrastructureResult(CategoryActionRequired, "The workflow requires external action")
	}
	if len(observation.Jobs) == 0 {
		return infrastructureResult(CategoryZeroJobs, "The workflow completed without creating a job")
	}
	if isInfrastructureConclusion(observation.Run.Conclusion) {
		return infrastructureResult(CategoryInfrastructure, "The workflow ended before a product result was established")
	}
	for _, job := range observation.Jobs {
		if job.Conclusion == "failure" {
			return Result{
				Category:   CategoryProductFailure,
				Conclusion: ConclusionFailure,
				Title:      "Product test job failed",
				Summary:    "At least one executed workflow job reported failure.",
			}
		}
	}
	return Result{
		Category:   CategoryObservedCompletion,
		Conclusion: ConclusionNeutral,
		Title:      "Workflow completion observed",
		Summary:    "The observer does not certify product success; use the originating workflow for its test result.",
	}
}

func infrastructureResult(category Category, title string) Result {
	return Result{
		Category:   category,
		Conclusion: ConclusionNeutral,
		Title:      title,
		Summary:    "This is an infrastructure classification, not a product-test result.",
	}
}

func isInfrastructureConclusion(conclusion string) bool {
	switch conclusion {
	case "startup_failure", "action_required", "cancelled", "stale", "skipped":
		return true
	default:
		return false
	}
}

func hasBillingDiagnostic(diagnostics []string) bool {
	for _, diagnostic := range diagnostics {
		value := strings.ToLower(diagnostic)
		for _, phrase := range []string{
			"billing", "payment required", "account payments", "spending limit", "actions minutes quota",
		} {
			if strings.Contains(value, phrase) {
				return true
			}
		}
	}
	return false
}
